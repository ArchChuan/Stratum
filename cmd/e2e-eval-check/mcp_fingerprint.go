package main

import (
	"fmt"
	"sort"
)

// mcpFingerprint hashes the server URLs and declared tool set. The server map
// is iterated in sorted name order so the hash is stable across runs (map
// iteration order is random; a drifting fingerprint would falsely mark every
// run non-comparable).
func mcpFingerprint(snapshot map[string]any) fingerprint {
	cfg, err := parseMCPServers(snapshot)
	if err != nil {
		return fingerprint{Hash: "invalid"}
	}
	names := make([]string, 0, len(cfg))
	for name := range cfg {
		names = append(names, name)
	}
	sort.Strings(names)
	var key string
	for _, name := range names {
		key += fmt.Sprintf("%s@%s;", name, cfg[name].URL)
	}
	return fingerprint{Hash: shortHash(key)}
}
