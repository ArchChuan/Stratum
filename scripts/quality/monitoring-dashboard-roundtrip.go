package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"

	"gopkg.in/yaml.v3"
)

var dashboardNames = []string{
	"stratum-service-overview",
	"stratum-http",
	"stratum-resources",
	"stratum-dependencies",
}

var dashboardFilenames = []string{
	"stratum-service-overview.json",
	"stratum-http.json",
	"stratum-resources.json",
	"stratum-dependencies.json",
}

type dashboardList struct {
	APIVersion string               `yaml:"apiVersion"`
	Kind       string               `yaml:"kind"`
	Items      []dashboardConfigMap `yaml:"items"`
}

type dashboardConfigMap struct {
	APIVersion string            `yaml:"apiVersion"`
	Kind       string            `yaml:"kind"`
	Metadata   dashboardMetadata `yaml:"metadata"`
	Data       map[string]string `yaml:"data"`
}

type dashboardMetadata struct {
	Name string `yaml:"name"`
}

func validateDashboardRoundTrip(manifestPath, dashboardDir string) error {
	manifestRoot, manifestName, err := openFileRoot(manifestPath)
	if err != nil {
		return fmt.Errorf("open dashboard manifest root: %w", err)
	}
	defer manifestRoot.Close()
	manifestBytes, err := manifestRoot.ReadFile(manifestName)
	if err != nil {
		return fmt.Errorf("read dashboard manifest: %w", err)
	}
	var manifest dashboardList
	if err := yaml.Unmarshal(manifestBytes, &manifest); err != nil {
		return fmt.Errorf("parse dashboard manifest: %w", err)
	}
	if manifest.APIVersion != "v1" || manifest.Kind != "List" || len(manifest.Items) != len(dashboardNames) {
		return fmt.Errorf("dashboard manifest must be a v1 List with exactly four items")
	}
	dashboardRoot, err := os.OpenRoot(dashboardDir)
	if err != nil {
		return fmt.Errorf("open dashboard source root: %w", err)
	}
	defer dashboardRoot.Close()
	for index, item := range manifest.Items {
		name, filename := dashboardNames[index], dashboardFilenames[index]
		if item.APIVersion != "v1" || item.Kind != "ConfigMap" || item.Metadata.Name != name {
			return fmt.Errorf("dashboard item %d must be ConfigMap %s", index, name)
		}
		if len(item.Data) != 1 {
			return fmt.Errorf("dashboard ConfigMap %s must have exactly one data key", name)
		}
		embedded, ok := item.Data[filename]
		if !ok {
			return fmt.Errorf("dashboard ConfigMap %s missing %s", name, filename)
		}
		sourceBytes, err := dashboardRoot.ReadFile(filename)
		if err != nil {
			return fmt.Errorf("read source dashboard %s: %w", filename, err)
		}
		var embeddedJSON, sourceJSON any
		if err := json.Unmarshal([]byte(embedded), &embeddedJSON); err != nil {
			return fmt.Errorf("parse embedded dashboard %s: %w", filename, err)
		}
		if err := json.Unmarshal(sourceBytes, &sourceJSON); err != nil {
			return fmt.Errorf("parse source dashboard %s: %w", filename, err)
		}
		if !reflect.DeepEqual(embeddedJSON, sourceJSON) {
			return fmt.Errorf("embedded dashboard differs from source: %s", filename)
		}
	}
	return nil
}

func openFileRoot(path string) (*os.Root, string, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, "", fmt.Errorf("resolve absolute path: %w", err)
	}
	root, err := os.OpenRoot(filepath.Dir(absPath))
	if err != nil {
		return nil, "", err
	}
	return root, filepath.Base(absPath), nil
}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: monitoring-dashboard-roundtrip <manifest> <dashboard-dir>")
		os.Exit(2)
	}
	if err := validateDashboardRoundTrip(os.Args[1], os.Args[2]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
