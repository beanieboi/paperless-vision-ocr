package api

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/ben/paperless-macos-ocr/internal/azure"
	"github.com/ben/paperless-macos-ocr/internal/jobs"
	"github.com/ben/paperless-macos-ocr/internal/ocr"
)

type Submitter interface{ Submit(string) bool }

type Server struct {
	repo           *jobs.Repository
	submitter      Submitter
	runner         ocr.Runner
	workDir        string
	apiKey         string
	maxUploadBytes int64
	logger         *slog.Logger
	handler        http.Handler
}

func New(repo *jobs.Repository, submitter Submitter, runner ocr.Runner, workDir, apiKey string, maxUploadBytes int64, logger *slog.Logger) *Server {
	server := &Server{repo: repo, submitter: submitter, runner: runner, workDir: workDir, apiKey: apiKey, maxUploadBytes: maxUploadBytes, logger: logger}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", server.health)
	mux.HandleFunc("GET /ready", server.ready)
	mux.HandleFunc("POST /documentintelligence/documentModels/prebuilt-read:analyze", server.authorize(server.analyze))
	mux.HandleFunc("GET /documentintelligence/documentModels/{model}/analyzeResults/{id}", server.authorize(server.result))
	mux.HandleFunc("GET /documentintelligence/documentModels/{model}/analyzeResults/{id}/pdf", server.authorize(server.pdf))
	server.handler = server.requestLog(mux)
	return server
}

func (s *Server) Handler() http.Handler { return s.handler }

func (s *Server) requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)
		s.logger.Debug("HTTP request", "method", r.Method, "path", r.URL.Path, "status", recorder.status, "duration_ms", time.Since(started).Milliseconds())
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (s *Server) authorize(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.apiKey != "" {
			expected := sha256.Sum256([]byte(s.apiKey))
			provided := sha256.Sum256([]byte(r.Header.Get("Ocp-Apim-Subscription-Key")))
			if subtle.ConstantTimeCompare(expected[:], provided[:]) != 1 {
				writeError(w, http.StatusUnauthorized, "Unauthorized", "Access denied due to an invalid subscription key.")
				return
			}
		}
		next(w, r)
	}
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	if err := s.runner.Ready(r.Context()); err != nil {
		writeError(w, http.StatusServiceUnavailable, "ServiceUnavailable", "mac-ocr is not ready.")
		return
	}
	file, err := os.CreateTemp(s.workDir, ".ready-*")
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "ServiceUnavailable", "OCR work directory is not writable.")
		return
	}
	name := file.Name()
	closeErr := file.Close()
	removeErr := os.Remove(name)
	if closeErr != nil || removeErr != nil {
		writeError(w, http.StatusServiceUnavailable, "ServiceUnavailable", "OCR work directory is not writable.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (s *Server) analyze(w http.ResponseWriter, r *http.Request) {
	started := time.Now().UTC()
	query := r.URL.Query()
	if query.Get("api-version") != azure.APIVersion || query.Get("outputContentFormat") != "text" || query.Get("output") != "pdf" {
		writeError(w, http.StatusBadRequest, "InvalidRequest", "Unsupported Azure Document Intelligence request options.")
		return
	}
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "UnsupportedContentType", "Content-Type must be application/json.")
		return
	}

	id, err := jobs.NewID()
	if err != nil {
		s.internalError(w, "generate job ID", err)
		return
	}
	directory := filepath.Join(s.workDir, id)
	if err := os.Mkdir(directory, 0700); err != nil {
		s.internalError(w, "create job directory", err)
		return
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(directory)
		}
	}()
	inputPath, outputPath := filepath.Join(directory, "input.pdf"), filepath.Join(directory, "output.pdf")
	input, err := os.OpenFile(inputPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		s.internalError(w, "create input PDF", err)
		return
	}
	encodedLimit := ((s.maxUploadBytes + 2) / 3 * 4) + 4096
	r.Body = http.MaxBytesReader(w, r.Body, encodedLimit)
	inputBytes, decodeErr := azure.DecodeAnalyzeRequest(r.Body, input, s.maxUploadBytes)
	closeErr := input.Close()
	if decodeErr != nil {
		var maxErr *http.MaxBytesError
		if errors.Is(decodeErr, azure.ErrUploadTooLarge) || errors.As(decodeErr, &maxErr) {
			writeError(w, http.StatusRequestEntityTooLarge, "ContentTooLarge", "The uploaded document exceeds the configured size limit.")
		} else {
			writeError(w, http.StatusBadRequest, "InvalidRequest", "The request body must contain a valid base64Source PDF.")
		}
		return
	}
	if closeErr != nil {
		s.internalError(w, "close input PDF", closeErr)
		return
	}
	if err := validatePDF(inputPath); err != nil {
		writeError(w, http.StatusBadRequest, "InvalidContent", "The uploaded document is not a valid PDF.")
		return
	}
	now := time.Now().UTC()
	job := jobs.Job{ID: id, Status: jobs.NotStarted, CreatedAt: started, UpdatedAt: now, InputPath: inputPath, OutputPDFPath: outputPath, InputBytes: inputBytes, UploadDuration: now.Sub(started), ExitCode: -1}
	if err := s.repo.Create(job); err != nil {
		s.internalError(w, "create job", err)
		return
	}
	if !s.submitter.Submit(id) {
		s.repo.Remove(id)
		writeError(w, http.StatusServiceUnavailable, "ServiceBusy", "The local OCR queue is full; retry later.")
		return
	}
	keep = true
	operation := absoluteURL(r, fmt.Sprintf("/documentintelligence/documentModels/%s/analyzeResults/%s?api-version=%s", azure.ModelID, id, azure.APIVersion))
	w.Header().Set("Operation-Location", operation)
	w.Header().Set("Retry-After", "1")
	w.Header().Set("x-ms-request-id", id)
	w.WriteHeader(http.StatusAccepted)
	s.logger.Info("OCR job accepted", "job_id", id, "status", jobs.NotStarted, "input_bytes", inputBytes, "upload_duration_ms", now.Sub(started).Milliseconds())
}

