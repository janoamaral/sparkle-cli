package profiler

import (
	"io"
	"sync"
	"time"
)

type GenerationWriter struct {
	inner io.Writer

	mu      sync.Mutex
	started bool
	first   time.Time
	last    time.Time
	total   int64
}

func NewGenerationWriter(inner io.Writer) *GenerationWriter {
	if inner == nil {
		inner = io.Discard
	}
	return &GenerationWriter{inner: inner}
}

func (w *GenerationWriter) Write(p []byte) (int, error) {
	n, err := w.inner.Write(p)
	if n > 0 {
		now := time.Now().UTC()
		w.mu.Lock()
		if !w.started {
			w.started = true
			w.first = now
		}
		w.last = now
		w.total += int64(n)
		w.mu.Unlock()
	}
	return n, err
}

func (w *GenerationWriter) Window() (time.Time, time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.first, w.last
}

func (w *GenerationWriter) Duration() time.Duration {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.started || w.last.Before(w.first) {
		return 0
	}
	return w.last.Sub(w.first)
}

func (w *GenerationWriter) Bytes() int64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.total
}
