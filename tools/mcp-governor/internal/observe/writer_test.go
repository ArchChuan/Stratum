package observe

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestWriterTakesExclusiveLockForAppend(t *testing.T) {
	root := filepath.Join(t.TempDir(), "events")
	w, err := NewWriter(root, "codex", "session")
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	path := filepath.Join(root, "codex", "session.jsonl")
	reader, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if err := syscall.Flock(int(reader.Fd()), syscall.LOCK_SH); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- w.Write(validWriterEvent("codex", "session")) }()
	select {
	case err := <-done:
		t.Fatalf("Write completed without waiting for shared lock: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	if err := syscall.Flock(int(reader.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Write did not finish after lock release")
	}
}

func TestWriterCreatesPrivateJSONLFile(t *testing.T) {
	root := filepath.Join(t.TempDir(), "events")
	w, err := NewWriter(root, "codex", "session-hash")
	if err != nil {
		t.Fatal(err)
	}
	event := validWriterEvent("codex", "session-hash")
	if err := w.Write(event); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	assertMode(t, root, 0o700)
	assertMode(t, filepath.Join(root, "codex"), 0o700)
	path := filepath.Join(root, "codex", "session-hash.jsonl")
	assertMode(t, path, 0o600)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Count(data, []byte{'\n'}) != 1 || len(data) == 0 || data[len(data)-1] != '\n' {
		t.Fatalf("event file = %q, want exactly one newline-terminated record", data)
	}
	var got Event
	if err := json.Unmarshal(bytes.TrimSuffix(data, []byte{'\n'}), &got); err != nil {
		t.Fatalf("unmarshal record: %v", err)
	}
	if got != event {
		t.Fatalf("record = %+v, want %+v", got, event)
	}
}

func TestWriterRejectsUnsafePathComponents(t *testing.T) {
	root := filepath.Join(t.TempDir(), "events")
	for _, tt := range []struct {
		name, client, session string
	}{
		{name: "empty client", client: "", session: "session"},
		{name: "dot client", client: ".", session: "session"},
		{name: "parent client", client: "..", session: "session"},
		{name: "client slash", client: "a/b", session: "session"},
		{name: "empty session", client: "codex", session: ""},
		{name: "dot session", client: "codex", session: "."},
		{name: "parent session", client: "codex", session: ".."},
		{name: "session slash", client: "codex", session: "a/b"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if w, err := NewWriter(root, tt.client, tt.session); err == nil {
				_ = w.Close()
				t.Fatal("NewWriter() succeeded for unsafe path component")
			}
		})
	}
}

