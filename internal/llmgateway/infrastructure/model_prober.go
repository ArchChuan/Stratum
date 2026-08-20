package infrastructure

import (
	"context"
	"sync"
	"time"

	"github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	"github.com/byteBuilderX/stratum/internal/llmgateway/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"go.uber.org/zap"
)

// modelProbeConcurrency 是单轮探活的最大并发模型数：有界并发，避免大量
// enabled 模型同时探活冲击 provider。
const modelProbeConcurrency = 8

// ModelProber 后台周期探活已启用模型，驱动 HealthRegistry 的主动信号，
// 覆盖空闲模型无业务调用信号的空窗。平台级（全局目录），无租户维度；
// 探活目标由 modelRepo 的 enabled 列表决定。
type ModelProber struct {
	modelRepo    port.ModelRepository
	providerRepo port.ProviderRepository
	chatProtos   map[domain.ProviderKind]ChatProtocol
	embedProtos  map[domain.ProviderKind]EmbedProtocol
	health       *HealthRegistry
	interval     time.Duration
	probeTimeout time.Duration
	logger       *zap.Logger

	stopCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

// NewModelProber 构造探活 worker。chatProtos/embedProtos 与 ModelRegistry
// 共享同一组协议实例，保证熔断器与健康状态机口径一致。
func NewModelProber(
	modelRepo port.ModelRepository,
	providerRepo port.ProviderRepository,
	chatProtos map[domain.ProviderKind]ChatProtocol,
	embedProtos map[domain.ProviderKind]EmbedProtocol,
	health *HealthRegistry,
	logger *zap.Logger,
) *ModelProber {
	return &ModelProber{
		modelRepo:    modelRepo,
		providerRepo: providerRepo,
		chatProtos:   chatProtos,
		embedProtos:  embedProtos,
		health:       health,
		interval:     constants.ModelProbeInterval,
		probeTimeout: constants.ModelProbeTimeout,
		logger:       logger,
		stopCh:       make(chan struct{}),
	}
}

// WithInterval 覆盖探活周期（测试注入短周期）。
func (p *ModelProber) WithInterval(d time.Duration) *ModelProber {
	p.interval = d
	return p
}

// WithProbeTimeout 覆盖单模型探活超时（测试注入短超时）。
func (p *ModelProber) WithProbeTimeout(d time.Duration) *ModelProber {
	p.probeTimeout = d
	return p
}

// Start 启动后台探活循环；首次立即探活一轮，之后按 interval 周期执行。
func (p *ModelProber) Start(ctx context.Context) {
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		p.run(ctx)
	}()
}

// Stop 关闭探活循环并等待 goroutine 退出。
func (p *ModelProber) Stop() {
	p.stopOnce.Do(func() { close(p.stopCh) })
	p.wg.Wait()
}

func (p *ModelProber) run(ctx context.Context) {
	p.ProbeOnce(ctx)
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-p.stopCh:
			return
		case <-ticker.C:
			p.ProbeOnce(ctx)
		}
	}
}

// ProbeOnce 遍历 enabled 模型，对 AllowProbe 放行的模型并发探活一轮。
// Start 的首轮与周期循环复用此方法；导出供测试单轮驱动确定性断言。
func (p *ModelProber) ProbeOnce(ctx context.Context) {
	if p.health == nil {
		return
	}
	enabled := true
	models, err := p.modelRepo.List(ctx, port.ModelFilter{Enabled: &enabled})
	if err != nil {
		p.logger.Warn("llmgateway.model_prober.list_failed", zap.Error(err))
		return
	}
	sem := make(chan struct{}, modelProbeConcurrency)
	var wg sync.WaitGroup
	for _, m := range models {
		if !p.health.AllowProbe(m.Name) {
			continue
		}
		wg.Add(1)
		go func(m domain.Model) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			p.probeModel(ctx, m)
		}(m)
	}
	wg.Wait()
}

// probeModel 按模型能力分派探活：chat 模型走 ChatProtocol.Health（轻量
// complete），仅 embedding 的模型走 EmbedProtocol.CreateEmbeddings 最小请求。
// 两者内部都走 per-model 熔断器：breaker open 时探活被短路，待恢复窗口
// 半开放行真正探测，与被动熔断状态机同步演进。结果经 RecordProbe 写回
// HealthRegistry（错误已脱敏）。
func (p *ModelProber) probeModel(ctx context.Context, m domain.Model) {
	provider, err := p.providerRepo.Get(ctx, m.ProviderID)
	if err != nil || !provider.Enabled {
		return
	}
	cfg := ProviderConfig{
		Name:        provider.Name,
		BaseURL:     provider.BaseURL,
		APIKey:      provider.APIKey,
		HealthModel: m.Name,
	}
	probeCtx, cancel := context.WithTimeout(ctx, p.probeTimeout)
	defer cancel()

	var probeErr error
	switch {
	case hasCapability(m, domain.CapChat):
		if proto, ok := p.chatProtos[provider.Kind]; ok {
			probeErr = proto.Health(probeCtx, cfg)
		}
	case hasCapability(m, domain.CapEmbedding):
		if proto, ok := p.embedProtos[provider.Kind]; ok {
			_, probeErr = proto.CreateEmbeddings(probeCtx, cfg, &EmbeddingRequest{Model: m.Name, Input: []string{"ping"}})
		}
	default:
		return
	}
	if probeErr != nil {
		p.logger.Warn("llmgateway.model_prober.probe_failed",
			zap.String("model", m.Name),
			zap.String("provider", provider.Name),
			zap.String("capability", string(m.Capabilities[0])),
			zap.Error(probeErr))
	}
	p.health.RecordProbe(m.Name, probeErr == nil, probeErr)
}

func hasCapability(m domain.Model, cap domain.ModelCapability) bool {
	for _, c := range m.Capabilities {
		if c == cap {
			return true
		}
	}
	return false
}
