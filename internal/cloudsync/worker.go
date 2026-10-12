package cloudsync

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"slices"
	"sync"
	"time"

	"checkin/internal/guest"
	"checkin/internal/guestfile"
)

const (
	SourceYD = "YD"
	SourceGD = "GD"
)

// maxFileSize caps how much of the download is read; the real file is ~75 KB.
const maxFileSize = 10 << 20

type Importer interface {
	Import(ctx context.Context, guests []guest.Guest) error
}

type Status struct {
	Source        string
	DefaultSource string
	Available     []string
	Running       bool
	LastRun       time.Time
	LastSuccess   time.Time
	Unchanged     bool   // the last successful run found the same file
	Guests        int    // guests in the last imported file
	Err           string // error of the last run, empty if it succeeded
}

type Worker struct {
	// Set once in NewWorker, never changed afterwards.
	sources       map[string]Source
	available     []string // sorted source names, for the admin switch
	defaultSource string
	importer      Importer
	interval      time.Duration
	trigger       chan struct{}

	// lastHash is only touched by the Run goroutine.
	lastHash string

	mu      sync.Mutex // guards current and status
	current string
	status  Status
}

func NewWorker(sources map[string]Source, defaultSource string, importer Importer, interval time.Duration) (*Worker, error) {
	if _, ok := sources[defaultSource]; !ok {
		return nil, fmt.Errorf("default source %q is not configured", defaultSource)
	}
	return &Worker{
		sources:       sources,
		available:     slices.Sorted(maps.Keys(sources)),
		defaultSource: defaultSource,
		current:       defaultSource,
		importer:      importer,
		interval:      interval,
		trigger:       make(chan struct{}, 1),
	}, nil
}

// Run syncs once right away, then on every tick and on every Trigger,
// until ctx is cancelled. Failures are logged and kept in Status; they never stop the loop.
func (w *Worker) Run(ctx context.Context) {
	w.syncOnce(ctx, false)

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.syncOnce(ctx, false)
		case <-w.trigger:
			w.syncOnce(ctx, true)
		}
	}
}

// Trigger asks for a forced sync. Repeated calls while one is pending collapse into one.
func (w *Worker) Trigger() {
	select {
	case w.trigger <- struct{}{}:
	default:
	}
}

func (w *Worker) Status() Status {
	w.mu.Lock()
	defer w.mu.Unlock()
	s := w.status
	s.Source = w.current
	s.DefaultSource = w.defaultSource
	s.Available = w.available
	return s
}

func (w *Worker) syncOnce(ctx context.Context, force bool) {
	w.mu.Lock()
	w.status.Running = true
	w.mu.Unlock()

	n, unchanged, err := w.sync(ctx, force)

	w.mu.Lock()
	defer w.mu.Unlock()
	now := time.Now()
	w.status.Running = false
	w.status.LastRun = now

	if err != nil {
		if ctx.Err() != nil {
			return // shutting down
		}
		w.status.Err = err.Error()
		slog.Error("sync failed", "err", err)
		return
	}
	w.status.Err = ""
	w.status.LastSuccess = now
	w.status.Unchanged = unchanged
	if unchanged {
		slog.Info("sync: file unchanged")
		return
	}
	w.status.Guests = n
	slog.Info("sync: imported", "guests", n)
}

func (w *Worker) sync(ctx context.Context, force bool) (guests int, unchanged bool, err error) {
	name := w.currentSource()
	body, err := w.sources[name].Fetch(ctx)
	if err != nil {
		return 0, false, fmt.Errorf("%s: %w", name, err)
	}
	defer body.Close()

	data, err := io.ReadAll(io.LimitReader(body, maxFileSize+1))
	if err != nil {
		return 0, false, fmt.Errorf("%s: read: %w", name, err)
	}
	if len(data) > maxFileSize {
		return 0, false, fmt.Errorf("%s: file is larger than %d MB", name, maxFileSize>>20)
	}

	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	if !force && hash == w.lastHash {
		return 0, true, nil
	}

	parsed, err := guestfile.Parse(bytes.NewReader(data))
	if err != nil {
		return 0, false, fmt.Errorf("%s: parse: %w", name, err)
	}
	if err := w.importer.Import(ctx, parsed); err != nil {
		return 0, false, fmt.Errorf("%s: import: %w", name, err)
	}

	w.lastHash = hash
	return len(parsed), false, nil
}

func (w *Worker) SetSource(name string) error {
	if _, ok := w.sources[name]; !ok {
		return fmt.Errorf("source %q isn't configured", name)
	}
	w.mu.Lock()
	w.current = name
	w.mu.Unlock()
	w.Trigger()
	return nil
}

func (w *Worker) currentSource() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.current
}
