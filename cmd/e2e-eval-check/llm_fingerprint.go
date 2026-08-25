package main

import "fmt"

// llmFingerprint hashes the model/instruction/tool identity of a skill or
// agent point. Any change forces an explicit re-decision.
func llmFingerprint(snapshot map[string]any) fingerprint {
	key := fmt.Sprintf("%v|%v|%v", snapshot["id"], snapshot["model"], snapshot["tools"])
	return fingerprint{Hash: shortHash(key)}
}
