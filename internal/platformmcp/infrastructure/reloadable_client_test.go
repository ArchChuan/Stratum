package infrastructure

import (
	"net/http"
	"testing"
)

func TestReloadableBackendClientReplacesTransportFromLatestTLSSnapshot(t *testing.T) {
	reloader := NewTLSReloader(writeReloadTestFiles(t))
	if err := reloader.Reload(); err != nil {
		t.Fatal(err)
	}
	client, err := NewReloadableBackendClient(reloader, "stratum-internal")
	if err != nil {
		t.Fatal(err)
	}
	first := client.current.Load()
	if first == nil {
		t.Fatal("initial backend client was not published")
	}

	if err := client.Reload(); err != nil {
		t.Fatal(err)
	}
	second := client.current.Load()
	if second == nil || second == first {
		t.Fatal("backend client transport was not replaced")
	}
	transport, ok := second.Transport.(*http.Transport)
	if !ok || transport.TLSClientConfig == nil || transport.TLSClientConfig.ServerName != "stratum-internal" {
		t.Fatalf("backend transport=%T", second.Transport)
	}
}
