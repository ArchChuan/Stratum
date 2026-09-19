package handler

import (
	"bytes"
	"net/http"
	"net/http/httptest"
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

func TestSSEWriterEmitsIDLine(t *testing.T) {
	rec := httptest.NewRecorder()
	w := newSSEEventWriter(rec)
	w.EnqueueStreamFrame("1726483200000-37", "", `{"token":"a"}`)
	w.Close()
	w.WriteUntilClosed(0)
	want := "id: 1726483200000-37\ndata: {\"token\":\"a\"}\n\n"
	if got := rec.Body.String(); got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

func TestSSEWriterEmitsIDAndEventLines(t *testing.T) {
	rec := httptest.NewRecorder()
	w := newSSEEventWriter(rec)
	w.EnqueueStreamFrame("1726483200000-37", "meta", `{"execution_id":"e1"}`)
	w.Close()
	w.WriteUntilClosed(0)
	want := "id: 1726483200000-37\nevent: meta\ndata: {\"execution_id\":\"e1\"}\n\n"
	if got := rec.Body.String(); got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

func TestSSEWriterOmitsIDLineWhenEmpty(t *testing.T) {
	// 既有帧（如 reset 合成帧）不带游标，不能凭空多出一行 id:。
	rec := httptest.NewRecorder()
	w := newSSEEventWriter(rec)
	w.EnqueueData(`{"done":true}`)
	w.Close()
	w.WriteUntilClosed(0)
	want := "data: {\"done\":true}\n\n"
	if got := rec.Body.String(); got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}
