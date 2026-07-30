package e2erunscope

import (
	cryptorand "crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	scopeSchemaVersion = 1
	scopeTTL           = 24 * time.Hour
	randomSuffixBytes  = 8
	minimumPort        = 1
	maximumPort        = 65535
)

var (
	runIDPattern        = regexp.MustCompile(`^[0-9]{8}t[0-9]{6}z-[a-f0-9]{16}$`)
	databasePattern     = regexp.MustCompile(`^stratum_e2e_[0-9]{8}t[0-9]{6}z_[a-f0-9]{16}$`)
	baseDatabasePattern = regexp.MustCompile(`^(?:stratum_)?(?:test|e2e)(?:_[a-z0-9]+)*$`)
)

type Ports struct {
	Frontend int `json:"frontend"`
	Backend  int `json:"backend"`
	OAuth    int `json:"oauth"`
	Fixture  int `json:"fixture"`
}

type InfrastructureLease struct {
	LeaseID      string `json:"lease_id"`
	StartedByE2E bool   `json:"started_by_e2e"`
}

type Scope struct {
	SchemaVersion  int                 `json:"schema_version"`
	RunID          string              `json:"run_id"`
	OwnerPID       int                 `json:"owner_pid"`
	CreatedAt      time.Time           `json:"created_at"`
	ExpiresAt      time.Time           `json:"expires_at"`
	Repository     string              `json:"repository"`
	DatabaseName   string              `json:"database_name"`
	Ports          Ports               `json:"ports"`
	Infrastructure InfrastructureLease `json:"infrastructure"`
}

type RuntimeURLs struct {
	Frontend string `json:"frontend"`
	Backend  string `json:"backend"`
	OAuth    string `json:"oauth"`
	Fixture  string `json:"fixture"`
}

func NewScope(repository string, ownerPID int, now time.Time, random io.Reader) (Scope, error) {
	if !filepath.IsAbs(repository) {
		return Scope{}, errors.New("run scope: repository path must be absolute")
	}
	if ownerPID <= 0 {
		return Scope{}, errors.New("run scope: owner PID must be positive")
	}
	if random == nil {
		random = cryptorand.Reader
	}

	suffixBytes := make([]byte, randomSuffixBytes)
	if _, err := io.ReadFull(random, suffixBytes); err != nil {
		return Scope{}, fmt.Errorf("run scope: generate random suffix: %w", err)
	}
	ports, err := AllocatePorts()
	if err != nil {
		return Scope{}, err
	}

	createdAt := now.UTC()
	timestamp := createdAt.Format("20060102t150405z")
	suffix := hex.EncodeToString(suffixBytes)
	runID := timestamp + "-" + suffix
	scope := Scope{
		SchemaVersion: scopeSchemaVersion,
		RunID:         runID,
		OwnerPID:      ownerPID,
		CreatedAt:     createdAt,
		ExpiresAt:     createdAt.Add(scopeTTL),
		Repository:    repository,
		DatabaseName:  "stratum_e2e_" + timestamp + "_" + suffix,
		Ports:         ports,
		Infrastructure: InfrastructureLease{
			LeaseID: runID,
		},
	}
	if err := Validate(scope); err != nil {
		return Scope{}, fmt.Errorf("run scope: validate generated scope: %w", err)
	}
	return scope, nil
}

func AllocatePorts() (Ports, error) {
	listeners := make([]*net.TCPListener, 0, 4)
	for range 4 {
		listener, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
		if err != nil {
			return Ports{}, errors.Join(fmt.Errorf("allocate loopback port: %w", err), closeListeners(listeners))
		}
		listeners = append(listeners, listener)
	}

	ports := Ports{
		Frontend: listeners[0].Addr().(*net.TCPAddr).Port,
		Backend:  listeners[1].Addr().(*net.TCPAddr).Port,
		OAuth:    listeners[2].Addr().(*net.TCPAddr).Port,
		Fixture:  listeners[3].Addr().(*net.TCPAddr).Port,
	}
	validationErr := validatePorts(ports)
	if closeErr := closeListeners(listeners); closeErr != nil {
		return Ports{}, errors.Join(validationErr, closeErr)
	}
	if validationErr != nil {
		return Ports{}, validationErr
	}
	return ports, nil
}

func Validate(scope Scope) error {
	if err := validateIdentity(scope); err != nil {
		return err
	}
	if err := validateMetadata(scope); err != nil {
		return err
	}
	return validatePorts(scope.Ports)
}

