package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	defaultListenAddress = "127.0.0.1:19090"
	serverHeaderTimeout  = 5 * time.Second
	serverReadTimeout    = 10 * time.Second
	serverWriteTimeout   = 10 * time.Second
	serverIdleTimeout    = 30 * time.Second
	serverShutdownBudget = 5 * time.Second
	maxOAuthFormBytes    = 16 << 10
)

type providerConfig struct {
	clientID     string
	clientSecret string
	callbackURL  string
	githubID     int64
	login        string
	email        string
}

type provider struct {
	config providerConfig
	mu     sync.Mutex
	codes  map[string]struct{}
	tokens map[string]struct{}
}

func newProvider(config providerConfig) *provider {
	return &provider{config: config, codes: make(map[string]struct{}), tokens: make(map[string]struct{})}
}

func (p *provider) routes() http.Handler {
	router := http.NewServeMux()
	router.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	router.HandleFunc("GET /login/oauth/authorize", p.authorize)
	router.HandleFunc("POST /login/oauth/access_token", p.exchange)
	router.HandleFunc("GET /user", p.user)
	router.HandleFunc("GET /user/emails", p.emails)
	return router
}

func (p *provider) authorize(w http.ResponseWriter, request *http.Request) {
	query := request.URL.Query()
	if query.Get("client_id") != p.config.clientID || query.Get("redirect_uri") != p.config.callbackURL ||
		query.Get("state") == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	code, err := randomOpaqueValue()
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error")
		return
	}
	p.mu.Lock()
	p.codes[code] = struct{}{}
	p.mu.Unlock()
	callback, err := url.Parse(p.config.callbackURL)
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error")
		return
	}
	callbackQuery := callback.Query()
	callbackQuery.Set("code", code)
	callbackQuery.Set("state", query.Get("state"))
	callback.RawQuery = callbackQuery.Encode()
	http.Redirect(w, request, callback.String(), http.StatusFound)
}

func (p *provider) exchange(w http.ResponseWriter, request *http.Request) {
	request.Body = http.MaxBytesReader(w, request.Body, maxOAuthFormBytes)
	if err := request.ParseForm(); err != nil || request.Form.Get("client_id") != p.config.clientID ||
		request.Form.Get("client_secret") != p.config.clientSecret ||
		request.Form.Get("redirect_uri") != p.config.callbackURL {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	code := request.Form.Get("code")
	p.mu.Lock()
	_, valid := p.codes[code]
	if valid {
		delete(p.codes, code)
	}
	p.mu.Unlock()
	if !valid {
		writeOAuthError(w, http.StatusBadRequest, "bad_verification_code")
		return
	}
	token, err := randomOpaqueValue()
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error")
		return
	}
	p.mu.Lock()
	p.tokens[token] = struct{}{}
	p.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]string{"access_token": token, "token_type": "bearer"})
}

func (p *provider) user(w http.ResponseWriter, request *http.Request) {
	if !p.authorized(request) {
		writeOAuthError(w, http.StatusUnauthorized, "bad_credentials")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": p.config.githubID, "login": p.config.login, "avatar_url": "", "email": nil,
	})
}

func (p *provider) emails(w http.ResponseWriter, request *http.Request) {
	if !p.authorized(request) {
		writeOAuthError(w, http.StatusUnauthorized, "bad_credentials")
		return
	}
	writeJSON(w, http.StatusOK, []map[string]any{{
		"email": p.config.email, "primary": true, "verified": true, "visibility": "private",
	}})
}

func (p *provider) authorized(request *http.Request) bool {
	token := strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer ")
	if token == "" {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	_, ok := p.tokens[token]
	return ok
}

func randomOpaqueValue() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func writeOAuthError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]string{"error": code})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func main() {
	if err := serve(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "e2e github oauth failed")
		os.Exit(1)
	}
}

func serve() error {
	config, address, err := loadProviderConfig(os.Getenv)
	if err != nil {
		return err
	}
	server := &http.Server{
		Addr: address, Handler: newProvider(config).routes(),
		ReadHeaderTimeout: serverHeaderTimeout, ReadTimeout: serverReadTimeout,
		WriteTimeout: serverWriteTimeout, IdleTimeout: serverIdleTimeout,
	}
	var listenConfig net.ListenConfig
	listener, err := listenConfig.Listen(context.Background(), "tcp", address)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	serveErrors := make(chan error, 1)
	go func() { serveErrors <- server.Serve(listener) }()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), serverShutdownBudget)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	case err := <-serveErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve: %w", err)
	}
}

func loadProviderConfig(getenv func(string) string) (providerConfig, string, error) {
	address := getenv("E2E_GITHUB_LISTEN_ADDRESS")
	if address == "" {
		address = defaultListenAddress
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil || (host != "127.0.0.1" && host != "localhost") {
		return providerConfig{}, "", fmt.Errorf("listen address must be loopback host:port")
	}
	githubID, err := strconv.ParseInt(getenv("E2E_GITHUB_ID"), 10, 64)
	if err != nil || githubID <= 0 {
		return providerConfig{}, "", fmt.Errorf("E2E_GITHUB_ID must be a positive integer")
	}
	config := providerConfig{
		clientID: getenv("GITHUB_CLIENT_ID"), clientSecret: getenv("GITHUB_CLIENT_SECRET"),
		callbackURL: getenv("GITHUB_CALLBACK_URL"), githubID: githubID,
		login: getenv("E2E_GITHUB_LOGIN"), email: getenv("E2E_GITHUB_EMAIL"),
	}
	if config.clientID == "" || config.clientSecret == "" || config.callbackURL == "" ||
		config.login == "" || config.email == "" {
		return providerConfig{}, "", fmt.Errorf("provider configuration is incomplete")
	}
	return config, address, nil
}
