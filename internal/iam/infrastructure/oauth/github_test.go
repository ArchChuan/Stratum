package oauth_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	oauth "github.com/byteBuilderX/stratum/internal/iam/infrastructure/oauth"
)

func TestExchangeCode_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "gho_test123", "token_type": "bearer"})
	}))
	defer ts.Close()

	client := oauth.NewGitHubClient("clientid", "clientsecret", ts.URL+"/login/oauth/access_token", ts.URL+"/user")
	token, err := client.ExchangeCode(context.Background(), "code123", "http://localhost/callback")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if token != "gho_test123" {
		t.Errorf("expected gho_test123, got %s", token)
	}
}

func TestGetUser_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/user/emails" {
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{
				{"email": "dev@example.com", "primary": true, "verified": true},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"id":         int64(42),
			"login":      "byteBuilderX",
			"email":      "dev@example.com",
			"avatar_url": "https://avatars.githubusercontent.com/u/42",
		})
	}))
	defer ts.Close()

	client := oauth.NewGitHubClient("clientid", "clientsecret", ts.URL+"/token", ts.URL+"/user")
	user, err := client.GetUser(context.Background(), "gho_test123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if user.Login != "byteBuilderX" {
		t.Errorf("expected byteBuilderX, got %s", user.Login)
	}
	if user.ID != 42 {
		t.Errorf("expected 42, got %d", user.ID)
	}
	if user.Email != "dev@example.com" {
		t.Errorf("expected verified profile email, got %q", user.Email)
	}
}

func TestGetUser_UsesPrimaryVerifiedEmail(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/user/emails" {
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{
				{"email": "unverified@example.com", "primary": true, "verified": false},
				{"email": "verified@example.com", "primary": true, "verified": true},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"id": int64(42), "login": "byteBuilderX", "avatar_url": "https://example.com/avatar",
		})
	}))
	defer ts.Close()

	client := oauth.NewGitHubClient("clientid", "clientsecret", ts.URL+"/token", ts.URL+"/user")
	user, err := client.GetUser(context.Background(), "gho_test123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if user.Email != "verified@example.com" {
		t.Fatalf("expected primary verified email, got %q", user.Email)
	}
}
