package config

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/nacos-group/nacos-sdk-go/v2/clients"
	"github.com/nacos-group/nacos-sdk-go/v2/clients/config_client"
	"github.com/nacos-group/nacos-sdk-go/v2/common/constant"
	"github.com/nacos-group/nacos-sdk-go/v2/vo"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/pkg/constants"
)

// Nacos dataId 约定：按业务域拆分，变更粒度小、审计清晰。
// 命名禁止含 /（Nacos 2.4+ 服务端校验 dataId 非法字符，stratum-auth 会 400）。
const (
	nacosGroup        = "DEFAULT_GROUP"
	nacosAuthDataID   = "stratum-auth"
	nacosMemoryDataID = "stratum-memory"
)

// nacosClient 抽象 Nacos 配置客户端，便于测试注入。
type nacosClient interface {
	GetConfig(dataID string) (string, error)
	Listen(dataID string, onChange func(content string)) error
	Close() error
}

type NacosSettings struct {
	URL       string
	Namespace string
	Username  string
	Password  string
}

// ServerAddresses 解析 URL（如 http://nacos:8848）为 SDK 需要的 host/port/scheme。
// 无 scheme 视为 http；无端口默认 8848。
func (s NacosSettings) ServerAddresses() (host string, port uint64, scheme string, err error) {
	raw := s.URL
	if !containsScheme(raw) {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", 0, "", fmt.Errorf("nacos url parse: %w", err)
	}
	if u.Hostname() == "" {
		return "", 0, "", fmt.Errorf("nacos url missing host: %s", s.URL)
	}
	scheme = u.Scheme
	if scheme != "http" && scheme != "https" {
		return "", 0, "", fmt.Errorf("nacos url scheme must be http/https: %s", s.URL)
	}
	host = u.Hostname()
	port = 8848
	if p := u.Port(); p != "" {
		parsed, perr := strconv.ParseUint(p, 10, 16)
		if perr != nil {
			return "", 0, "", fmt.Errorf("nacos url port: %w", perr)
		}
		port = parsed
	}
	return host, port, scheme, nil
}

func containsScheme(raw string) bool {
	return strings.Contains(raw, "://")
}

// nacosClientConfig 构造 SDK 客户端配置。LogDir 显式指向系统临时目录：
// nacos-sdk 默认 LogDir = 当前工作目录 + /log，容器内 WORKDIR 是 /app，
// appuser 无权限 mkdir /app/log → 启动期报 permission denied。
func nacosClientConfig(s NacosSettings) constant.ClientConfig {
	return constant.ClientConfig{
		NamespaceId:         s.Namespace,
		Username:            s.Username,
		Password:            s.Password,
		TimeoutMs:           uint64(constants.NacosTimeoutMs),
		LogLevel:            "warn",
		LogDir:              os.TempDir(),
		NotLoadCacheAtStart: true,
	}
}

type sdkNacosClient struct {
	cc config_client.IConfigClient
}

// newNacosClient 构造 Nacos 客户端；包级变量便于测试注入 fake（Task 4）。
var newNacosClient = newNacosClientImpl

