package application

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/google/uuid"

	"github.com/byteBuilderX/stratum/internal/mechanism/domain"
	"github.com/byteBuilderX/stratum/internal/mechanism/domain/port"
)

// Service 是机制基线（model_profiles）的应用门面：解析、管理、指纹。
// 单节点部署下使用进程内缓存（model → baseline），Upsert 后失效；
// 多副本部署时由部署层保证配置同步（当前单节点 k3s，见部署架构）。
type Service struct {
	repo port.ProfileRepo

	mu    sync.RWMutex
	cache map[string]domain.Baseline
}

// NewService 构造 Service。repo 不可为空（DB 缺失时由 wiring 决定不构造）。
func NewService(repo port.ProfileRepo) *Service {
	return &Service{repo: repo, cache: make(map[string]domain.Baseline)}
}

// ErrProfileNotFound 标记按族键查询缺失。
var ErrProfileNotFound = errors.New("mechanism: profile not found")

// GetEffective 返回模型命中的档案基线；无档案命中时回退 embedded 种子
// （DefaultBaseline，行为与改造前一致）。匹配顺序：完整族键 → 前缀族回退
// （按 prefix 长度降序，最具体者胜）。
func (s *Service) GetEffective(ctx context.Context, model string) (domain.Baseline, error) {
	if model == "" {
		return domain.DefaultBaseline(), nil
	}
	s.mu.RLock()
	if b, ok := s.cache[model]; ok {
		s.mu.RUnlock()
		return b, nil
	}
	s.mu.RUnlock()

	b, err := s.resolve(ctx, model)
	if err != nil {
		return domain.Baseline{}, err
	}
	s.mu.Lock()
	s.cache[model] = b
	s.mu.Unlock()
	return b, nil
}

// resolve 是未命中缓存时的真实解析：全量档案 + Go 侧族前缀匹配。
func (s *Service) resolve(ctx context.Context, model string) (domain.Baseline, error) {
	profiles, err := s.repo.List(ctx)
	if err != nil {
		return domain.Baseline{}, fmt.Errorf("mechanism service: list profiles: %w", err)
	}
	best := domain.Profile{}
	found := false
	for _, p := range profiles {
		if p.Status != domain.ProfileStatusActive || !p.Matches(model) {
			continue
		}
		// 最具体的族前缀胜出（len 越大越具体）。
		if !found || len(p.Matcher.FamilyPrefixes[0]) > len(best.Matcher.FamilyPrefixes[0]) {
			best = p
			found = true
		}
	}
	if !found {
		return domain.DefaultBaseline(), nil
	}
	return best.Baseline, nil
}

// ListProfiles 返回全部档案。
func (s *Service) ListProfiles(ctx context.Context) ([]domain.Profile, error) {
	profiles, err := s.repo.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("mechanism service: list: %w", err)
	}
	return profiles, nil
}

// UpsertProfile 校验并写入档案（族键冲突升级版本）。
func (s *Service) UpsertProfile(ctx context.Context, p domain.Profile, updatedBy string) error {
	if updatedBy == "" {
		updatedBy = "api"
	}
	if p.Status == "" {
		p.Status = domain.ProfileStatusDraft
	}
	if err := p.Validate(); err != nil {
		return err
	}
	if p.ID == "" {
		p.ID = uuid.Must(uuid.NewV7()).String()
	}
	// 首版从 1 开始；族键冲突时 SQL 层 version+1 覆盖本值，互不干扰。
	if p.Version == 0 {
		p.Version = 1
	}
	p.Fingerprint = ComputeFingerprint(p)
	p.CreatedBy = updatedBy
	if err := s.repo.Upsert(ctx, p); err != nil {
		return err
	}
	// 写入成功后失效缓存；清空比逐键删除简单且行数级成本可忽略。
	s.mu.Lock()
	s.cache = make(map[string]domain.Baseline)
	s.mu.Unlock()
	return nil
}

// GetByFamilyKey 按族键读取档案。
func (s *Service) GetByFamilyKey(ctx context.Context, familyKey string) (domain.Profile, error) {
	p, ok, err := s.repo.GetByFamilyKey(ctx, familyKey)
	if err != nil {
		return domain.Profile{}, err
	}
	if !ok {
		return domain.Profile{}, ErrProfileNotFound
	}
	return p, nil
}

// ComputeFingerprint 计算档案指纹：matcher + baseline 的稳定哈希。
// 模型升级/基线变更导致指纹变化，触发重评测（阶段 3）。
func ComputeFingerprint(p domain.Profile) string {
	sort.Strings(p.Matcher.FamilyPrefixes)
	payload, err := json.Marshal(map[string]any{
		"matcher":  p.Matcher,
		"baseline": p.Baseline,
	})
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(payload)
	return fmt.Sprintf("%x", sum[:12])
}
