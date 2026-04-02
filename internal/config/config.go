// Package config 配置模型 - 参考 src/model/config.rs
package config

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
)

// systemVersions 可选的系统版本列表
var systemVersions = []string{"darwin#24.6.0", "win32#10.0.22631"}

// randomSystemVersion 随机选择一个系统版本
func randomSystemVersion() string {
	return systemVersions[rand.Intn(len(systemVersions))]
}

// Config 服务器配置结构体
type Config struct {
	Host                     string  `json:"host"`
	Port                     int     `json:"port"`
	Region                   string  `json:"region"`
	AuthRegion               *string `json:"authRegion,omitempty"`
	ApiRegion                *string `json:"apiRegion,omitempty"`
	KiroVersion              string  `json:"kiroVersion"`
	MachineID                *string `json:"machineId,omitempty"`
	ApiKey                   *string `json:"apiKey,omitempty"`
	SystemVersion            string  `json:"systemVersion"`
	NodeVersion              string  `json:"nodeVersion"`
	TlsBackend               string  `json:"tlsBackend"`
	CountTokensApiUrl        *string `json:"countTokensApiUrl,omitempty"`
	CountTokensApiKey        *string `json:"countTokensApiKey,omitempty"`
	CountTokensAuthType      string  `json:"countTokensAuthType"`
	RequestMaxBytes          int     `json:"requestMaxBytes"`
	RequestMaxChars          int     `json:"requestMaxChars"`
	RequestContextTokenLimit int     `json:"requestContextTokenLimit"`
	StreamPingIntervalSecs   int     `json:"streamPingIntervalSecs"`
	StreamMaxIdlePings       int     `json:"streamMaxIdlePings"`
	StreamIdleWarnAfterPings int     `json:"streamIdleWarnAfterPings"`
	ToolResultCurrentMaxChars int    `json:"toolResultCurrentMaxChars"`
	ToolResultCurrentMaxLines int    `json:"toolResultCurrentMaxLines"`
	ToolResultHistoryMaxChars int    `json:"toolResultHistoryMaxChars"`
	ToolResultHistoryMaxLines int    `json:"toolResultHistoryMaxLines"`
	ProxyUrl                 *string `json:"proxyUrl,omitempty"`
	ProxyUsername            *string `json:"proxyUsername,omitempty"`
	ProxyPassword            *string `json:"proxyPassword,omitempty"`
	AdminApiKey              *string `json:"adminApiKey,omitempty"`
	LoadBalancingMode        string  `json:"loadBalancingMode"`

	// ConfigPath 配置文件路径，不参与 JSON 序列化
	ConfigPath string `json:"-"`
}

// DefaultConfig 返回带有默认值的配置
func DefaultConfig() *Config {
	return &Config{
		Host:                      "127.0.0.1",
		Port:                      8080,
		Region:                    "us-east-1",
		KiroVersion:               "0.10.0",
		SystemVersion:             randomSystemVersion(),
		NodeVersion:               "22.21.1",
		TlsBackend:                "rustls",
		CountTokensAuthType:       "x-api-key",
		RequestMaxBytes:           8 * 1024 * 1024,
		RequestMaxChars:           2_000_000,
		RequestContextTokenLimit:  184_000,
		StreamPingIntervalSecs:    15,
		StreamMaxIdlePings:        4,
		StreamIdleWarnAfterPings:  2,
		ToolResultCurrentMaxChars: 16_000,
		ToolResultCurrentMaxLines: 300,
		ToolResultHistoryMaxChars: 6_000,
		ToolResultHistoryMaxLines: 120,
		LoadBalancingMode:         "priority",
	}
}

// DefaultConfigPath 返回默认配置文件路径
func DefaultConfigPath() string {
	return "config/config.json"
}

// GetMachineID 返回配置的 machineId（实现 machineid.ConfigProvider 接口）
func (c *Config) GetMachineID() *string {
	return c.MachineID
}