func newNacosClientImpl(s NacosSettings) (nacosClient, error) {
	host, port, scheme, err := s.ServerAddresses()
	if err != nil {
		return nil, err
	}
	cfg := nacosClientConfig(s)
	cc, err := clients.NewConfigClient(vo.NacosClientParam{
		ClientConfig: &cfg,
		ServerConfigs: []constant.ServerConfig{
			{IpAddr: host, Port: port, Scheme: scheme},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("nacos client: %w", err)
	}
	return &sdkNacosClient{cc: cc}, nil
}

func (c *sdkNacosClient) GetConfig(dataID string) (string, error) {
	content, err := c.cc.GetConfig(vo.ConfigParam{DataId: dataID, Group: nacosGroup})
	if err != nil {
		return "", fmt.Errorf("nacos get %s: %w", dataID, err)
	}
	return content, nil
}

func (c *sdkNacosClient) Listen(dataID string, onChange func(string)) error {
	if err := c.cc.ListenConfig(vo.ConfigParam{
		DataId:   dataID,
		Group:    nacosGroup,
		OnChange: func(namespace, group, dataId, data string) { onChange(data) },
	}); err != nil {
		return fmt.Errorf("nacos listen %s: %w", dataID, err)
	}
	return nil
}

func (c *sdkNacosClient) Close() error {
	c.cc.CloseClient()
	return nil
}

// ConnectNacos 建立 Nacos 连接并应用档位 A 配置。
// 语义（fail-closed）：未配置 NacosURL → 返回 nil 不启用；
// 连接失败 → 返回 error（调用方 WARN，用 env/默认值启动）；
// 单个 dataId 拉取/解析失败 → WARN 跳过，不阻断其余 dataId；
// 非法内容 → 整体回退旧值。
// 冷生效字段（装配参数）在同步阶段应用，装配期一次读取；
// 热更新 listener 只注册有热生效字段的 dataId（memory 调度字段），
// 回调只走 applyMemoryConfigDynamic，不写任何冷生效普通字段。
func (c *Config) ConnectNacos(logger *zap.Logger) error {
	if c.NacosURL == "" {
		return nil
	}
	client, err := newNacosClient(NacosSettings{
		URL: c.NacosURL, Namespace: c.NacosNamespace,
		Username: c.NacosUsername, Password: c.NacosPassword,
	})
	if err != nil {
		return fmt.Errorf("config nacos connect: %w", err)
	}
	c.nacos = client

	// 同步拉取：全量应用（冷生效普通字段 + 热生效调度字段）。
	for dataID, apply := range map[string]func(string) error{
		nacosAuthDataID:   c.applyAuthConfig,
		nacosMemoryDataID: c.applyMemoryConfig,
	} {
		content, err := client.GetConfig(dataID)
		if err != nil {
			logger.Warn("config: nacos get failed, using env/fallback",
				zap.String("data_id", dataID), zap.Error(err))
			continue
		}
		if err := apply(content); err != nil {
			logger.Warn("config: nacos apply failed, keeping previous value",
				zap.String("data_id", dataID), zap.Error(err))
		}
	}

	// 热更新 listener：只注册 memory（stratum-memory 的 poll_interval/batch_size
	// 是热生效字段）。auth（stratum-auth）全部为冷生效字段，回调写普通字段会与
	// 启动期装配读（wiring memory.go/router.go）构成 data race 且无语义价值，
	// 故不注册——修改 stratum-auth 后重启生效。
	if err := client.Listen(nacosMemoryDataID, func(content string) {
		if err := c.applyMemoryConfigDynamic(content); err != nil {
			logger.Warn("config: nacos push rejected, keeping previous value",
				zap.String("data_id", nacosMemoryDataID), zap.Error(err))
		}
	}); err != nil {
		logger.Warn("config: nacos listen failed, hot reload disabled for this dataId",
			zap.String("data_id", nacosMemoryDataID), zap.Error(err))
	}
	return nil
}

// CloseNacos 关闭 Nacos 连接。幂等。
func (c *Config) CloseNacos() error {
	if c.nacos == nil {
		return nil
	}
	if err := c.nacos.Close(); err != nil {
		return fmt.Errorf("config nacos close: %w", err)
	}
	c.nacos = nil
	return nil
}

// applyAuthConfig 应用 stratum-auth dataId。
// 字段缺省不覆盖（*bool 指针区分"未设置"与"显式 false"）。
func (c *Config) applyAuthConfig(content string) error {
	var d struct {
		PasswordAuthEnabled *bool `json:"password_auth_enabled"`
		GuestAuthEnabled    *bool `json:"guest_auth_enabled"`
	}
	if err := json.Unmarshal([]byte(content), &d); err != nil {
		return fmt.Errorf("parse auth config: %w", err)
	}
	if d.PasswordAuthEnabled != nil {
		c.PasswordAuthEnabled = *d.PasswordAuthEnabled
	}
	if d.GuestAuthEnabled != nil {
		c.GuestAuthEnabled = *d.GuestAuthEnabled
	}
	return nil
}

// applyMemoryConfig 应用 stratum-memory dataId（同步拉取路径）。
// enabled 等装配参数为冷生效（写入字段，下次启动生效）；
// poll_interval/batch_size 为热生效（原子写入 dynamic 并通知 listener）。
// 任一字段非法 → 整体回退（不部分应用，解析失败不写任何字段）。
func (c *Config) applyMemoryConfig(content string) error {
	var d struct {
		Enabled      *bool  `json:"enabled"`
		PollInterval string `json:"poll_interval"`
		BatchSize    *int   `json:"batch_size"`
	}
	if err := json.Unmarshal([]byte(content), &d); err != nil {
		return fmt.Errorf("parse memory config: %w", err)
	}
	dynamic, err := memoryDynamicFrom(d.PollInterval, d.BatchSize)
	if err != nil {
		return err
	}
	if d.Enabled != nil {
		c.MemoryPipeline.Enabled = *d.Enabled
	}
	c.applyMemoryDynamic(dynamic)
	return nil
}

// applyMemoryConfigDynamic 只应用热生效调度字段（poll_interval/batch_size），
// 供 Nacos listener 回调使用。不写冷生效普通字段（enabled），避免回调
// goroutine 与启动期装配读构成 data race。任一字段非法 → 整体回退（不部分应用）。
func (c *Config) applyMemoryConfigDynamic(content string) error {
	var d struct {
		PollInterval string `json:"poll_interval"`
		BatchSize    *int   `json:"batch_size"`
	}
	if err := json.Unmarshal([]byte(content), &d); err != nil {
		return fmt.Errorf("parse memory config: %w", err)
	}
	dynamic, err := memoryDynamicFrom(d.PollInterval, d.BatchSize)
	if err != nil {
		return err
	}
	c.applyMemoryDynamic(dynamic)
	return nil
}

// memoryDynamicFrom 解析热生效调度字段；任一字段非法 → error。
func memoryDynamicFrom(pollInterval string, batchSize *int) (MemoryPipelineDynamic, error) {
	dynamic := MemoryPipelineDynamic{}
	if pollInterval != "" {
		parsed, err := time.ParseDuration(pollInterval)
		if err != nil {
			return MemoryPipelineDynamic{}, fmt.Errorf("parse poll_interval: %w", err)
		}
		dynamic.PollInterval = parsed
	}
	if batchSize != nil {
		dynamic.BatchSize = *batchSize
	}
	return dynamic, nil
}

// applyMemoryDynamic 只在显式提供了任一调度字段时推送动态值，
// 缺省不覆盖（防止只改其他字段时清零动态值）。
func (c *Config) applyMemoryDynamic(dynamic MemoryPipelineDynamic) {
	if dynamic != (MemoryPipelineDynamic{}) {
		c.ApplyMemoryPipelineDynamic(dynamic)
	}
}
