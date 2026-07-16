package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

type Config struct {
	Version      int           `json:"version"`
	OutputPath   string        `json:"output_path"`
	RegistryPath string        `json:"registry_path"`
	Services     []ServiceRule `json:"services"`
}

type ServiceRule struct {
	Name           string   `json:"name"`
	AllArgsContain []string `json:"all_args_contain"`
}

func Decode(r io.Reader) (Config, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	if err := rejectDuplicateKeys(json.NewDecoder(bytes.NewReader(data)), "$"); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}

	var cfg Config
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}

	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return Config{}, fmt.Errorf("decode config: trailing JSON value")
		}
		return Config{}, fmt.Errorf("decode config trailing data: %w", err)
	}
	if err := validate(cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func rejectDuplicateKeys(decoder *json.Decoder, path string) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}

	switch delim {
	case '{':
		keys := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("object key at %s is not a string", path)
			}
			if _, exists := keys[key]; exists {
				return fmt.Errorf("duplicate key %q at %s", key, path)
			}
			keys[key] = struct{}{}
			if err := rejectDuplicateKeys(decoder, path+"."+key); err != nil {
				return err
			}
		}
	case '[':
		for index := 0; decoder.More(); index++ {
			if err := rejectDuplicateKeys(decoder, fmt.Sprintf("%s[%d]", path, index)); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter %q at %s", delim, path)
	}

	_, err = decoder.Token()
	return err
}

func validate(cfg Config) error {
	if cfg.Version != 1 {
		return fmt.Errorf("config version must be 1, got %d", cfg.Version)
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

	names := make(map[string]struct{}, len(cfg.Services))
	for i, service := range cfg.Services {
		context := fmt.Sprintf("config services[%d]", i)
		if strings.TrimSpace(service.Name) == "" {
			return fmt.Errorf("%s name must not be empty", context)
		}
		if _, exists := names[service.Name]; exists {
			return fmt.Errorf("%s name %q is duplicate", context, service.Name)
		}
		names[service.Name] = struct{}{}
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
