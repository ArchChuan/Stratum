package application

import (
	"context"
	"errors"
	"testing"

	"github.com/byteBuilderX/stratum/internal/mechanism/domain"
	"github.com/byteBuilderX/stratum/internal/mechanism/domain/port"
)

// fakeModelExists 是 ModelExists 的内存替身：命中集 + 可注入错误。
type fakeModelExists struct {
	models map[string]bool
	err    error
}

func (f *fakeModelExists) Exists(_ context.Context, model string, _ port.ModelCapability) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	return f.models[model], nil
}

// fakeRepo 是 ProfileRepo 的内存替身（测试只 mock 外部依赖，不 mock 领域逻辑）。
type fakeRepo struct {
	profiles  []domain.Profile
	err       error
	upsertErr error
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
	if f.upsertErr != nil {
		return f.upsertErr
	}
	for i, existing := range f.profiles {
		if existing.FamilyKey == p.FamilyKey {
			// 模拟 SQL 层 ON CONFLICT：version=model_profiles.version+1
			p.Version = existing.Version + 1
			f.profiles[i] = p
			return nil
		}
	}
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

func TestUpsertProfile_propagatesRepoError(t *testing.T) {
	svc := NewService(&fakeRepo{upsertErr: errors.New("db down")})
	err := svc.UpsertProfile(context.Background(), profile("qwen", "qwen", ""), "ops")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// profileWithModels 构造带模型引用的档案（供存在性校验用例）。
func profileWithModels(family, prefix string, models domain.BaselineModels) domain.Profile {
	p := profile(family, prefix, domain.ProfileStatusDraft)
	p.Baseline.Models = models
	return p
}

func TestUpsertProfile_requiresModelsInGlobalCatalog(t *testing.T) {
	// qwen-turbo 在目录、qwen-max 不在。
	exists := &fakeModelExists{models: map[string]bool{"qwen-turbo": true}}
	svc := NewService(&fakeRepo{}, exists)

	t.Run("non-empty model not in catalog rejected", func(t *testing.T) {
		p := profileWithModels("qwen", "qwen", domain.BaselineModels{JudgeModel: "qwen-max"})
		err := svc.UpsertProfile(context.Background(), p, "ops")
		if !errors.Is(err, domain.ErrInvalidProfile) {
			t.Fatalf("expected ErrInvalidProfile for unknown model, got %v", err)
		}
	})

	t.Run("empty models skip existence check", func(t *testing.T) {
		if err := svc.UpsertProfile(context.Background(), profile("qwen", "qwen", ""), "ops"); err != nil {
			t.Fatalf("UpsertProfile: %v", err)
		}
	})

	t.Run("all models in catalog accepted", func(t *testing.T) {
		p := profileWithModels("qwen", "qwen", domain.BaselineModels{
			EnrichModel:     "qwen-turbo",
			SummaryModel:    "qwen-turbo",
			ExtractionModel: "qwen-turbo",
			JudgeModel:      "qwen-turbo",
		})
		if err := svc.UpsertProfile(context.Background(), p, "ops"); err != nil {
			t.Fatalf("UpsertProfile: %v", err)
		}
	})
}

func TestUpsertProfile_propagatesCatalogError(t *testing.T) {
	svc := NewService(&fakeRepo{}, &fakeModelExists{err: errors.New("catalog down")})
	p := profileWithModels("qwen", "qwen", domain.BaselineModels{JudgeModel: "qwen-turbo"})
	if err := svc.UpsertProfile(context.Background(), p, "ops"); err == nil {
		t.Fatal("expected catalog error to propagate (fail closed)")
	}
}

func TestUpsertProfile_defaultsActorToApi(t *testing.T) {
	repo := &fakeRepo{}
	svc := NewService(repo)
	p := profile("qwen", "qwen", "")
	if err := svc.UpsertProfile(context.Background(), p, ""); err != nil {
		t.Fatalf("UpsertProfile: %v", err)
	}
	if repo.profiles[0].CreatedBy != "api" {
		t.Fatalf("expected actor fallback 'api', got %q", repo.profiles[0].CreatedBy)
	}
}

func TestListProfiles_propagatesRepoError(t *testing.T) {
	svc := NewService(&fakeRepo{err: errors.New("db down")})
	if _, err := svc.ListProfiles(context.Background()); err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestUpsertInvalidatesCache(t *testing.T) {
	repo := &fakeRepo{profiles: []domain.Profile{
		profile("qwen", "qwen", domain.ProfileStatusActive),
	}}
	svc := NewService(repo)

	got, err := svc.GetEffective(context.Background(), "qwen-turbo")
	if err != nil {
		t.Fatalf("GetEffective: %v", err)
	}
	if got.Prompts.Compaction != "baseline-qwen" {
		t.Fatalf("expected cached baseline, got %q", got.Prompts.Compaction)
	}

	// 更新档案后，缓存必须失效（下一次解析取新值）。
	updated := profile("qwen", "qwen", domain.ProfileStatusActive)
	updated.Baseline.Prompts.Compaction = "baseline-qwen-v2"
	if err := svc.UpsertProfile(context.Background(), updated, "ops"); err != nil {
		t.Fatalf("UpsertProfile: %v", err)
	}
	got, err = svc.GetEffective(context.Background(), "qwen-turbo")
	if err != nil {
		t.Fatalf("GetEffective: %v", err)
	}
	if got.Prompts.Compaction != "baseline-qwen-v2" {
		t.Fatalf("expected invalidated cache to see new value, got %q", got.Prompts.Compaction)
	}
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
