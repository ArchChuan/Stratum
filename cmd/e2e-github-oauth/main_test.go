package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

const (
	testClientID     = "stateful-client"
	testClientSecret = "stateful-secret"
	testCallbackURL  = "http://127.0.0.1:18080/auth/github/callback"
)

func TestProviderCompletesAuthorizeTokenAndProfileProtocol(t *testing.T) {
	provider := newProvider(providerConfig{
		clientID: testClientID, clientSecret: testClientSecret, callbackURL: testCallbackURL,
		githubID: 730001, login: "stateful-oauth-user", email: "stateful-oauth@example.test",
	})
	server := httptest.NewServer(provider.routes())
	defer server.Close()

	authorizeURL := server.URL + "/login/oauth/authorize?" + url.Values{
		"client_id": {testClientID}, "redirect_uri": {testCallbackURL}, "state": {"state-value"},
	}.Encode()
	client := server.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Get(authorizeURL)
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	defer response.Body.Close() //nolint:errcheck
	location, err := url.Parse(response.Header.Get("Location"))
	if err != nil {
		t.Fatalf("parse callback: %v", err)
	}
	if response.StatusCode != http.StatusFound || location.Query().Get("state") != "state-value" {
		t.Fatalf("authorize status=%d location=%s", response.StatusCode, location.String())
	}
	code := location.Query().Get("code")
	if code == "" {
		t.Fatal("authorization code is missing")
	}

	token := exchangeToken(t, client, server.URL, code, testClientSecret)
	assertProfile(t, client, server.URL, token)

	replay := exchangeTokenResponse(t, client, server.URL, code, testClientSecret)
	defer replay.Body.Close() //nolint:errcheck
	if replay.StatusCode != http.StatusBadRequest {
		t.Fatalf("replayed code status=%d", replay.StatusCode)
	}
}

func TestProviderRejectsInvalidBearerAndDoesNotEchoSecret(t *testing.T) {
	provider := newProvider(providerConfig{
		clientID: testClientID, clientSecret: testClientSecret, callbackURL: testCallbackURL,
		githubID: 730001, login: "stateful-oauth-user", email: "stateful-oauth@example.test",
	})
	server := httptest.NewServer(provider.routes())
	defer server.Close()

	req, err := http.NewRequest(http.MethodGet, server.URL+"/user", nil) //nolint:noctx
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+testClientSecret)
	response, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("profile request: %v", err)
	}
	defer response.Body.Close() //nolint:errcheck
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusUnauthorized || strings.Contains(string(body), testClientSecret) {
		t.Fatalf("unsafe rejection status=%d body=%q", response.StatusCode, body)
	}
}

func TestProviderRejectsOversizedTokenForm(t *testing.T) {
	provider := newProvider(providerConfig{
		clientID: testClientID, clientSecret: testClientSecret, callbackURL: testCallbackURL,
	})
	server := httptest.NewServer(provider.routes())
	defer server.Close()

	body := strings.NewReader("client_id=" + strings.Repeat("x", maxOAuthFormBytes))
	request, err := http.NewRequest(http.MethodPost, server.URL+"/login/oauth/access_token", body) //nolint:noctx
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatalf("oversized token request: %v", err)
	}
	defer response.Body.Close() //nolint:errcheck
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("oversized token status=%d", response.StatusCode)
	}
}

func exchangeToken(t *testing.T, client *http.Client, baseURL, code, secret string) string {
	t.Helper()
	response := exchangeTokenResponse(t, client, baseURL, code, secret)
	defer response.Body.Close() //nolint:errcheck
	if response.StatusCode != http.StatusOK {
		t.Fatalf("token status=%d", response.StatusCode)
	}
	var payload struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.AccessToken == "" {
		t.Fatal("access token is missing")
	}
	return payload.AccessToken
}

func exchangeTokenResponse(t *testing.T, client *http.Client, baseURL, code, secret string) *http.Response {
	t.Helper()
	response, err := client.PostForm(baseURL+"/login/oauth/access_token", url.Values{
		"client_id": {testClientID}, "client_secret": {secret}, "redirect_uri": {testCallbackURL}, "code": {code},
	})
	if err != nil {
		t.Fatalf("token exchange: %v", err)
	}
	return response
}

func assertProfile(t *testing.T, client *http.Client, baseURL, token string) {
	t.Helper()
	for _, path := range []string{"/user", "/user/emails"} {
		req, err := http.NewRequest(http.MethodGet, baseURL+path, nil) //nolint:noctx
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		response, err := client.Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("GET %s status=%d", path, response.StatusCode)
		}
	}
}
