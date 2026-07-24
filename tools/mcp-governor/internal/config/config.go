package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/byteBuilderX/stratum/tools/mcp-governor/internal/jsonstrict"
)

const (
	minRawRetentionDays = 7
	maxRawRetentionDays = 90
)

type Transport string

const (
	TransportStdio          Transport = "stdio"
	TransportStreamableHTTP Transport = "streamable_http"
	TransportSSE            Transport = "sse"
)

type Scope string

const (
	ScopeUser       Scope = "user"
	ScopeRepository Scope = "repository"
	ScopeSession    Scope = "session"
)

type SessionPolicy string

const (
	SessionPolicyIsolated     SessionPolicy = "isolated"
	SessionPolicySessionLocal SessionPolicy = "session_local"
)

type Client string

const (
	ClientCodex  Client = "codex"
	ClientClaude Client = "claude"
	ClientVSCode Client = "vscode"
	ClientLingma Client = "lingma"
)

type Observation struct {
	EventsDir                            string `json:"events_dir"`
	ReportsDir                           string `json:"reports_dir"`
	SaltPath                             string `json:"salt_path"`
	RawRetentionDays                     int    `json:"raw_retention_days"`
	MaxEventSegmentBytes                 int64  `json:"max_event_segment_bytes,omitempty"`
	ReportMaxEventBytes                  int64  `json:"report_max_event_bytes,omitempty"`
	ReportMaxRecords                     int    `json:"report_max_records,omitempty"`
	ReportMaxToolCardinality             int    `json:"report_max_tool_cardinality,omitempty"`
	ReportMaxSessionCardinality          int    `json:"report_max_session_cardinality,omitempty"`
	ReportMaxDayCardinality              int    `json:"report_max_day_cardinality,omitempty"`
	ReportMaxServiceCardinality          int    `json:"report_max_service_cardinality,omitempty"`
	ReportMaxSnapshotIdentityCardinality int    `json:"report_max_snapshot_identity_cardinality,omitempty"`
	ReportMaxDistributionValues          int    `json:"report_max_distribution_values,omitempty"`
	ReportMaxWorkUnits                   int64  `json:"report_max_work_units,omitempty"`
}

type Config struct {
	Version      int           `json:"version"`
	OutputPath   string        `json:"output_path"`
	RegistryPath string        `json:"registry_path"`
	Observation  Observation   `json:"observation"`
	Services     []ServiceRule `json:"services"`
}

type ServiceRule struct {
	Name           string        `json:"name"`
	Command        string        `json:"command"`
	Args           []string      `json:"args"`
	Cwd            string        `json:"cwd"`
	Transport      Transport     `json:"transport"`
	Scope          Scope         `json:"scope"`
	SessionPolicy  SessionPolicy `json:"session_policy"`
	Clients        []Client      `json:"clients"`
	AllArgsContain []string      `json:"all_args_contain"`
}

type configVersion struct {
	Version int `json:"version"`
}

type configVersion1 struct {
	Version      int                   `json:"version"`
	OutputPath   string                `json:"output_path"`
	RegistryPath string                `json:"registry_path"`
	Services     []serviceRuleVersion1 `json:"services"`
}

type serviceRuleVersion1 struct {
	Name           string   `json:"name"`
	AllArgsContain []string `json:"all_args_contain"`
}

func Decode(r io.Reader) (Config, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	if err := jsonstrict.ValidateNoDuplicateKeys(data); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}

	var version configVersion
	if err := json.NewDecoder(bytes.NewReader(data)).Decode(&version); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}

	var cfg Config
	switch version.Version {
	case 1:
		var wire configVersion1
		if err := decodeStrict(data, &wire); err != nil {
			return Config{}, err
		}
		cfg = convertVersion1(wire)
	case 2:
		if err := decodeStrict(data, &cfg); err != nil {
			return Config{}, err
		}
	default:
		return Config{}, fmt.Errorf("config version must be 1 or 2, got %d", version.Version)
	}
	if err := validate(&cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func decodeStrict(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode config: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("decode config: trailing JSON value")
		}
		return fmt.Errorf("decode config trailing data: %w", err)
	}
	return nil
}

