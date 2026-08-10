package config

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/nacos-group/nacos-sdk-go/v2/clients"
	"github.com/nacos-group/nacos-sdk-go/v2/clients/config_client"
	"github.com/nacos-group/nacos-sdk-go/v2/common/constant"
	"github.com/nacos-group/nacos-sdk-go/v2/vo"

	"github.com/byteBuilderX/stratum/pkg/constants"
)

// Nacos dataId 约定：按业务域拆分，变更粒度小、审计清晰。
const (
	nacosGroup        = "DEFAULT_GROUP"
	nacosAuthDataID   = "stratum/auth"
	nacosMemoryDataID = "stratum/memory"
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
	cc, err := clients.NewConfigClient(vo.NacosClientParam{
		ClientConfig: &constant.ClientConfig{
			NamespaceId:         s.Namespace,
			Username:            s.Username,
			Password:            s.Password,
			TimeoutMs:           uint64(constants.NacosTimeoutMs),
			LogLevel:            "warn",
			NotLoadCacheAtStart: true,
		},
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
