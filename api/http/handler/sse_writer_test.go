package handler

import (
	"bytes"
	"net/http"
	"testing"
	"time"
)

type testFlushWriter struct {
	bytes.Buffer
	header  http.Header
	flushes int
}

func (w *testFlushWriter) Header() http.Header {
	if w.header == nil {
		w.header = http.Header{}
	}
	return w.header
}

func (w *testFlushWriter) WriteHeader(int) {}

func (w *testFlushWriter) Flush() {
	w.flushes++
}

func TestSSEEventWriterSerializesQueuedEvents(t *testing.T) {
	w := &testFlushWriter{}
	writer := newSSEEventWriter(w)

	writer.EnqueueData(`{"token":"a"}`)
	writer.EnqueueComment("heartbeat")
	writer.EnqueueData(`{"done":true}`)
	writer.Close()

	writer.WriteUntilClosed(time.Second)

	got := w.String()
	want := "data: {\"token\":\"a\"}\n\n: heartbeat\n\ndata: {\"done\":true}\n\n"
	if got != want {
		t.Fatalf("unexpected SSE output\nwant: %q\n got: %q", want, got)
	}
	if w.flushes != 3 {
		t.Fatalf("expected one flush per event, got %d", w.flushes)
	}
}