func (s *Server) result(w http.ResponseWriter, r *http.Request) {
	if !validCommonRequest(r) {
		writeError(w, http.StatusBadRequest, "InvalidRequest", "Unsupported model or API version.")
		return
	}
	job, err := s.repo.Get(r.PathValue("id"))
	if err != nil {
		writeLookupError(w, err)
		return
	}
	response := map[string]any{"status": job.Status, "createdDateTime": job.CreatedAt.Format(time.RFC3339Nano), "lastUpdatedDateTime": job.UpdatedAt.Format(time.RFC3339Nano)}
	switch job.Status {
	case jobs.Succeeded:
		response["analyzeResult"] = map[string]any{"apiVersion": azure.APIVersion, "modelId": azure.ModelID, "stringIndexType": "textElements", "contentFormat": "text", "content": job.OCRText, "pages": []any{}}
	case jobs.Failed:
		response["error"] = map[string]string{"code": job.ErrorCode, "message": job.ErrorMessage}
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) pdf(w http.ResponseWriter, r *http.Request) {
	if !validCommonRequest(r) {
		writeError(w, http.StatusBadRequest, "InvalidRequest", "Unsupported model or API version.")
		return
	}
	job, err := s.repo.Get(r.PathValue("id"))
	if err != nil {
		writeLookupError(w, err)
		return
	}
	if job.Status != jobs.Succeeded {
		writeError(w, http.StatusConflict, "ResultNotReady", "The searchable PDF is not available until analysis succeeds.")
		return
	}
	file, err := os.Open(job.OutputPDFPath)
	if err != nil {
		s.internalError(w, "open output PDF", err)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		s.internalError(w, "stat output PDF", err)
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", info.Size()))
	http.ServeContent(w, r, "result.pdf", info.ModTime(), file)
}

func validCommonRequest(r *http.Request) bool {
	return r.PathValue("model") == azure.ModelID && r.URL.Query().Get("api-version") == azure.APIVersion
}

func validatePDF(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	header := make([]byte, 5)
	if _, err := io.ReadFull(file, header); err != nil {
		return err
	}
	if string(header) != "%PDF-" {
		return errors.New("missing PDF signature")
	}
	return nil
}

func absoluteURL(r *http.Request, path string) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host + path
}

func writeLookupError(w http.ResponseWriter, err error) {
	if errors.Is(err, jobs.ErrExpired) {
		writeError(w, http.StatusNotFound, "ResultExpired", "The analysis result has expired.")
		return
	}
	writeError(w, http.StatusNotFound, "NotFound", "The requested analysis result was not found.")
}

func (s *Server) internalError(w http.ResponseWriter, operation string, err error) {
	s.logger.Error("internal API error", "operation", operation, "error", err)
	writeError(w, http.StatusInternalServerError, "InternalServerError", "An internal error occurred.")
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
