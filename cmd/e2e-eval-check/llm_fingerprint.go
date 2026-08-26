package main

import "fmt"

// llmFingerprint hashes the model/instruction identity of a skill or agent
// point. Any change forces an explicit re-decision.
func llmFingerprint(snapshot map[string]any) fingerprint {
	return fingerprint{Hash: shortHash(llmFingerprintKey(snapshot))}
}

// llmFingerprintKey builds the configuration-identity key for a skill or agent
// snapshot. Skill snapshots carry the skill under `skill` (name/content are the
// declared snapshot values; at runtime the registry skill selected by name is
// executed) plus the carrier agent config under `agent`. Agent snapshots carry
// model/tools/system_prompt at the top level. Both include the effective model
// and system prompt so a behaviour-relevant config change is visible.
func llmFingerprintKey(snapshot map[string]any) string {
	if skill, ok := snapshot["skill"].(map[string]any); ok {
		agent, _ := snapshot["agent"].(map[string]any)
		return fmt.Sprintf("skill|%v|%v|%v|%v",
			skill["name"], skill["content"], agent["model"], agent["system_prompt"])
	}
	return fmt.Sprintf("agent|%v|%v|%v|%v",
		snapshot["id"], snapshot["model"], snapshot["tools"], snapshot["system_prompt"])
}
