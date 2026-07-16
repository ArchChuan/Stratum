package process

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/byteBuilderX/stratum/tools/mcp-governor/internal/jsonstrict"
)

const RegistryVersion = 1

type Registration struct {
	Identity    Identity  `json:"identity"`
	Client      Identity  `json:"client"`
	Service     string    `json:"service"`
	Repository  string    `json:"repository,omitempty"`
	ConnectedAt time.Time `json:"connected_at"`
}

type Registry struct {
	Version       int            `json:"version"`
	Registrations []Registration `json:"registrations"`
}

func DecodeRegistry(reader io.Reader) (Registry, error) {
	data, err := io.ReadAll(reader)
	if err != nil {
		return Registry{}, fmt.Errorf("read registry: %w", err)
	}
	if err := jsonstrict.ValidateNoDuplicateKeys(data); err != nil {
		return Registry{}, fmt.Errorf("decode registry: %w", err)
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()

	var registry Registry
	if err := decoder.Decode(&registry); err != nil {
		return Registry{}, fmt.Errorf("decode registry: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return Registry{}, errors.New("decode registry: trailing JSON value")
		}
		return Registry{}, fmt.Errorf("decode registry trailing data: %w", err)
	}
	if registry.Version != RegistryVersion {
		return Registry{}, fmt.Errorf("unsupported registry version %d", registry.Version)
	}

	seen := make(map[Identity]struct{}, len(registry.Registrations))
	for i, registration := range registry.Registrations {
		if err := validateIdentity(registration.Identity); err != nil {
			return Registry{}, fmt.Errorf("registration %d identity: %w", i, err)
		}
		if err := validateIdentity(registration.Client); err != nil {
			return Registry{}, fmt.Errorf("registration %d client: %w", i, err)
		}
		if strings.TrimSpace(registration.Service) == "" {
			return Registry{}, fmt.Errorf("registration %d has empty service", i)
		}
		if registration.ConnectedAt.IsZero() {
			return Registry{}, fmt.Errorf("registration %d has zero connected_at", i)
		}
		if _, exists := seen[registration.Identity]; exists {
			return Registry{}, fmt.Errorf("duplicate process identity %+v", registration.Identity)
		}
		seen[registration.Identity] = struct{}{}
	}
	return registry, nil
}

func validateIdentity(identity Identity) error {
	if identity.PID <= 0 {
		return fmt.Errorf("invalid PID %d", identity.PID)
	}
	if identity.StartTicks == 0 {
		return errors.New("start ticks must be positive")
	}
	return nil
}