func convertVersion1(wire configVersion1) Config {
	services := make([]ServiceRule, len(wire.Services))
	for i, service := range wire.Services {
		services[i] = ServiceRule{Name: service.Name, AllArgsContain: service.AllArgsContain}
	}
	return Config{
		Version:      wire.Version,
		OutputPath:   wire.OutputPath,
		RegistryPath: wire.RegistryPath,
		Services:     services,
	}
}

func validate(cfg *Config) error {
	if cfg.Version != 1 && cfg.Version != 2 {
		return fmt.Errorf("config version must be 1 or 2, got %d", cfg.Version)
	}
	if strings.TrimSpace(cfg.OutputPath) == "" {
		return fmt.Errorf("config output_path must not be empty")
	}
	if strings.TrimSpace(cfg.RegistryPath) == "" {
		return fmt.Errorf("config registry_path must not be empty")
	}
	if len(cfg.Services) == 0 {
		return fmt.Errorf("config services must contain at least one service")
	}
	if cfg.Version == 2 {
		if err := validateObservation(cfg.Observation); err != nil {
			return err
		}
	}

	names := make(map[string]struct{}, len(cfg.Services))
	for i := range cfg.Services {
		service := &cfg.Services[i]
		context := fmt.Sprintf("config services[%d]", i)
		if strings.TrimSpace(service.Name) == "" {
			return fmt.Errorf("%s name must not be empty", context)
		}
		if _, exists := names[service.Name]; exists {
			return fmt.Errorf("%s name %q is duplicate", context, service.Name)
		}
		names[service.Name] = struct{}{}
		if cfg.Version == 1 {
			applyVersion1Defaults(service)
		} else if err := validateVersion2Service(*service, context); err != nil {
			return err
		}
		if len(service.AllArgsContain) == 0 {
			return fmt.Errorf("%s all_args_contain must contain at least one fragment", context)
		}
		fragments := make(map[string]struct{}, len(service.AllArgsContain))
		for j, fragment := range service.AllArgsContain {
			if fragment == "" {
				return fmt.Errorf("%s all_args_contain[%d] fragment must not be empty", context, j)
			}
			if _, exists := fragments[fragment]; exists {
				return fmt.Errorf("%s all_args_contain[%d] fragment %q is duplicate", context, j, fragment)
			}
			fragments[fragment] = struct{}{}
		}
	}
	return nil
}

func applyVersion1Defaults(service *ServiceRule) {
	service.Transport = TransportStdio
	service.Scope = ScopeUser
	service.SessionPolicy = SessionPolicyIsolated
	service.Clients = []Client{ClientCodex, ClientClaude, ClientVSCode, ClientLingma}
}

func validateObservation(observation Observation) error {
	paths := []struct {
		name  string
		value string
	}{
		{"events_dir", observation.EventsDir},
		{"reports_dir", observation.ReportsDir},
		{"salt_path", observation.SaltPath},
	}
	for _, path := range paths {
		if strings.TrimSpace(path.value) == "" {
			return fmt.Errorf("config observation %s must not be empty", path.name)
		}
	}
	if observation.RawRetentionDays < minRawRetentionDays || observation.RawRetentionDays > maxRawRetentionDays {
		return fmt.Errorf("config observation raw_retention_days must be between %d and %d", minRawRetentionDays,
			maxRawRetentionDays)
	}
	if observation.MaxEventSegmentBytes < 0 || observation.MaxEventSegmentBytes > 1<<30 {
		return fmt.Errorf("config observation max_event_segment_bytes must be between 0 and %d", 1<<30)
	}
	if observation.ReportMaxEventBytes < 0 || observation.ReportMaxEventBytes > 1<<32 {
		return fmt.Errorf("config observation report_max_event_bytes must be between 0 and %d", 1<<32)
	}
	if observation.ReportMaxRecords < 0 || observation.ReportMaxRecords > 100_000_000 {
		return fmt.Errorf("config observation report_max_records must be between 0 and %d", 100_000_000)
	}
	if observation.ReportMaxToolCardinality < 0 || observation.ReportMaxToolCardinality > 10_000_000 {
		return fmt.Errorf("config observation report_max_tool_cardinality must be between 0 and %d", 10_000_000)
	}
	if observation.ReportMaxSessionCardinality < 0 || observation.ReportMaxSessionCardinality > 10_000_000 {
		return fmt.Errorf("config observation report_max_session_cardinality must be between 0 and %d", 10_000_000)
	}
	if observation.ReportMaxDayCardinality < 0 || observation.ReportMaxDayCardinality > 10_000_000 {
		return fmt.Errorf("config observation report_max_day_cardinality must be between 0 and %d", 10_000_000)
	}
	if observation.ReportMaxServiceCardinality < 0 || observation.ReportMaxServiceCardinality > 10_000_000 {
		return fmt.Errorf("config observation report_max_service_cardinality must be between 0 and %d", 10_000_000)
	}
	if observation.ReportMaxSnapshotIdentityCardinality < 0 || observation.ReportMaxSnapshotIdentityCardinality > 10_000_000 {
		return fmt.Errorf("config observation report_max_snapshot_identity_cardinality must be between 0 and %d", 10_000_000)
	}
	if observation.ReportMaxDistributionValues < 0 || observation.ReportMaxDistributionValues > 10_000_000 {
		return fmt.Errorf("config observation report_max_distribution_values must be between 0 and %d", 10_000_000)
	}
	if observation.ReportMaxWorkUnits < 0 || observation.ReportMaxWorkUnits > 1_000_000_000 {
		return fmt.Errorf("config observation report_max_work_units must be between 0 and %d", 1_000_000_000)
	}
	return nil
}