// EffectiveAuthRegion 返回有效的认证区域，优先使用 AuthRegion，否则回退到 Region
func (c *Config) EffectiveAuthRegion() string {
	if c.AuthRegion != nil {
		return *c.AuthRegion
	}
	return c.Region
}

// EffectiveApiRegion 返回有效的 API 区域，优先使用 ApiRegion，否则回退到 Region
func (c *Config) EffectiveApiRegion() string {
	if c.ApiRegion != nil {
		return *c.ApiRegion
	}
	return c.Region
}

// Load 从 JSON 文件加载配置，缺失字段使用默认值
func Load(path string) (*Config, error) {
	cfg := DefaultConfig()
	cfg.ConfigPath = path

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// 配置文件不存在，返回默认配置
			return cfg, nil
		}
		return nil, fmt.Errorf("读取配置文件失败: %w", err)
	}

	// 解析 JSON 到临时 map，逐字段覆盖默认值
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("解析配置文件 JSON 失败: %w", err)
	}

	// 直接反序列化到配置结构体，encoding/json 只覆盖 JSON 中存在的字段
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("映射配置字段失败: %w", err)
	}

	return cfg, nil
}

// Save 将配置保存到文件
func (c *Config) Save() error {
	if c.ConfigPath == "" {
		return fmt.Errorf("配置文件路径未知，无法保存配置")
	}

	data, err := json.MarshalIndent(c.ToDict(), "", "  ")
	if err != nil {
		return fmt.Errorf("序列化配置失败: %w", err)
	}

	if err := os.WriteFile(c.ConfigPath, data, 0644); err != nil {
		return fmt.Errorf("写入配置文件失败: %w", err)
	}

	return nil
}

// ToDict 将配置转换为 map，省略值为 nil 的可选字段
func (c *Config) ToDict() map[string]interface{} {
	result := map[string]interface{}{
		"host":                      c.Host,
		"port":                      c.Port,
		"region":                    c.Region,
		"kiroVersion":               c.KiroVersion,
		"systemVersion":             c.SystemVersion,
		"nodeVersion":               c.NodeVersion,
		"tlsBackend":                c.TlsBackend,
		"countTokensAuthType":       c.CountTokensAuthType,
		"requestMaxBytes":           c.RequestMaxBytes,
		"requestMaxChars":           c.RequestMaxChars,
		"requestContextTokenLimit":  c.RequestContextTokenLimit,
		"streamPingIntervalSecs":    c.StreamPingIntervalSecs,
		"streamMaxIdlePings":        c.StreamMaxIdlePings,
		"streamIdleWarnAfterPings":  c.StreamIdleWarnAfterPings,
		"toolResultCurrentMaxChars": c.ToolResultCurrentMaxChars,
		"toolResultCurrentMaxLines": c.ToolResultCurrentMaxLines,
		"toolResultHistoryMaxChars": c.ToolResultHistoryMaxChars,
		"toolResultHistoryMaxLines": c.ToolResultHistoryMaxLines,
		"loadBalancingMode":         c.LoadBalancingMode,
	}

	// 可选字段：仅在非 nil 时写入
	if c.AuthRegion != nil {
		result["authRegion"] = *c.AuthRegion
	}
	if c.ApiRegion != nil {
		result["apiRegion"] = *c.ApiRegion
	}
	if c.MachineID != nil {
		result["machineId"] = *c.MachineID
	}
	if c.ApiKey != nil {
		result["apiKey"] = *c.ApiKey
	}
	if c.CountTokensApiUrl != nil {
		result["countTokensApiUrl"] = *c.CountTokensApiUrl
	}
	if c.CountTokensApiKey != nil {
		result["countTokensApiKey"] = *c.CountTokensApiKey
	}
	if c.ProxyUrl != nil {
		result["proxyUrl"] = *c.ProxyUrl
	}
	if c.ProxyUsername != nil {
		result["proxyUsername"] = *c.ProxyUsername
	}
	if c.ProxyPassword != nil {
		result["proxyPassword"] = *c.ProxyPassword
	}
	if c.AdminApiKey != nil {
		result["adminApiKey"] = *c.AdminApiKey
	}

	return result
}
