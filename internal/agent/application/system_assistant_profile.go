package application

import (
	"fmt"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
)

const systemAssistantPrompt = "You are Stratum's platform assistant.\n" +
	"Operate only on evidence from the current authenticated tenant. " +
	"Never access or infer data from another tenant.\n" +
	"Claims about Stratum behavior require citations from retrieved official documentation. " +
	"If no official citation is available, state the evidence gap instead of presenting general knowledge " +
	"as an official answer.\n" +
	"Separate confirmed facts, evidence-supported inferences, and missing or failed evidence " +
	"in every diagnostic response.\n" +
	"This profile is read-only: do not create, update, delete, publish, deploy, or execute tenant resources " +
	"or tools that perform writes.\n" +
	"Never request passwords, tokens, API keys, private keys, or other secrets, and never include secrets " +
	"in prompts, responses, traces, or logs.\n" +
	"Unavailable diagnostic evidence is an evidence gap; it must never be reported as proof " +
	"that the system is healthy."

// BuiltinSystemAssistantProfiles retains every released profile version.
func BuiltinSystemAssistantProfiles() map[string]domain.SystemAssistantProfile {
	return map[string]domain.SystemAssistantProfile{
		domain.CurrentSystemAssistantProfileVersion: {
			Key: domain.SystemAssistantKey, Version: domain.CurrentSystemAssistantProfileVersion,
			Name: "Stratum 平台助手", Description: "基于官方资料提供平台使用指导和当前租户的只读诊断。",
			SystemPrompt: systemAssistantPrompt, MaxIterations: 8, MaxContextTokens: 32768,
		},
	}
}

// BuiltinSystemAssistantProfile returns a copy of the active profile.
func BuiltinSystemAssistantProfile() *domain.SystemAssistantProfile {
	profile := BuiltinSystemAssistantProfiles()[domain.CurrentSystemAssistantProfileVersion]
	return &profile
}

// ComposeSystemAssistantProfile creates a fresh runtime config. Ordinary
// agents are copied unchanged. Managed assistants preserve only tenant-owned
// runtime selections and receive every other field from the platform profile.
func ComposeSystemAssistantProfile(
	cfg *domain.AgentConfig, profile *domain.SystemAssistantProfile,
) (*domain.AgentConfig, error) {
	if cfg == nil {
		return nil, fmt.Errorf("compose system assistant profile: nil agent config")
	}
	copyCfg := *cfg
	copyCfg.AllowedSkills = append([]string(nil), cfg.AllowedSkills...)
	copyCfg.MCPToolIDs = append([]string(nil), cfg.MCPToolIDs...)
	copyCfg.Capabilities = append([]domain.AgentCapability(nil), cfg.Capabilities...)
	copyCfg.KnowledgeWorkspaceIDs = append([]string(nil), cfg.KnowledgeWorkspaceIDs...)
	copyCfg.KnowledgeWorkspaceNames = append([]string(nil), cfg.KnowledgeWorkspaceNames...)
	copyCfg.KnowledgeWorkspaceDescriptions = append(
		[]string(nil), cfg.KnowledgeWorkspaceDescriptions...,
	)
	if cfg.SystemKey != domain.SystemAssistantKey {
		return &copyCfg, nil
	}
	if profile == nil {
		return nil, fmt.Errorf("compose system assistant profile: nil profile")
	}
	known, ok := BuiltinSystemAssistantProfiles()[profile.Version]
	if !ok || profile.Key != domain.SystemAssistantKey || known.Key != profile.Key {
		return nil, fmt.Errorf(
			"compose system assistant profile: unknown profile %q version %q", profile.Key, profile.Version,
		)
	}
	profile = &known

	return &domain.AgentConfig{
		ID: cfg.ID, Name: profile.Name, Type: domain.ReActAgent, Description: profile.Description,
		SystemPrompt: profile.SystemPrompt, LLMModel: cfg.LLMModel, EmbedModel: cfg.EmbedModel,
		MaxIterations: profile.MaxIterations, MaxContextTokens: profile.MaxContextTokens,
		MemoryScope: cfg.MemoryScope, SystemKey: profile.Key, IsSystem: true, ManagementMode: "platform",
	}, nil
}
