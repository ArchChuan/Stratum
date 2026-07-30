package infrastructure

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBackendProbeRequiresHealthyFixedInternalEndpoint(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		wantErr    bool
	}{
		{name: "healthy", statusCode: http.StatusOK},
		{name: "unavailable", statusCode: http.StatusServiceUnavailable, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.statusCode)
			}))
			t.Cleanup(server.Close)
			probe, err := NewBackendProbe(server.Client(), server.URL+"/internal/livez")
			if err != nil {
				t.Fatal(err)
			}

			err = probe.Check(t.Context())

			if (err != nil) != tc.wantErr {
				t.Fatalf("error=%v, wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

func TestBackendProbeRejectsNonHTTPSOrParameterizedEndpoint(t *testing.T) {
	tests := []string{
		"http://stratum-internal:8443/internal/livez",
		"https://user@stratum-internal:8443/internal/livez",
		"https://stratum-internal:8443/internal/livez?token=forbidden",
		"https://stratum-internal:8443/other",
	}
	for _, endpoint := range tests {
		t.Run(endpoint, func(t *testing.T) {
			if _, err := NewBackendProbe(http.DefaultClient, endpoint); err == nil {
				t.Fatal("expected unsafe backend readiness endpoint to fail")
			}
		})
	}
}
