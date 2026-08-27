package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path"
	"strings"
	"testing"
	"time"

	"github.com/beanieboi/paperless-mac-ocr/internal/azure"
	"github.com/beanieboi/paperless-mac-ocr/internal/jobs"
	"github.com/beanieboi/paperless-mac-ocr/internal/ocr"
)

type fakeSubmitter struct {
	accept bool
	ids    []string
}

func (f *fakeSubmitter) Submit(id string) bool { f.ids = append(f.ids, id); return f.accept }

type readyRunner struct{ err error }

func (r *readyRunner) Ready(context.Context) error                              { return r.err }
func (r *readyRunner) Process(context.Context, ocr.Request) (ocr.Result, error) { panic("not called") }

type apiFixture struct {
	server    *Server
	repo      *jobs.Repository
	submitter *fakeSubmitter
	handler   http.Handler
	work      string
}

func newFixture(t *testing.T, key string, limit int64) *apiFixture {
	t.Helper()
	work := t.TempDir()
	repo := jobs.NewRepository(time.Hour)
	submitter := &fakeSubmitter{accept: true}
	server := New(repo, submitter, &readyRunner{}, work, key, limit, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return &apiFixture{server: server, repo: repo, submitter: submitter, handler: server.Handler(), work: work}
}

func analyzeURL() string {
	return "/documentintelligence/documentModels/prebuilt-read:analyze?api-version=" + azure.APIVersion + "&outputContentFormat=text&output=pdf"
}
func analyzeRequest(pdf []byte) *http.Request {
	body := `{"base64Source":"` + base64.StdEncoding.EncodeToString(pdf) + `"}`
	req := httptest.NewRequest(http.MethodPost, "http://mac.example:8080"+analyzeURL(), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Ocp-Apim-Subscription-Key", "secret")
	return req
}
func submit(t *testing.T, fixture *apiFixture) (*httptest.ResponseRecorder, string) {
	t.Helper()
	response := httptest.NewRecorder()
	fixture.handler.ServeHTTP(response, analyzeRequest([]byte("%PDF-fake")))
	if response.Code != http.StatusAccepted {
		t.Fatalf("status %d: %s", response.Code, response.Body.String())
	}
	location, err := url.Parse(response.Header().Get("Operation-Location"))
	if err != nil {
		t.Fatal(err)
	}
	return response, path.Base(location.Path)
}

func TestHealthAndReady(t *testing.T) {
	fixture := newFixture(t, "", 100)
	for _, endpoint := range []string{"/health", "/ready"} {
		response := httptest.NewRecorder()
		fixture.handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, endpoint, nil))
		if response.Code != 200 {
			t.Fatalf("%s status %d", endpoint, response.Code)
		}
	}
	fixture.server.runner = &readyRunner{err: errors.New("missing")}
	response := httptest.NewRecorder()
	fixture.handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/ready", nil))
	if response.Code != 503 {
		t.Fatalf("ready status %d", response.Code)
	}
}

func TestAuthentication(t *testing.T) {
	fixture := newFixture(t, "correct", 100)
	req := analyzeRequest([]byte("%PDF-fake"))
	req.Header.Set("Ocp-Apim-Subscription-Key", "wrong")
	response := httptest.NewRecorder()
	fixture.handler.ServeHTTP(response, req)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status %d", response.Code)
	}
	if len(fixture.submitter.ids) != 0 {
		t.Fatal("unauthorized request was submitted")
	}
}

func TestAnalyzeAcceptedAndRunning(t *testing.T) {
	fixture := newFixture(t, "secret", 100)
	response, id := submit(t, fixture)
	location := response.Header().Get("Operation-Location")
	if !strings.HasPrefix(location, "http://mac.example:8080/documentintelligence/") || !strings.Contains(location, id) {
		t.Fatalf("location %q", location)
	}
	if response.Header().Get("Retry-After") != "1" {
		t.Fatalf("retry after %q", response.Header().Get("Retry-After"))
	}
	job, err := fixture.repo.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != jobs.NotStarted || job.InputBytes != 9 {
		t.Fatalf("job %#v", job)
	}
	result := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, location, nil)
	req.Header.Set("Ocp-Apim-Subscription-Key", "secret")
	fixture.handler.ServeHTTP(result, req)
	if result.Code != 200 || !strings.Contains(result.Body.String(), `"status":"notStarted"`) {
		t.Fatalf("result %d %s", result.Code, result.Body.String())
	}
}

