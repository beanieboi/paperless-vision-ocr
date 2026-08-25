package jobs

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestRepositoryLifecycle(t *testing.T) {
	now := time.Now().UTC()
	repo := NewRepository(time.Hour)
	job := Job{ID: "one", Status: NotStarted, CreatedAt: now, UpdatedAt: now}
	if err := repo.Create(job); err != nil {
		t.Fatal(err)
	}
	if err := repo.Update("one", func(j *Job) { j.Status = Succeeded; j.OCRText = "hello"; j.UpdatedAt = now.Add(time.Minute) }); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Get("one")
	if err != nil || got.OCRText != "hello" || got.Status != Succeeded {
		t.Fatalf("got %#v, %v", got, err)
	}
	if err := repo.Create(job); err == nil {
		t.Fatal("duplicate create succeeded")
	}
}

func TestRepositoryConcurrentAccess(t *testing.T) {
	repo := NewRepository(time.Hour)
	now := time.Now().UTC()
	if err := repo.Create(Job{ID: "job", Status: Running, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 50 {
		wg.Add(2)
		go func() { defer wg.Done(); _, _ = repo.Get("job") }()
		go func() { defer wg.Done(); _ = repo.Update("job", func(j *Job) { j.UpdatedAt = time.Now().UTC() }) }()
	}
	wg.Wait()
}

func TestExpiration(t *testing.T) {
	now := time.Now().UTC()
	repo := NewRepository(time.Hour)
	_ = repo.Create(Job{ID: "old", Status: Succeeded, CreatedAt: now.Add(-3 * time.Hour), UpdatedAt: now.Add(-2 * time.Hour)})
	_ = repo.Create(Job{ID: "running", Status: Running, CreatedAt: now.Add(-3 * time.Hour), UpdatedAt: now.Add(-2 * time.Hour)})
	removed := repo.Expire(now, time.Hour)
	if len(removed) != 1 || removed[0].ID != "old" {
		t.Fatalf("removed %#v", removed)
	}
	if _, err := repo.Get("old"); !errors.Is(err, ErrExpired) {
		t.Fatalf("error = %v", err)
	}
	if _, err := repo.Get("running"); err != nil {
		t.Fatal(err)
	}
}

func TestNewID(t *testing.T) {
	a, err := NewID()
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewID()
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != 36 || a == b {
		t.Fatalf("IDs %q %q", a, b)
	}
}
