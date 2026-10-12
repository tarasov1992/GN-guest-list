package cloudsync

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"checkin/internal/guest"
	"checkin/internal/guestfile"
)

// maxFileSize caps how much of the download is read; the real file is ~75 KB.
const maxFileSize = 10 << 20

type Importer interface {
	Import(ctx context.Context, guests []guest.Guest) error
}

type Status struct {
	Running     bool
	LastRun     time.Time
	LastSuccess time.Time
	Unchanged   bool   // the last successful run found the same file
	Guests      int    // guests in the last imported file
	Err         string // error of the last run, empty if it succeeded
}

type Worker struct {
	client   *Client
	importer Importer
	interval time.Duration
	trigger  chan struct{}

	// lastHash is only touched by the Run goroutine.
	lastHash string

	mu     sync.Mutex
	status Status
}

func NewWorker(client *Client, importer Importer, interval time.Duration) *Worker {
	return &Worker{
		client:   client,
		importer: importer,
		interval: interval,
		trigger:  make(chan struct{}, 1),
	}
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
	return w.status
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
	meta, err := w.client.Meta(ctx)
	if err != nil {
		return 0, false, err
	}
	if !force && meta.SHA256 == w.lastHash {
		return 0, true, nil
	}

	body, err := w.client.Download(ctx)
	if err != nil {
		return 0, false, err
	}
	defer body.Close()

	parsed, err := guestfile.Parse(io.LimitReader(body, maxFileSize))
	if err != nil {
		return 0, false, fmt.Errorf("parse: %w", err)
	}
	if err := w.importer.Import(ctx, parsed); err != nil {
		return 0, false, fmt.Errorf("import: %w", err)
	}

	w.lastHash = meta.SHA256
	return len(parsed), false, nil
}
