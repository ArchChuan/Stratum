package handler

import (
	"fmt"
	"net/http"
	"sync"
	"time"
)

type sseEvent struct {
	comment string
	name    string
	data    string
}

func (w *sseEventWriter) EnqueueEvent(name, data string) bool {
	return w.enqueue(sseEvent{name: name, data: data})
}

type sseEventWriter struct {
	w       http.ResponseWriter
	flusher http.Flusher
	events  chan sseEvent
	mu      sync.Mutex
	closed  bool
}

func newSSEEventWriter(w http.ResponseWriter) *sseEventWriter {
	flusher, _ := w.(http.Flusher)
	return &sseEventWriter{
		w:       w,
		flusher: flusher,
		events:  make(chan sseEvent, 128),
	}
}

func (w *sseEventWriter) EnqueueData(data string) bool {
	return w.enqueue(sseEvent{data: data})
}

func (w *sseEventWriter) EnqueueComment(comment string) bool {
	return w.enqueue(sseEvent{comment: comment})
}

func (w *sseEventWriter) enqueue(ev sseEvent) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return false
	}
	w.events <- ev
	return true
}

func (w *sseEventWriter) Close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	w.closed = true
	close(w.events)
}

func (w *sseEventWriter) WriteUntilClosed(timeout time.Duration) {
	if timeout <= 0 {
		for ev := range w.events {
			w.write(ev)
		}
		return
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case ev, ok := <-w.events:
			if !ok {
				return
			}
			w.write(ev)
		case <-timer.C:
			return
		}
	}
}

func (w *sseEventWriter) write(ev sseEvent) {
	if ev.comment != "" {
		_, _ = fmt.Fprintf(w.w, ": %s\n\n", ev.comment)
	} else {
		if ev.name != "" {
			_, _ = fmt.Fprintf(w.w, "event: %s\n", ev.name)
		}
		_, _ = fmt.Fprintf(w.w, "data: %s\n\n", ev.data)
	}
	if w.flusher != nil {
		w.flusher.Flush()
	}
}
