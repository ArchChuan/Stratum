package main

import "fmt"

// knowledgeFingerprint hashes the effective retrieval config plus the declared
// embedding provider. Any change marks the run non-comparable.
func knowledgeFingerprint(snapshot map[string]any, provider string) fingerprint {
	key := fmt.Sprintf("%v|%v|%v|%v|%v|%v|%v|%v|%s",
		snapshot["embedding_model"], snapshot["query_mode"], snapshot["top_k"],
		snapshot["chunk_size"], snapshot["chunk_overlap"], snapshot["score_threshold"],
		snapshot["reranking"], snapshot["query_rewrite"], provider)
	return fingerprint{
		Hash:         shortHash(key),
		ProviderHash: shortHash(provider),
	}
}
