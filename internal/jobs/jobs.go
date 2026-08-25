package jobs

import (
	"crypto/rand"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"
)

type Status string

const (
	NotStarted Status = "notStarted"
	Running    Status = "running"
	Succeeded  Status = "succeeded"
	Failed     Status = "failed"
)

var (
	ErrNotFound = errors.New("job not found")
	ErrExpired  = errors.New("job expired")
)

type Job struct {
	ID             string
	Status         Status
	CreatedAt      time.Time
	UpdatedAt      time.Time
	StartedAt      time.Time
	FinishedAt     time.Time
	InputPath      string
	OutputPDFPath  string
	OCRText        string
	ErrorCode      string
	ErrorMessage   string
	Diagnostic     string
	Duration       time.Duration
	QueueDuration  time.Duration
	OCRDuration    time.Duration
	UploadDuration time.Duration
	InputBytes     int64
	OutputBytes    int64
	ExitCode       int
}

func NewID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", value[0:4], value[4:6], value[6:8], value[8:10], value[10:16]), nil
}

type Repository struct {
	mu           sync.RWMutex
	jobs         map[string]Job
	expired      map[string]time.Time
	tombstoneTTL time.Duration
}

func NewRepository(tombstoneTTL time.Duration) *Repository {
	return &Repository{jobs: make(map[string]Job), expired: make(map[string]time.Time), tombstoneTTL: tombstoneTTL}
}

func (r *Repository) Create(job Job) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.jobs[job.ID]; exists {
		return fmt.Errorf("job %s already exists", job.ID)
	}
	r.jobs[job.ID] = job
	return nil
}

func (r *Repository) Get(id string) (Job, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if job, ok := r.jobs[id]; ok {
		return job, nil
	}
	if _, ok := r.expired[id]; ok {
		return Job{}, ErrExpired
	}
	return Job{}, ErrNotFound
}

func (r *Repository) Update(id string, update func(*Job)) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	job, ok := r.jobs[id]
	if !ok {
		return ErrNotFound
	}
	update(&job)
	r.jobs[id] = job
	return nil
}

func (r *Repository) Remove(id string) { r.mu.Lock(); delete(r.jobs, id); r.mu.Unlock() }

func (r *Repository) Expire(now time.Time, ttl time.Duration) []Job {
	r.mu.Lock()
	defer r.mu.Unlock()
	var removed []Job
	for id, job := range r.jobs {
		if (job.Status == Running || job.Status == NotStarted) || now.Sub(job.UpdatedAt) < ttl {
			continue
		}
		removed = append(removed, job)
		delete(r.jobs, id)
		r.expired[id] = now.Add(r.tombstoneTTL)
	}
	for id, deadline := range r.expired {
		if !deadline.After(now) {
			delete(r.expired, id)
		}
	}
	return removed
}

func (r *Repository) ActiveDirectory(path string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, job := range r.jobs {
		if job.InputPath == filepath.Join(path, "input.pdf") || job.OutputPDFPath == filepath.Join(path, "output.pdf") {
			return true
		}
	}
	return false
}