func TestWriterRejectsSymlinkTargets(t *testing.T) {
	for _, target := range []string{"root", "client", "file"} {
		t.Run(target, func(t *testing.T) {
			base := t.TempDir()
			root := filepath.Join(base, "events")
			real := filepath.Join(base, "real")
			if err := os.Mkdir(real, 0o700); err != nil {
				t.Fatal(err)
			}
			switch target {
			case "root":
				if err := os.Symlink(real, root); err != nil {
					t.Fatal(err)
				}
			case "client":
				if err := os.Mkdir(root, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(real, filepath.Join(root, "codex")); err != nil {
					t.Fatal(err)
				}
			case "file":
				if err := os.MkdirAll(filepath.Join(root, "codex"), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(real, "target"), filepath.Join(root, "codex", "session.jsonl")); err != nil {
					t.Fatal(err)
				}
			}
			if w, err := NewWriter(root, "codex", "session"); err == nil {
				_ = w.Close()
				t.Fatalf("NewWriter() followed %s symlink", target)
			}
		})
	}
}

func TestWriterRejectsWrongExistingModes(t *testing.T) {
	for _, target := range []string{"root", "client", "file"} {
		t.Run(target, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "events")
			if err := os.Mkdir(root, 0o700); err != nil {
				t.Fatal(err)
			}
			client := filepath.Join(root, "codex")
			if target != "root" {
				if err := os.Mkdir(client, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			switch target {
			case "root":
				if err := os.Chmod(root, 0o755); err != nil {
					t.Fatal(err)
				}
			case "client":
				if err := os.Chmod(client, 0o750); err != nil {
					t.Fatal(err)
				}
			case "file":
				path := filepath.Join(client, "session.jsonl")
				if err := os.WriteFile(path, nil, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if w, err := NewWriter(root, "codex", "session"); err == nil {
				_ = w.Close()
				t.Fatalf("NewWriter() accepted wrong %s mode", target)
			}
		})
	}
}

func TestWriterRejectsInvalidEventsAndWritesAfterClose(t *testing.T) {
	root := filepath.Join(t.TempDir(), "events")
	w, err := NewWriter(root, "codex", "session")
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Write(Event{}); err == nil {
		t.Fatal("Write() accepted invalid event")
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := w.Write(validWriterEvent("codex", "session")); err == nil {
		t.Fatal("Write() after Close() succeeded")
	}
	data, err := os.ReadFile(filepath.Join(root, "codex", "session.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 0 {
		t.Fatalf("invalid event was persisted: %q", data)
	}
}

func TestWriterRejectsEventForDifferentClientOrSession(t *testing.T) {
	for _, tt := range []struct {
		name    string
		client  string
		session string
	}{
		{name: "different client", client: "claude", session: "session"},
		{name: "different session", client: "codex", session: "other-session"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "events")
			w, err := NewWriter(root, "codex", "session")
			if err != nil {
				t.Fatal(err)
			}
			if err := w.Write(validWriterEvent(tt.client, tt.session)); err == nil {
				t.Fatal("Write() accepted event for a different session file")
			}
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filepath.Join(root, "codex", "session.jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			if len(data) != 0 {
				t.Fatalf("mismatched event was persisted: %q", data)
			}
		})
	}
}

func TestWriterRejectsExistingSpecialModeBits(t *testing.T) {
	for _, target := range []string{"root", "client", "file"} {
		t.Run(target, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "events")
			if err := os.Mkdir(root, 0o700); err != nil {
				t.Fatal(err)
			}
			client := filepath.Join(root, "codex")
			if target != "root" {
				if err := os.Mkdir(client, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			var path string
			var mode uint32
			switch target {
			case "root":
				path, mode = root, syscall.S_ISVTX|0o700
			case "client":
				path, mode = client, syscall.S_ISGID|0o700
			case "file":
				path, mode = filepath.Join(client, "session.jsonl"), syscall.S_ISUID|0o600
				if err := os.WriteFile(path, nil, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := syscall.Chmod(path, mode); err != nil {
				t.Fatal(err)
			}
			if w, err := NewWriter(root, "codex", "session"); err == nil {
				_ = w.Close()
				t.Fatalf("NewWriter() accepted special mode bits on %s", target)
			}
		})
	}
}

func TestWriterConcurrentDifferentSessions(t *testing.T) {
	root := filepath.Join(t.TempDir(), "events")
	const writers = 8
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			session := "session-" + string(rune('a'+i))
			w, err := NewWriter(root, "codex", session)
			if err == nil {
				err = w.Write(validWriterEvent("codex", session))
			}
			if err == nil {
				err = w.Close()
			}
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < writers; i++ {
		session := "session-" + string(rune('a'+i))
		data, err := os.ReadFile(filepath.Join(root, "codex", session+".jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Count(data, []byte{'\n'}) != 1 {
			t.Fatalf("%s records = %q", session, data)
		}
	}
}

func validWriterEvent(client, session string) Event {
	return Event{
		Version: EventVersion, Kind: KindSessionReady, At: time.Unix(100, 0).UTC(), Client: client,
		Service: "github", SessionHash: session, DurationMS: 12, ResponseBytes: 34,
	}
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %#o, want %#o", path, got, want)
	}
}
