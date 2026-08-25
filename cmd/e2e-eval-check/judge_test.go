package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestJudgeParsesResponse(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody = readAll(t, r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"passed\":true,\"score\":90,\"reason\":\"meets criteria\"}"}}]}`))
	}))
	defer srv.Close()
	j := &judgeClient{baseURL: srv.URL, model: "m", http: srv.Client()}
	res, err := j.Judge(context.Background(), judgeSpec{Criteria: "must be correct"}, "the output")
	if err != nil {
		t.Fatalf("Judge: %v", err)
	}
	if !res.Passed || res.Score != 90 {
		t.Fatalf("Judge = %+v, want passed", res)
	}
	if gotBody == "" {
		t.Fatal("expected request body")
	}
}

func TestJudgeFailClosedOnInvalidLLMResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[]}`))
	}))
	defer srv.Close()
	j := &judgeClient{baseURL: srv.URL, model: "m", http: srv.Client()}
	_, err := j.Judge(context.Background(), judgeSpec{Criteria: "x"}, "y")
	if err == nil {
		t.Fatal("expected error on invalid judge response")
	}
	if !isInfra(err) {
		t.Fatalf("malformed judge response must be infra (exit 2), got %T: %v", err, err)
	}
}

func TestJudgeFailClosedOnNonJSONVerdict(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"not json"}}]}`))
	}))
	defer srv.Close()
	j := &judgeClient{baseURL: srv.URL, model: "m", http: srv.Client()}
	if _, err := j.Judge(context.Background(), judgeSpec{}, "y"); err == nil || !isInfra(err) {
		t.Fatalf("expected infra error on non-JSON verdict, got %v", err)
	}
}

func TestJudgeFailClosedOnOutOfRangeScore(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"passed\":true,\"score\":150,\"reason\":\"x\"}"}}]}`))
	}))
	defer srv.Close()
	j := &judgeClient{baseURL: srv.URL, model: "m", http: srv.Client()}
	if _, err := j.Judge(context.Background(), judgeSpec{}, "y"); err == nil || !isInfra(err) {
		t.Fatalf("expected infra error on out-of-range score, got %v", err)
	}
}

func TestJudgeFailClosedOnHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	j := &judgeClient{baseURL: srv.URL, model: "m", http: srv.Client()}
	if _, err := j.Judge(context.Background(), judgeSpec{}, "y"); err == nil || !isInfra(err) {
		t.Fatalf("expected infra error on HTTP 500, got %v", err)
	}
}

// TestJudgeBearerInHeaderOnly pins the bearer-hygiene contract: the judge API
// key must travel only in the Authorization header, never in the URL or body.
func TestJudgeBearerInHeaderOnly(t *testing.T) {
	var authHeader, reqPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
		reqPath = r.URL.Path
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"passed\":true,\"score\":10,\"reason\":\"x\"}"}}]}`))
	}))
	defer srv.Close()
	j := &judgeClient{baseURL: srv.URL, model: "m", apiKey: "secret-key", http: srv.Client()}
	if _, err := j.Judge(context.Background(), judgeSpec{}, "y"); err != nil {
		t.Fatalf("Judge: %v", err)
	}
	if authHeader != "Bearer secret-key" {
		t.Fatalf("Authorization = %q, want Bearer secret-key", authHeader)
	}
	if reqPath != "/v1/chat/completions" {
		t.Fatalf("judge path = %q, want /v1/chat/completions", reqPath)
	}
}

// TestJudgeBaseURLNormalizesV1 pins the guidance that both
// https://api.openai.com and https://api.openai.com/v1 work (no double /v1).
func TestJudgeBaseURLNormalizesV1(t *testing.T) {
	if got := newJudgeClient(&judgeConfig{BaseURL: "https://api.openai.com/v1/", Model: "m"}, "").baseURL; got != "https://api.openai.com" {
		t.Fatalf("baseURL = %q, want trailing /v1 stripped", got)
	}
	if got := newJudgeClient(&judgeConfig{BaseURL: "https://api.openai.com", Model: "m"}, "").baseURL; got != "https://api.openai.com" {
		t.Fatalf("baseURL = %q, want unchanged", got)
	}
}

func readAll(t *testing.T, body io.ReadCloser) string {
	t.Helper()
	b, err := io.ReadAll(body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
