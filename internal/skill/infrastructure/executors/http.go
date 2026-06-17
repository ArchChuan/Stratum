// Package executors provides infrastructure-layer skill executors (HTTP, LLM).
package executors

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"text/template"
	"time"

	"github.com/byteBuilderX/stratum/internal/skill/domain"
	"github.com/byteBuilderX/stratum/pkg/httpclient"
)

type HTTPSkill struct {
	*domain.BaseSkill
	URL          string
	Method       string
	Headers      map[string]string
	BodyTemplate string
	TimeoutSec   int
}

func ValidateSkillURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("URL scheme must be http or https, got: %s", u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("URL must have a host")
	}
	return nil
}

func NewHTTPSkill(id, name, description, rawURL, method string, headers map[string]string, bodyTemplate string, timeoutSec int) (*HTTPSkill, error) {
	if err := ValidateSkillURL(rawURL); err != nil {
		return nil, fmt.Errorf("invalid HTTP skill URL: %w", err)
	}
	if method == "" {
		method = "POST"
	}
	if timeoutSec <= 0 {
		timeoutSec = int(domain.DefaultSkillTimeout.Seconds())
	}
	return &HTTPSkill{
		BaseSkill: &domain.BaseSkill{
			ID:          id,
			Name:        name,
			Description: description,
			Type:        "http",
		},
		URL:          rawURL,
		Method:       method,
		Headers:      headers,
		BodyTemplate: bodyTemplate,
		TimeoutSec:   timeoutSec,
	}, nil
}

func (hs *HTTPSkill) GetConfig() map[string]any {
	return map[string]any{
		"url":           hs.URL,
		"method":        hs.Method,
		"headers":       hs.Headers,
		"body_template": hs.BodyTemplate,
		"timeout_sec":   hs.TimeoutSec,
	}
}

func (hs *HTTPSkill) Execute(ctx context.Context, input interface{}) (interface{}, error) {
	inputMap, _ := input.(map[string]interface{})

	// render body template
	var bodyReader io.Reader
	if hs.BodyTemplate != "" {
		tmpl, err := template.New("body").Parse(hs.BodyTemplate)
		if err != nil {
			return nil, fmt.Errorf("invalid body template: %w", err)
		}
		var buf bytes.Buffer
		if err := tmpl.Execute(&buf, inputMap); err != nil {
			return nil, fmt.Errorf("body template render failed: %w", err)
		}
		bodyReader = &buf
	} else if inputMap != nil {
		b, err := json.Marshal(inputMap)
		if err != nil {
			return nil, fmt.Errorf("marshal input: %w", err)
		}
		bodyReader = bytes.NewReader(b)
	}

	client := httpclient.NewSSRFSafe(
		httpclient.WithTimeout(time.Duration(hs.TimeoutSec)*time.Second),
		httpclient.WithUserAgent("stratum-skill/1.0"),
		httpclient.WithCheckRedirect(func(req *http.Request, via []*http.Request) error {
			return fmt.Errorf("redirects are not allowed for HTTP skills")
		}),
	)
	req, err := http.NewRequestWithContext(ctx, strings.ToUpper(hs.Method), hs.URL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	if bodyReader != nil && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range hs.Headers {
		req.Header.Set(k, v)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request failed: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	rawBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	result := map[string]interface{}{
		"status_code": resp.StatusCode,
	}

	var parsed interface{}
	if json.Unmarshal(rawBody, &parsed) == nil {
		result["body"] = parsed
	} else {
		result["body"] = string(rawBody)
	}

	if resp.StatusCode >= 400 {
		return result, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(rawBody))
	}
	return result, nil
}
