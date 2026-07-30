package infrastructure

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestTLSReloaderPublishesCompleteSnapshotAndPreservesOldOnFailure(t *testing.T) {
	files := writeReloadTestFiles(t)
	reloader := NewTLSReloader(files)

	if err := reloader.Reload(); err != nil {
		t.Fatal(err)
	}
	first := reloader.Current()
	if first == nil || !reloader.Ready() {
		t.Fatal("TLS snapshot was not ready after successful reload")
	}
	if err := os.WriteFile(files.CertFile, []byte("invalid"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := reloader.Reload(); err == nil {
		t.Fatal("expected invalid replacement certificate to fail")
	}
	if reloader.Current() != first {
		t.Fatal("failed reload replaced the last valid TLS snapshot")
	}
}

func TestTLSReloaderVerifiesAllowedClientWorkloadURI(t *testing.T) {
	files := writeReloadTestFiles(t)
	reloader := NewTLSReloader(files)
	if err := reloader.Reload(); err != nil {
		t.Fatal(err)
	}
	verify := reloader.Current().VerifyConnection
	tests := []struct {
		name    string
		uri     string
		wantErr bool
	}{
		{name: "backend", uri: BackendWorkloadURI},
		{name: "metrics scraper", uri: MetricsWorkloadURI},
		{name: "other workload", uri: "spiffe://stratum.local/ns/stratum/sa/other", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			uri, err := url.Parse(tc.uri)
			if err != nil {
				t.Fatal(err)
			}
			cert := &x509.Certificate{URIs: []*url.URL{uri}}
			state := tls.ConnectionState{
				PeerCertificates: []*x509.Certificate{cert},
				VerifiedChains:   [][]*x509.Certificate{{cert}},
			}
			if gotErr := verify(state); (gotErr != nil) != tc.wantErr {
				t.Fatalf("error=%v, wantErr=%v", gotErr, tc.wantErr)
			}
		})
	}
}

func TestTLSReloaderBuildsBackendClientConfigFromCurrentSnapshot(t *testing.T) {
	files := writeReloadTestFiles(t)
	reloader := NewTLSReloader(files)
	if err := reloader.Reload(); err != nil {
		t.Fatal(err)
	}

	clientConfig, err := reloader.BackendClientConfig("stratum-internal")

	if err != nil {
		t.Fatal(err)
	}
	if clientConfig.ServerName != "stratum-internal" || clientConfig.RootCAs == nil ||
		len(clientConfig.Certificates) != 1 || clientConfig.VerifyConnection == nil {
		t.Fatalf("client TLS config=%+v", clientConfig)
	}
}

func TestTLSReloaderReportsCurrentCertificateExpiry(t *testing.T) {
	reloader := NewTLSReloader(writeReloadTestFiles(t))
	if err := reloader.Reload(); err != nil {
		t.Fatal(err)
	}

	seconds, err := reloader.CertificateExpirySeconds()

	if err != nil || seconds <= 0 {
		t.Fatalf("seconds=%f err=%v", seconds, err)
	}
}

func writeReloadTestFiles(t *testing.T) TLSFiles {
	t.Helper()
	testServer := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(testServer.Close)
	certificate := testServer.TLS.Certificates[0]
	keyDER, err := x509.MarshalPKCS8PrivateKey(certificate.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	files := TLSFiles{
		CertFile:     filepath.Join(dir, "tls.crt"),
		KeyFile:      filepath.Join(dir, "tls.key"),
		ClientCAFile: filepath.Join(dir, "ca.crt"),
	}
	writeReloadPEM(t, files.CertFile, "CERTIFICATE", certificate.Certificate[0])
	writeReloadPEM(t, files.KeyFile, "PRIVATE KEY", keyDER)
	writeReloadPEM(t, files.ClientCAFile, "CERTIFICATE", certificate.Certificate[0])
	return files
}

func writeReloadPEM(t *testing.T, path, blockType string, der []byte) {
	t.Helper()
	data := pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: der})
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}
