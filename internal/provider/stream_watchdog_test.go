package provider

import (
	"bytes"
	"errors"
	"io"
	"testing"
	"time"
)

type slowReader struct {
	data   []byte
	delay  time.Duration
	called bool
}

func (s *slowReader) Read(p []byte) (n int, err error) {
	if s.called {
		time.Sleep(s.delay)
		return 0, io.EOF
	}
	s.called = true
	time.Sleep(s.delay)
	copy(p, s.data)
	return len(s.data), nil
}

func (s *slowReader) Close() error {
	return nil
}

func TestWatchdogReader_NormalRead(t *testing.T) {
	src := io.NopCloser(bytes.NewBufferString("hello world"))
	w := NewWatchdogReader(src, 100*time.Millisecond)
	defer w.Close()

	buf := make([]byte, 32)
	n, err := w.Read(buf)
	if err != nil && err != io.EOF {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(buf[:n]) != "hello world" {
		t.Fatalf("expected 'hello world', got %s", string(buf[:n]))
	}
}

func TestWatchdogReader_Timeout(t *testing.T) {
	src := &slowReader{
		data:  []byte("chunk1"),
		delay: 80 * time.Millisecond,
	}
	// Idle timeout 40ms, so 80ms delay should trigger stall
	w := NewWatchdogReader(src, 40*time.Millisecond)
	defer w.Close()

	buf := make([]byte, 32)
	_, err := w.Read(buf)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !errors.Is(err, ErrStreamStalled) {
		t.Fatalf("expected ErrStreamStalled, got %v", err)
	}
}