func validateVersion2Service(service ServiceRule, context string) error {
	if strings.TrimSpace(service.Command) == "" {
		return fmt.Errorf("%s command must not be empty", context)
	}
	for i, arg := range service.Args {
		if containsInlineCredential(arg) {
			return fmt.Errorf("%s args[%d] must not contain an inline credential assignment", context, i)
		}
		if isCredentialFlag(arg) && i+1 < len(service.Args) &&
			!strings.HasPrefix(strings.TrimSpace(service.Args[i+1]), "-") {
			return fmt.Errorf("%s args[%d] must not contain a split credential value", context, i)
		}
	}
	if !validTransport(service.Transport) {
		return fmt.Errorf("%s transport %q is invalid", context, service.Transport)
	}
	if !validScope(service.Scope) {
		return fmt.Errorf("%s scope %q is invalid", context, service.Scope)
	}
	if !validSessionPolicy(service.SessionPolicy) {
		return fmt.Errorf("%s session_policy %q is invalid", context, service.SessionPolicy)
	}
	if service.Scope == ScopeRepository && service.SessionPolicy != SessionPolicyIsolated {
		return fmt.Errorf("%s session_policy must be isolated for repository scope", context)
	}
	if service.Scope == ScopeSession && service.SessionPolicy != SessionPolicySessionLocal {
		return fmt.Errorf("%s session_policy must be session_local for session scope", context)
	}
	if len(service.Clients) == 0 {
		return fmt.Errorf("%s clients must contain at least one client", context)
	}
	clients := make(map[Client]struct{}, len(service.Clients))
	for i, client := range service.Clients {
		if !validClient(client) {
			return fmt.Errorf("%s clients[%d] %q is invalid", context, i, client)
		}
		if _, exists := clients[client]; exists {
			return fmt.Errorf("%s clients[%d] %q is duplicate", context, i, client)
		}
		clients[client] = struct{}{}
	}
	return nil
}

func containsInlineCredential(arg string) bool {
	key, assigned := credentialArgumentKey(arg)
	return assigned && validCredentialKey(key)
}

func isCredentialFlag(arg string) bool {
	key, assigned := credentialArgumentKey(arg)
	return !assigned && validCredentialKey(key)
}

func credentialArgumentKey(arg string) (string, bool) {
	key, _, assigned := strings.Cut(strings.TrimLeft(strings.TrimSpace(strings.ToLower(arg)), "-"), "=")
	return key, assigned
}

func validCredentialKey(key string) bool {
	for _, credentialKey := range []string{"token", "password", "secret", "api-key", "api_key"} {
		if key == credentialKey {
			return true
		}
	}
	return false
}

func validTransport(value Transport) bool {
	return value == TransportStdio || value == TransportStreamableHTTP || value == TransportSSE
}

func validScope(value Scope) bool {
	return value == ScopeUser || value == ScopeRepository || value == ScopeSession
}

func validSessionPolicy(value SessionPolicy) bool {
	return value == SessionPolicyIsolated || value == SessionPolicySessionLocal
}

func validClient(value Client) bool {
	return value == ClientCodex || value == ClientClaude || value == ClientVSCode || value == ClientLingma
}
