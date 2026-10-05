package provider

import (
	"errors"
	"io"
	"sync"
	"time"
)

var (
	// ErrStreamStalled is returned when a stream does not receive any chunk for the configured idle duration
	ErrStreamStalled = errors.New("stream stalled: no chunks received within idle timeout")
)

// WatchdogReader wraps an io.ReadCloser with an idle timeout.
// If no Read call succeeds within idleTimeout, the underlying reader is closed
// and subsequent reads return ErrStreamStalled.
type WatchdogReader struct {
	reader      io.ReadCloser
	idleTimeout time.Duration
	timer       *time.Timer
	mu          sync.Mutex
	stalled     bool
	closed      bool
}

// NewWatchdogReader creates a new WatchdogReader with the specified idle timeout.
// A zero or negative timeout disables the watchdog and returns the original reader.
func NewWatchdogReader(r io.ReadCloser, idleTimeout time.Duration) *WatchdogReader {
	if r == nil {
		return nil
	}
	if idleTimeout <= 0 {
		idleTimeout = 35 * time.Second
	}

	w := &WatchdogReader{
		reader:      r,
		idleTimeout: idleTimeout,
	}

	w.timer = time.AfterFunc(idleTimeout, w.onTimeout)
	return w
}

func (w *WatchdogReader) onTimeout() {
	w.mu.Lock()
	if w.closed || w.stalled {
		w.mu.Unlock()
		return
	}
	w.stalled = true
	w.mu.Unlock()

	// Close underlying reader to force unblocking any stuck Read calls
	_ = w.reader.Close()
}

// Read implements io.Reader with active watchdog timer resets
func (w *WatchdogReader) Read(p []byte) (n int, err error) {
	w.mu.Lock()
	if w.stalled {
		w.mu.Unlock()
		return 0, ErrStreamStalled
	}
	if w.closed {
		w.mu.Unlock()
		return 0, io.EOF
	}
	// Reset watchdog before and after blocking read
	if w.timer != nil {
		w.timer.Reset(w.idleTimeout)
	}
	w.mu.Unlock()

	n, err = w.reader.Read(p)

	w.mu.Lock()
	defer w.mu.Unlock()

	if w.stalled {
		return n, ErrStreamStalled
	}

	if err != nil {
		// Stop timer on error or EOF
		if w.timer != nil {
			w.timer.Stop()
		}
		return n, err
	}

	// Data was received, reset timer for next chunk
	if w.timer != nil && !w.closed && !w.stalled {
		w.timer.Reset(w.idleTimeout)
	}

	return n, nil
}

// Close closes the underlying reader and stops the watchdog timer
func (w *WatchdogReader) Close() error {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return nil
	}
	w.closed = true
	if w.timer != nil {
		w.timer.Stop()
	}
	w.mu.Unlock()

	return w.reader.Close()
}
