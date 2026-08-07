package application

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/internal/iam/domain"
)

type settingsTenantRepo struct {
	name        string
	settings    []byte
	updatedName string
	updates     int
}

func (r *settingsTenantRepo) CountMembers(context.Context, string) (int, error) { return 0, nil }
func (r *settingsTenantRepo) ListMembers(context.Context, string, int, int) ([]domain.Member, error) {
	return nil, nil
}
func (r *settingsTenantRepo) ListMembersByRole(context.Context, string, []string) ([]domain.Member, error) {
	return nil, nil
}
func (r *settingsTenantRepo) GetMemberRole(context.Context, string, string) (string, error) {
	return "", domain.ErrMemberNotFound
}
func (r *settingsTenantRepo) UpdateMemberRole(context.Context, string, string, string) error {
	return nil
}
func (r *settingsTenantRepo) DeleteMember(context.Context, string, string) error { return nil }
func (r *settingsTenantRepo) GetTenantSettings(context.Context, string) (string, bool, []byte, error) {
	return r.name, false, append([]byte(nil), r.settings...), nil
}
func (r *settingsTenantRepo) UpdateTenantName(_ context.Context, _ string, name string) error {
	r.updatedName = name
	return nil
}
func (r *settingsTenantRepo) UpdateTenantSettings(_ context.Context, _ string, settings []byte) error {
	r.settings = append([]byte(nil), settings...)
	r.updates++
	return nil
}
func (r *settingsTenantRepo) ListUserTenants(context.Context, string) ([]domain.UserTenantInfo, error) {
	return nil, nil
}

func TestGetSettingsRemovesLegacyLLMAPIKeys(t *testing.T) {
	repo := &settingsTenantRepo{
		name:     "tenant",
		settings: []byte(`{"llm_api_keys":{"qwen":"encrypted"},"theme":"dark"}`),
	}
	svc := NewTenantService(repo, zap.NewNop())

	_, _, settings, err := svc.GetSettings(context.Background(), "tenant-1")
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	if _, exists := settings["llm_api_keys"]; exists {
		t.Fatal("legacy llm_api_keys must not be returned")
	}
	if settings["theme"] != "dark" {
		t.Fatalf("unrelated setting = %v, want dark", settings["theme"])
	}
}

func TestUpdateSettingsRejectsLegacyLLMAPIKeys(t *testing.T) {
	original := []byte(`{"theme":"dark"}`)
	repo := &settingsTenantRepo{settings: append([]byte(nil), original...)}
	svc := NewTenantService(repo, zap.NewNop())

	err := svc.UpdateSettings(context.Background(), "tenant-1", "admin", UpdateSettingsInput{
		Settings: map[string]interface{}{"llm_api_keys": map[string]interface{}{"qwen": "secret"}},
	})
	if !errors.Is(err, ErrInvalidSettings) {
		t.Fatalf("UpdateSettings error = %v, want ErrInvalidSettings", err)
	}
	if repo.updates != 0 {
		t.Fatalf("settings updates = %d, want 0", repo.updates)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(repo.settings, &got); err != nil {
		t.Fatal(err)
	}
	if got["theme"] != "dark" {
		t.Fatalf("stored settings changed: %v", got)
	}
}

func TestUpdateSettingsStillUpdatesNameAndOrdinarySettings(t *testing.T) {
	repo := &settingsTenantRepo{settings: []byte(`{"theme":"dark"}`)}
	svc := NewTenantService(repo, zap.NewNop())

	err := svc.UpdateSettings(context.Background(), "tenant-1", "owner", UpdateSettingsInput{
		Name: "renamed", Settings: map[string]interface{}{"locale": "zh-CN"},
	})
	if err != nil {
		t.Fatalf("UpdateSettings: %v", err)
	}
	if repo.updatedName != "renamed" || repo.updates != 1 {
		t.Fatalf("name=%q updates=%d", repo.updatedName, repo.updates)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(repo.settings, &got); err != nil {
		t.Fatal(err)
	}
	if got["theme"] != "dark" || got["locale"] != "zh-CN" {
		t.Fatalf("unexpected merged settings: %v", got)
	}
}
