package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/api/middleware"
	"github.com/byteBuilderX/stratum/config"
	"go.uber.org/zap"
)

func TestLoadInternalServerTLSRequiresVerifiedPlatformMCPClient(t *testing.T) {
	files := writeInternalServerTestCertificates(t)

	tlsConfig, err := loadInternalServerTLS(files)
	if err != nil {
		t.Fatal(err)
	}
	if tlsConfig.ClientAuth.String() != "RequireAndVerifyClientCert" {
		t.Fatalf("client auth=%s, want RequireAndVerifyClientCert", tlsConfig.ClientAuth)
	}
	if tlsConfig.ClientCAs == nil || len(tlsConfig.Certificates) != 1 {
		t.Fatal("server certificate or internal client CA was not loaded")
	}
	if tlsConfig.MinVersion != tls.VersionTLS12 {
		t.Fatalf("minimum TLS version=%x, want TLS 1.2", tlsConfig.MinVersion)
	}
}

func TestLoadInternalServerTLSRejectsInvalidClientCA(t *testing.T) {
	files := writeInternalServerTestCertificates(t)
	if err := os.WriteFile(files.ClientCAFile, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := loadInternalServerTLS(files)

	if err == nil {
		t.Fatal("expected invalid client CA to fail")
	}
}

func TestVerifyPlatformMCPConnectionRequiresExactSPIFFEURI(t *testing.T) {
	tests := []struct {
		name    string
		uri     string
		wantErr bool
	}{
		{name: "platform MCP identity", uri: middleware.PlatformMCPWorkloadURI},
		{name: "other workload", uri: "spiffe://stratum.local/ns/stratum/sa/other", wantErr: true},
		{name: "missing URI", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cert := clientCertificateWithURI(t, tc.uri)
			err := verifyPlatformMCPConnection(verifiedConnectionState(cert))
			if (err != nil) != tc.wantErr {
				t.Fatalf("error=%v, wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

func TestInternalHTTPServerStopWaitsForServe(t *testing.T) {
	files := writeInternalServerTestCertificates(t)
	files.Port = "0"
	server := newInternalHTTPServer(
		files,
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
		zap.NewNop(),
	)

	if err := server.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := server.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-server.done:
	default:
		t.Fatal("Serve goroutine was not joined")
	}
}

func writeInternalServerTestCertificates(t *testing.T) config.InternalAPIConfig {
	t.Helper()
	dir := t.TempDir()
	caCert, caKey := createCertificateAuthority(t)
	serverCert, serverKey := createServerCertificate(t, caCert, caKey)
	files := config.InternalAPIConfig{
		Port:         "8443",
		CertFile:     filepath.Join(dir, "tls.crt"),
		KeyFile:      filepath.Join(dir, "tls.key"),
		ClientCAFile: filepath.Join(dir, "ca.crt"),
	}
	writePEMFile(t, files.CertFile, "CERTIFICATE", serverCert.Raw)
	keyDER, err := x509.MarshalPKCS8PrivateKey(serverKey)
	if err != nil {
		t.Fatal(err)
	}
	writePEMFile(t, files.KeyFile, "PRIVATE KEY", keyDER)
	writePEMFile(t, files.ClientCAFile, "CERTIFICATE", caCert.Raw)
	return files
}

func createCertificateAuthority(t *testing.T) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key := generateECDSAKey(t)
	template := certificateTemplate(t, "test internal CA")
	template.IsCA = true
	template.BasicConstraintsValid = true
	template.KeyUsage = x509.KeyUsageCertSign
	der := createCertificate(t, template, template, &key.PublicKey, key)
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert, key
}

func createServerCertificate(
	t *testing.T,
	ca *x509.Certificate,
	caKey *ecdsa.PrivateKey,
) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key := generateECDSAKey(t)
	template := certificateTemplate(t, "stratum-internal")
	template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
	der := createCertificate(t, template, ca, &key.PublicKey, caKey)
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert, key
}

func certificateTemplate(t *testing.T, commonName string) *x509.Certificate {
	t.Helper()
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatal(err)
	}
	return &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
	}
}

func generateECDSAKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func createCertificate(
	t *testing.T,
	template, parent *x509.Certificate,
	publicKey any,
	privateKey any,
) []byte {
	t.Helper()
	der, err := x509.CreateCertificate(rand.Reader, template, parent, publicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return der
}

func writePEMFile(t *testing.T, path, blockType string, der []byte) {
	t.Helper()
	data := pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: der})
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func clientCertificateWithURI(t *testing.T, rawURI string) *x509.Certificate {
	t.Helper()
	cert := &x509.Certificate{}
	if rawURI == "" {
		return cert
	}
	uri, err := url.Parse(rawURI)
	if err != nil {
		t.Fatal(err)
	}
	cert.URIs = []*url.URL{uri}
	return cert
}

func verifiedConnectionState(cert *x509.Certificate) tls.ConnectionState {
	return tls.ConnectionState{
		PeerCertificates: []*x509.Certificate{cert},
		VerifiedChains:   [][]*x509.Certificate{{cert}},
	}
}
