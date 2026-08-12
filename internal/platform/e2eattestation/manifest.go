package e2eattestation

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

var manifestDomains = map[string]struct{}{
	"dashboard": {}, "iam": {}, "agent": {}, "skill": {}, "mcp": {},
	"knowledge": {}, "memory": {}, "evaluation": {}, "workflow": {},
	"llm-admin": {}, "collab": {}, "scheduled-task": {},
	"prompt": {}, "audit": {}, "mechanism": {},
}

var coverageLevels = map[string]struct{}{
	"short": {}, "soak": {}, "lower_layer": {},
}

type Manifest struct {
	Version      int          `json:"version"`
	Capabilities []Capability `json:"capabilities"`
}

type Capability struct {
	ID                      string       `json:"id"`
	Domain                  string       `json:"domain"`
	UserGoal                string       `json:"user_goal"`
	Route                   string       `json:"route"`
	Roles                   RoleCoverage `json:"roles"`
	Mutation                string       `json:"mutation"`
	BrowserActionID         string       `json:"browser_action_id"`
	HTTPEvidence            string       `json:"http_evidence"`
	DBEvidence              string       `json:"db_evidence"`
	Coverage                string       `json:"coverage"`
	LowerLayerJustification string       `json:"lower_layer_justification,omitempty"`
}

type RoleCoverage struct {
	Allowed []string `json:"allowed"`
	Denied  []string `json:"denied"`
}

func LoadManifest(path string) (Manifest, error) {
	file, err := os.Open(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("open manifest: %w", err)
	}
	defer file.Close()

	var manifest Manifest
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode manifest: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func ValidateManifest(
	manifest Manifest,
	routes, mutations []string,
	actions map[string]struct{},
) error {
	var validationErrors []error
	if manifest.Version <= 0 {
		validationErrors = append(validationErrors, errors.New("manifest version must be positive"))
	}

	capabilityIDs := make(map[string]struct{}, len(manifest.Capabilities))
	mappedRoutes := make(map[string]struct{}, len(manifest.Capabilities))
	mappedMutations := make(map[string]struct{}, len(manifest.Capabilities))
	for i, capability := range manifest.Capabilities {
		label := fmt.Sprintf("capability[%d]", i)
		if capability.ID == "" {
			validationErrors = append(validationErrors, fmt.Errorf("%s: capability ID is required", label))
		} else if _, exists := capabilityIDs[capability.ID]; exists {
			validationErrors = append(validationErrors, fmt.Errorf("duplicate capability ID %q", capability.ID))
		} else {
			capabilityIDs[capability.ID] = struct{}{}
		}
		if _, ok := manifestDomains[capability.Domain]; !ok {
			validationErrors = append(validationErrors, fmt.Errorf("%s: unknown domain %q", label, capability.Domain))
		}
		if strings.TrimSpace(capability.UserGoal) == "" {
			validationErrors = append(validationErrors, fmt.Errorf("%s: user goal is required", label))
		}
		if strings.TrimSpace(capability.Route) == "" {
			validationErrors = append(validationErrors, fmt.Errorf("%s: route is required", label))
		} else {
			mappedRoutes[capability.Route] = struct{}{}
		}
		if len(capability.Roles.Allowed) == 0 {
			validationErrors = append(validationErrors, fmt.Errorf("%s: allowed role coverage is required", label))
		}
		if capability.Roles.Denied == nil {
			validationErrors = append(validationErrors, fmt.Errorf("%s: denied role coverage is required", label))
		}
		if strings.TrimSpace(capability.Mutation) == "" {
			validationErrors = append(validationErrors, fmt.Errorf("%s: mutation or state transition is required", label))
		} else {
			mappedMutations[capability.Mutation] = struct{}{}
		}
		if strings.TrimSpace(capability.BrowserActionID) == "" {
			validationErrors = append(validationErrors, fmt.Errorf("%s: browser action ID is required", label))
		} else if _, ok := actions[capability.BrowserActionID]; !ok {
			validationErrors = append(validationErrors, fmt.Errorf(
				"%s: unknown browser action ID %q", label, capability.BrowserActionID,
			))
		}
		if strings.TrimSpace(capability.HTTPEvidence) == "" {
			validationErrors = append(validationErrors, fmt.Errorf("%s: HTTP evidence is required", label))
		}
		if strings.TrimSpace(capability.DBEvidence) == "" {
			validationErrors = append(validationErrors, fmt.Errorf("%s: DB evidence is required", label))
		}
		if _, ok := coverageLevels[capability.Coverage]; !ok {
			validationErrors = append(validationErrors, fmt.Errorf("%s: unknown coverage %q", label, capability.Coverage))
		}
		justification := strings.TrimSpace(capability.LowerLayerJustification)
		if capability.Coverage == "lower_layer" && justification == "" {
			validationErrors = append(validationErrors, fmt.Errorf("%s: lower-layer justification is required", label))
		}
		if capability.Coverage != "lower_layer" && justification != "" {
			validationErrors = append(validationErrors, fmt.Errorf("%s: lower-layer justification is only valid for lower_layer coverage", label))
		}
	}

	for _, route := range routes {
		if _, ok := mappedRoutes[route]; !ok {
			validationErrors = append(validationErrors, fmt.Errorf("unmapped route %q", route))
		}
	}
	for _, mutation := range mutations {
		if _, ok := mappedMutations[mutation]; !ok {
			validationErrors = append(validationErrors, fmt.Errorf("unmapped mutation %q", mutation))
		}
	}

	return errors.Join(validationErrors...)
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var trailing any
	err := decoder.Decode(&trailing)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("decode trailing manifest data: %w", err)
	}
	return errors.New("decode manifest: multiple JSON values")
}