func TestSucceededResultAndPDF(t *testing.T) {
	fixture := newFixture(t, "", 100)
	_, id := submit(t, fixture)
	job, _ := fixture.repo.Get(id)
	if err := os.WriteFile(job.OutputPDFPath, []byte("%PDF-output"), 0600); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	_ = fixture.repo.Update(id, func(j *jobs.Job) {
		j.Status = jobs.Succeeded
		j.OCRText = "Rechnung 12345"
		j.UpdatedAt = now
		j.OutputBytes = 11
	})
	base := "/documentintelligence/documentModels/prebuilt-read/analyzeResults/" + id
	response := httptest.NewRecorder()
	fixture.handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, base+"?api-version="+azure.APIVersion, nil))
	if response.Code != 200 {
		t.Fatalf("status %d: %s", response.Code, response.Body.String())
	}
	var payload struct {
		AnalyzeResult struct {
			Content string `json:"content"`
			Pages   []any  `json:"pages"`
		} `json:"analyzeResult"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.AnalyzeResult.Content != "Rechnung 12345" || payload.AnalyzeResult.Pages == nil {
		t.Fatalf("payload %#v", payload)
	}
	pdf := httptest.NewRecorder()
	fixture.handler.ServeHTTP(pdf, httptest.NewRequest(http.MethodGet, base+"/pdf?api-version="+azure.APIVersion, nil))
	if pdf.Code != 200 || pdf.Header().Get("Content-Type") != "application/pdf" || !bytes.Equal(pdf.Body.Bytes(), []byte("%PDF-output")) {
		t.Fatalf("pdf %d %q %q", pdf.Code, pdf.Header().Get("Content-Type"), pdf.Body.Bytes())
	}
}

func TestFailedAndUnknownResults(t *testing.T) {
	fixture := newFixture(t, "", 100)
	_, id := submit(t, fixture)
	now := time.Now().UTC()
	_ = fixture.repo.Update(id, func(j *jobs.Job) {
		j.Status = jobs.Failed
		j.ErrorCode = "OCROperationFailed"
		j.ErrorMessage = "failed"
		j.UpdatedAt = now
	})
	base := "/documentintelligence/documentModels/prebuilt-read/analyzeResults/" + id
	response := httptest.NewRecorder()
	fixture.handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, base+"?api-version="+azure.APIVersion, nil))
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"status":"failed"`) {
		t.Fatalf("failed %d %s", response.Code, response.Body.String())
	}
	pdf := httptest.NewRecorder()
	fixture.handler.ServeHTTP(pdf, httptest.NewRequest(http.MethodGet, base+"/pdf?api-version="+azure.APIVersion, nil))
	if pdf.Code != 409 {
		t.Fatalf("pdf status %d", pdf.Code)
	}
	unknown := httptest.NewRecorder()
	fixture.handler.ServeHTTP(unknown, httptest.NewRequest(http.MethodGet, "/documentintelligence/documentModels/prebuilt-read/analyzeResults/missing?api-version="+azure.APIVersion, nil))
	if unknown.Code != 404 {
		t.Fatalf("unknown status %d", unknown.Code)
	}
}

func TestExpiredResult(t *testing.T) {
	fixture := newFixture(t, "", 100)
	_, id := submit(t, fixture)
	old := time.Now().UTC().Add(-2 * time.Hour)
	_ = fixture.repo.Update(id, func(j *jobs.Job) { j.Status = jobs.Succeeded; j.UpdatedAt = old })
	fixture.repo.Expire(time.Now().UTC(), time.Hour)
	response := httptest.NewRecorder()
	fixture.handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/documentintelligence/documentModels/prebuilt-read/analyzeResults/"+id+"?api-version="+azure.APIVersion, nil))
	if response.Code != http.StatusNotFound || !strings.Contains(response.Body.String(), "ResultExpired") {
		t.Fatalf("expired response %d %s", response.Code, response.Body.String())
	}
}

func TestMalformedAndOversizedAnalyze(t *testing.T) {
	fixture := newFixture(t, "", 8)
	tests := []struct {
		req    *http.Request
		status int
	}{
		{httptest.NewRequest(http.MethodPost, analyzeURL(), strings.NewReader(`{"base64Source":"bad"}`)), http.StatusUnsupportedMediaType},
		{analyzeRequest([]byte("not pdf data")), http.StatusRequestEntityTooLarge},
		{analyzeRequest([]byte("tiny")), http.StatusBadRequest},
	}
	for _, test := range tests {
		response := httptest.NewRecorder()
		fixture.handler.ServeHTTP(response, test.req)
		if response.Code != test.status {
			t.Errorf("status %d, want %d: %s", response.Code, test.status, response.Body.String())
		}
	}
}

func TestQueueFull(t *testing.T) {
	fixture := newFixture(t, "", 100)
	fixture.submitter.accept = false
	response := httptest.NewRecorder()
	fixture.handler.ServeHTTP(response, analyzeRequest([]byte("%PDF-fake")))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d", response.Code)
	}
}
