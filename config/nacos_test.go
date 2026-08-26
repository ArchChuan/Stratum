package config

import (
	"os"
	"testing"
	"time"

	"go.uber.org/zap"
)

func TestNacosSettingsServerAddresses(t *testing.T) {
	cases := []struct {
		name   string
		url    string
		host   string
		port   uint64
		scheme string
	}{
		{name: "http with port", url: "http://nacos:8848", host: "nacos", port: 8848, scheme: "http"},
		{name: "no scheme defaults http", url: "nacos:8848", host: "nacos", port: 8848, scheme: "http"},
		{name: "host only defaults 8848", url: "nacos", host: "nacos", port: 8848, scheme: "http"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := NacosSettings{URL: tc.url}
			host, port, scheme, err := s.ServerAddresses()
			if err != nil {
				t.Fatalf("ServerAddresses() error: %v", err)
			}
			if host != tc.host || port != tc.port || scheme != tc.scheme {
				t.Fatalf("got %s:%d(%s), want %s:%d(%s)", host, port, scheme, tc.host, tc.port, tc.scheme)
			}
		})
	}
}

func TestNacosSettingsServerAddressesInvalid(t *testing.T) {
	s := NacosSettings{URL: "http://host:notaport"}
	if _, _, _, err := s.ServerAddresses(); err == nil {
		t.Fatal("expected error for invalid port")
	}
}

func TestNacosClientConfigLogDirWritableTemp(t *testing.T) {
	cfg := nacosClientConfig(NacosSettings{Namespace: "ns", Username: "u", Password: "p"})
	// nacos-sdk 默认 LogDir = <cwd>/log，容器内 appuser 无权限创建 /app/log；
	// 必须显式指向系统临时目录，否则启动期报 permission denied。
	if cfg.LogDir != os.TempDir() {
		t.Fatalf("LogDir = %q, want %q", cfg.LogDir, os.TempDir())
	}
	if cfg.NamespaceId != "ns" || cfg.Username != "u" || cfg.Password != "p" {
		t.Fatalf("settings not forwarded to client config: %+v", cfg)
	}
	if !cfg.NotLoadCacheAtStart {
		t.Fatal("NotLoadCacheAtStart must stay true")
	}
}

// fakeNacosClient 用于 config 包内测试，不依赖真实 Nacos。
type fakeNacosClient struct {
	contents  map[string]string
	listeners map[string]func(string)
	err       error
}

func (f *fakeNacosClient) GetConfig(dataID string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return f.contents[dataID], nil
}

func (f *fakeNacosClient) Listen(dataID string, onChange func(string)) error {
	if f.err != nil {
		return f.err
	}
	f.listeners[dataID] = onChange
	return nil
}

func (f *fakeNacosClient) Close() error { return nil }

func TestConnectNacosAppliesColdAndHotConfig(t *testing.T) {
	fake := &fakeNacosClient{
		contents: map[string]string{
			nacosAuthDataID:   `{"password_auth_enabled": false, "guest_auth_enabled": true}`,
			nacosMemoryDataID: `{"enabled": true, "poll_interval": "5s", "batch_size": 100}`,
		},
		listeners: map[string]func(string){},
	}
	// brief 遗漏 NacosURL 导致 ConnectNacos 直接跳过（fail-closed 设计）；
	// 补上 URL 才能走冷生效覆盖路径，断言未改动。
	cfg := &Config{NacosURL: "http://nacos:8848", PasswordAuthEnabled: true, GuestAuthEnabled: false}
	newNacosClient = func(s NacosSettings) (nacosClient, error) { return fake, nil }
	defer func() { newNacosClient = newNacosClientImpl }()

	logger := zap.NewNop()
	if err := cfg.ConnectNacos(logger); err != nil {
		t.Fatalf("ConnectNacos() error: %v", err)
	}
	// 冷生效字段（启动拉取）
	if cfg.PasswordAuthEnabled {
		t.Fatal("PasswordAuthEnabled should be false from Nacos")
	}
	if !cfg.GuestAuthEnabled {
		t.Fatal("GuestAuthEnabled should be true from Nacos")
	}
	if !cfg.MemoryPipeline.Enabled {
		t.Fatal("MemoryPipeline.Enabled should be true from Nacos")
	}
	// 热更新字段（初始拉取也写入 dynamic）
	if d := cfg.LoadMemoryPipelineDynamic(); d.PollInterval != 5*time.Second || d.BatchSize != 100 {
		t.Fatalf("dynamic = %+v, want 5s/100", d)
	}
	// 热更新推送
	cfg.applyMemoryConfig(`{"poll_interval": "10s", "batch_size": 200}`)
	if d := cfg.LoadMemoryPipelineDynamic(); d.PollInterval != 10*time.Second || d.BatchSize != 200 {
		t.Fatalf("dynamic after push = %+v, want 10s/200", d)
	}
}

func TestConnectNacosSkippedWhenNotConfigured(t *testing.T) {
	cfg := &Config{} // NacosURL 为空
	if err := cfg.ConnectNacos(zap.NewNop()); err != nil {
		t.Fatalf("ConnectNacos() = %v, want nil skip", err)
	}
}

