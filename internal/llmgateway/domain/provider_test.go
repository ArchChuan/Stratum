package domain

import (
	"testing"
)

func TestProviderKindValues(t *testing.T) {
	if ProviderOpenAICompat != "openai_compat" {
		t.Errorf("expected openai_compat, got %s", ProviderOpenAICompat)
	}
	if ProviderAnthropic != "anthropic" {
		t.Errorf("expected anthropic, got %s", ProviderAnthropic)
	}
	if ProviderOllama != "ollama" {
		t.Errorf("expected ollama, got %s", ProviderOllama)
	}
}

func TestModelCapabilityValues(t *testing.T) {
	caps := map[ModelCapability]bool{
		CapChat: true, CapEmbedding: true,
		CapVision: true, CapToolUse: true,
		CapReasoning: true,
	}
	if len(caps) != 5 {
		t.Errorf("expected 5 capabilities, got %d", len(caps))
	}
}
