package application

import (
	"context"
	"errors"
	"testing"

	"github.com/byteBuilderX/stratum/internal/mechanism/domain"
)

// fakeRepo 是 ProfileRepo 的内存替身（测试只 mock 外部依赖，不 mock 领域逻辑）。
type fakeRepo struct {
	profiles []domain.Profile
	err      error
}

func (f *fakeRepo) GetByFamilyKey(_ context.Context, familyKey string) (domain.Profile, bool, error) {
	for _, p := range f.profiles {
		if p.FamilyKey == familyKey {
			return p, true, nil
		}
	}
	return domain.Profile{}, false, nil
}

func (f *fakeRepo) List(_ context.Context) ([]domain.Profile, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.profiles, nil
}

func (f *fakeRepo) Upsert(_ context.Context, p domain.Profile) error {
	f.profiles = append(f.profiles, p)
	return nil
}

func profile(family, prefix string, status string) domain.Profile {
	return domain.Profile{
		FamilyKey:   family,
		DisplayName: family,
		Matcher:     domain.ModelMatcher{FamilyPrefixes: []string{prefix}},
		Status:      status,
		Baseline: domain.Baseline{
			Prompts: domain.BaselinePrompts{Compaction: "baseline-" + family},
		},
	}
}

func TestGetEffectiveResolvesProfileByModelPrefix(t *testing.T) {
	svc := NewService(&fakeRepo{profiles: []domain.Profile{
		profile("qwen", "qwen", domain.ProfileStatusActive),
	}})

	cases := []struct {
		name  string
		model string
		want  string // 期望 compaction prompt；空串表示回退种子
	}{
		{name: "exact family prefix hits", model: "qwen-turbo", want: "baseline-qwen"},
		{name: "unrelated model falls back to seed", model: "gpt-4o", want: ""},
		{name: "empty model falls back to seed", model: "", want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := svc.GetEffective(context.Background(), tc.model)
			if err != nil {
				t.Fatalf("GetEffective: unexpected error %v", err)
			}
			if tc.want == "" {
				if got.Prompts.Compaction != domain.DefaultBaseline().Prompts.Compaction {
					t.Fatalf("expected seed fallback, got %q", got.Prompts.Compaction)
				}
				return
			}
			if got.Prompts.Compaction != tc.want {
				t.Fatalf("expected %q, got %q", tc.want, got.Prompts.Compaction)
			}
		})
	}
}

func TestGetEffectiveSkipsDraftAndPicksMostSpecific(t *testing.T) {
	svc := NewService(&fakeRepo{profiles: []domain.Profile{
		profile("qwen", "qwen", domain.ProfileStatusDraft),          // draft 不生效
		profile("qwen-max", "qwen-max", domain.ProfileStatusActive), // 更具体前缀
	}})

	got, err := svc.GetEffective(context.Background(), "qwen-max-0428")
	if err != nil {
		t.Fatalf("GetEffective: %v", err)
	}
	if got.Prompts.Compaction != "baseline-qwen-max" {
		t.Fatalf("expected most specific profile, got %q", got.Prompts.Compaction)
	}
}

func TestGetEffectivePropagatesRepoError(t *testing.T) {
	svc := NewService(&fakeRepo{err: errors.New("db down")})
	if _, err := svc.GetEffective(context.Background(), "qwen-turbo"); err == nil {
		t.Fatal("expected error to propagate (fail closed)")
	}
}

func TestUpsertProfileAssignsFingerprintAndValidates(t *testing.T) {
	svc := NewService(&fakeRepo{})

	t.Run("valid profile gets id and fingerprint", func(t *testing.T) {
		p := profile("deepseek", "deepseek", "")
		if err := svc.UpsertProfile(context.Background(), p, "ops"); err != nil {
			t.Fatalf("UpsertProfile: %v", err)
		}
		got, err := svc.GetByFamilyKey(context.Background(), "deepseek")
		if err != nil {
			t.Fatalf("GetByFamilyKey: %v", err)
		}
		if got.ID == "" || got.Fingerprint == "" {
			t.Fatal("expected id and fingerprint to be assigned")
		}
		if got.Status != domain.ProfileStatusDraft {
			t.Fatalf("expected default draft status, got %q", got.Status)
		}
	})

	t.Run("invalid profile rejected", func(t *testing.T) {
		bad := domain.Profile{FamilyKey: "x", Matcher: domain.ModelMatcher{}} // 空前缀
		if err := svc.UpsertProfile(context.Background(), bad, "ops"); !errors.Is(err, domain.ErrInvalidProfile) {
			t.Fatalf("expected ErrInvalidProfile, got %v", err)
		}
	})
}

func TestComputeFingerprintStableAndSensitive(t *testing.T) {
	a := profile("qwen", "qwen", "")
	b := profile("qwen", "qwen", "")
	if ComputeFingerprint(a) != ComputeFingerprint(b) {
		t.Fatal("fingerprint must be stable for identical profiles")
	}
	b.Baseline.Prompts.Compaction = "changed"
	if ComputeFingerprint(a) == ComputeFingerprint(b) {
		t.Fatal("fingerprint must change when baseline changes")
	}
}