func validateIdentity(scope Scope) error {
	if scope.SchemaVersion != scopeSchemaVersion {
		return errors.New("run scope: unsupported schema version")
	}
	if !runIDPattern.MatchString(scope.RunID) {
		return errors.New("run scope: invalid run ID")
	}
	if !databasePattern.MatchString(scope.DatabaseName) {
		return errors.New("run scope: invalid database name")
	}
	if scope.Infrastructure.LeaseID != scope.RunID {
		return errors.New("run scope: infrastructure lease does not match run ID")
	}
	return nil
}

func validateMetadata(scope Scope) error {
	if scope.OwnerPID <= 0 {
		return errors.New("run scope: owner PID must be positive")
	}
	if !filepath.IsAbs(scope.Repository) {
		return errors.New("run scope: repository path must be absolute")
	}
	if scope.CreatedAt.IsZero() || scope.ExpiresAt.IsZero() {
		return errors.New("run scope: timestamps are required")
	}
	if scope.CreatedAt.Location() != time.UTC || scope.ExpiresAt.Location() != time.UTC {
		return errors.New("run scope: timestamps must use UTC")
	}
	if !scope.ExpiresAt.Equal(scope.CreatedAt.Add(scopeTTL)) {
		return errors.New("run scope: expiry must be 24 hours after creation")
	}
	return nil
}

func DatabaseURL(base string, databaseName string) (string, error) {
	if !databasePattern.MatchString(databaseName) {
		return "", errors.New("database URL: invalid target database name")
	}
	parsed, err := parseE2EBaseURL(base)
	if err != nil {
		return "", err
	}
	parsed.Path = "/" + databaseName
	parsed.RawPath = ""
	return parsed.String(), nil
}

func MaintenanceURL(base string) (string, error) {
	parsed, err := parseE2EBaseURL(base)
	if err != nil {
		return "", err
	}
	parsed.Path = "/postgres"
	parsed.RawPath = ""
	return parsed.String(), nil
}

func URLs(ports Ports) RuntimeURLs {
	return RuntimeURLs{
		Frontend: loopbackURL(ports.Frontend),
		Backend:  loopbackURL(ports.Backend),
		OAuth:    loopbackURL(ports.OAuth),
		Fixture:  loopbackURL(ports.Fixture),
	}
}

func parseE2EBaseURL(base string) (*url.URL, error) {
	parsed, err := url.Parse(base)
	if err != nil {
		return nil, errors.New("database URL: invalid base DSN")
	}
	if parsed.Scheme != "postgres" && parsed.Scheme != "postgresql" {
		return nil, errors.New("database URL: unsupported scheme")
	}
	if parsed.Opaque != "" || parsed.Fragment != "" {
		return nil, errors.New("database URL: opaque URLs and fragments are not allowed")
	}
	if !safePostgresHost(parsed.Hostname()) {
		return nil, errors.New("database URL: host is outside the E2E boundary")
	}
	databaseName := strings.TrimPrefix(parsed.Path, "/")
	if databaseName == "" || strings.Contains(databaseName, "/") || !isE2EDatabase(databaseName) {
		return nil, errors.New("database URL: base database is outside the E2E boundary")
	}
	return parsed, nil
}

func safePostgresHost(host string) bool {
	switch strings.ToLower(host) {
	case "127.0.0.1", "localhost":
		return true
	default:
		return false
	}
}

func isE2EDatabase(databaseName string) bool {
	return baseDatabasePattern.MatchString(strings.ToLower(databaseName))
}

func validatePorts(ports Ports) error {
	values := []int{ports.Frontend, ports.Backend, ports.OAuth, ports.Fixture}
	seen := make(map[int]struct{}, len(values))
	for _, port := range values {
		if port < minimumPort || port > maximumPort {
			return errors.New("run scope: port is outside the TCP range")
		}
		if _, exists := seen[port]; exists {
			return errors.New("run scope: ports must be distinct")
		}
		seen[port] = struct{}{}
	}
	return nil
}

func closeListeners(listeners []*net.TCPListener) error {
	errs := make([]error, 0, len(listeners))
	for _, listener := range listeners {
		if err := listener.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close port probe: %w", err))
		}
	}
	return errors.Join(errs...)
}

func loopbackURL(port int) string {
	return fmt.Sprintf("http://127.0.0.1:%d", port)
}