func TestApplyMemoryConfigInvalidJSONKeepsOldValue(t *testing.T) {
	cfg := &Config{}
	cfg.ApplyMemoryPipelineDynamic(MemoryPipelineDynamic{PollInterval: time.Second})
	if err := cfg.applyMemoryConfig(`{not json`); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	if d := cfg.LoadMemoryPipelineDynamic(); d.PollInterval != time.Second {
		t.Fatalf("dynamic lost after invalid push: %+v", d)
	}
}

func TestApplyMemoryConfigInvalidDurationKeepsOldValue(t *testing.T) {
	cfg := &Config{}
	cfg.ApplyMemoryPipelineDynamic(MemoryPipelineDynamic{PollInterval: time.Second, BatchSize: 10})
	if err := cfg.applyMemoryConfig(`{"poll_interval": "5x", "batch_size": 20}`); err == nil {
		t.Fatal("expected error for invalid duration")
	}
	// 原子性：整体回退，batch_size 也不应用
	if d := cfg.LoadMemoryPipelineDynamic(); d.BatchSize != 10 {
		t.Fatalf("partial apply detected: %+v", d)
	}
}

func TestApplyMemoryConfigMissingDynamicFieldsDontClear(t *testing.T) {
	cfg := &Config{}
	cfg.ApplyMemoryPipelineDynamic(MemoryPipelineDynamic{PollInterval: 3 * time.Second, BatchSize: 50})
	// 只改 enabled，不提供调度字段 → 动态值保留
	if err := cfg.applyMemoryConfig(`{"enabled": true}`); err != nil {
		t.Fatalf("applyMemoryConfig() error: %v", err)
	}
	if d := cfg.LoadMemoryPipelineDynamic(); d.PollInterval != 3*time.Second || d.BatchSize != 50 {
		t.Fatalf("dynamic cleared by partial push: %+v", d)
	}
}

func TestConnectNacosListenerHotPushUpdatesDynamicOnly(t *testing.T) {
	fake := &fakeNacosClient{
		contents: map[string]string{
			nacosAuthDataID:   `{"password_auth_enabled": true}`,
			nacosMemoryDataID: `{"enabled": true, "poll_interval": "5s", "batch_size": 100}`,
		},
		listeners: map[string]func(string){},
	}
	cfg := &Config{NacosURL: "http://nacos:8848", MemoryPipeline: MemoryPipelineConfig{Enabled: false}}
	newNacosClient = func(s NacosSettings) (nacosClient, error) { return fake, nil }
	defer func() { newNacosClient = newNacosClientImpl }()

	if err := cfg.ConnectNacos(zap.NewNop()); err != nil {
		t.Fatalf("ConnectNacos() error: %v", err)
	}
	// 同步拉取阶段：冷生效字段已写入
	if !cfg.MemoryPipeline.Enabled {
		t.Fatal("MemoryPipeline.Enabled should be true from sync pull")
	}
	// auth 全部为冷生效字段，不得注册热更新 listener
	if _, ok := fake.listeners[nacosAuthDataID]; ok {
		t.Fatal("auth dataId must not register a hot listener (all cold fields)")
	}
	memoryPush, ok := fake.listeners[nacosMemoryDataID]
	if !ok {
		t.Fatal("memory dataId must register a hot listener")
	}
	// 热推送：enabled=false 是冷生效字段，回调不得写；调度字段热生效
	memoryPush(`{"enabled": false, "poll_interval": "10s", "batch_size": 200}`)
	if !cfg.MemoryPipeline.Enabled {
		t.Fatal("listener callback must not write cold-enabled field MemoryPipeline.Enabled")
	}
	if d := cfg.LoadMemoryPipelineDynamic(); d.PollInterval != 10*time.Second || d.BatchSize != 200 {
		t.Fatalf("dynamic after hot push = %+v, want 10s/200", d)
	}
}

func TestConnectNacosListenerInvalidPushKeepsOldDynamic(t *testing.T) {
	fake := &fakeNacosClient{
		contents: map[string]string{
			nacosMemoryDataID: `{"enabled": true, "poll_interval": "5s", "batch_size": 100}`,
		},
		listeners: map[string]func(string){},
	}
	cfg := &Config{NacosURL: "http://nacos:8848"}
	newNacosClient = func(s NacosSettings) (nacosClient, error) { return fake, nil }
	defer func() { newNacosClient = newNacosClientImpl }()

	if err := cfg.ConnectNacos(zap.NewNop()); err != nil {
		t.Fatalf("ConnectNacos() error: %v", err)
	}
	// 非法动态推送：回调不 panic（WARN 语义），旧动态整体保留
	fake.listeners[nacosMemoryDataID](`{"poll_interval": "5x", "batch_size": 20}`)
	if d := cfg.LoadMemoryPipelineDynamic(); d.PollInterval != 5*time.Second || d.BatchSize != 100 {
		t.Fatalf("dynamic after invalid hot push = %+v, want 5s/100", d)
	}
}

func TestApplyAuthConfigMissingFieldsDontOverride(t *testing.T) {
	cfg := &Config{PasswordAuthEnabled: true}
	// 字段缺省（JSON 无 password_auth_enabled）→ 保持 env 值
	if err := cfg.applyAuthConfig(`{"guest_auth_enabled": false}`); err != nil {
		t.Fatalf("applyAuthConfig() error: %v", err)
	}
	if !cfg.PasswordAuthEnabled {
		t.Fatal("missing field should not override existing value")
	}
	if cfg.GuestAuthEnabled {
		t.Fatal("guest_auth_enabled should be false")
	}
}
