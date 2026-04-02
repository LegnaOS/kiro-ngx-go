package model

// Kiro OAuth 凭据模型 - 参考 src/kiro/model/credentials.rs

import (
	"encoding/json"
	"os"
	"sort"
	"strings"

	"kiro-proxy/internal/httpclient"
)

// RegionConfig 区域配置接口，用于获取有效的认证和 API 区域
type RegionConfig interface {
	EffectiveAuthRegion() string
	EffectiveApiRegion() string
}

// KiroCredentials Kiro 凭据
type KiroCredentials struct {
	ID                     *int     `json:"id,omitempty"`
	AccessToken            *string  `json:"accessToken,omitempty"`
	RefreshToken           *string  `json:"refreshToken,omitempty"`
	ProfileArn             *string  `json:"profileArn,omitempty"`
	ExpiresAt              *string  `json:"expiresAt,omitempty"`
	AuthMethod             *string  `json:"authMethod,omitempty"`
	ClientID               *string  `json:"clientId,omitempty"`
	ClientSecret           *string  `json:"clientSecret,omitempty"`
	Priority               int      `json:"priority,omitempty"`
	Region                 *string  `json:"region,omitempty"`
	AuthRegion             *string  `json:"authRegion,omitempty"`
	ApiRegion              *string  `json:"apiRegion,omitempty"`
	MachineID              *string  `json:"machineId,omitempty"`
	Email                  *string  `json:"email,omitempty"`
	SubscriptionTitle      *string  `json:"subscriptionTitle,omitempty"`
	BalanceCurrentUsage    *float64 `json:"balanceCurrentUsage,omitempty"`
	BalanceUsageLimit      *float64 `json:"balanceUsageLimit,omitempty"`
	BalanceRemaining       *float64 `json:"balanceRemaining,omitempty"`
	BalanceUsagePercentage *float64 `json:"balanceUsagePercentage,omitempty"`
	BalanceNextResetAt     *float64 `json:"balanceNextResetAt,omitempty"`
	BalanceUpdatedAt       *string  `json:"balanceUpdatedAt,omitempty"`
	ProxyUrl               *string  `json:"proxyUrl,omitempty"`
	ProxyUsername          *string  `json:"proxyUsername,omitempty"`
	ProxyPassword          *string  `json:"proxyPassword,omitempty"`
	Disabled               bool     `json:"disabled"`
}

// CanonicalizeAuthMethod 规范化认证方式: builder-id/iam → idc
func (c *KiroCredentials) CanonicalizeAuthMethod() {
	if c.AuthMethod == nil {
		return
	}
	lower := strings.ToLower(*c.AuthMethod)
	if lower == "builder-id" || lower == "iam" {
		idc := "idc"
		c.AuthMethod = &idc
	}
}

// EffectiveAuthRegion 获取有效的 Auth Region
// 优先级: credential.auth_region > credential.region > config.auth_region > config.region
func (c *KiroCredentials) EffectiveAuthRegion(config RegionConfig) string {
	if c.AuthRegion != nil && *c.AuthRegion != "" {
		return *c.AuthRegion
	}
	if c.Region != nil && *c.Region != "" {
		return *c.Region
	}
	return config.EffectiveAuthRegion()
}

// EffectiveApiRegion 获取有效的 API Region
// 优先级: credential.api_region > config.api_region > config.region
func (c *KiroCredentials) EffectiveApiRegion(config RegionConfig) string {
	if c.ApiRegion != nil && *c.ApiRegion != "" {
		return *c.ApiRegion
	}
	return config.EffectiveApiRegion()
}

// EffectiveProxy 获取有效的代理配置
// 优先级: credential proxy > global proxy > nil
// "direct" 表示显式不使用代理
func (c *KiroCredentials) EffectiveProxy(globalProxy *httpclient.ProxyConfig) *httpclient.ProxyConfig {
	if c.ProxyUrl != nil && *c.ProxyUrl != "" {
		if strings.ToLower(*c.ProxyUrl) == "direct" {
			return nil
		}
		proxy := &httpclient.ProxyConfig{URL: *c.ProxyUrl}
		if c.ProxyUsername != nil && c.ProxyPassword != nil &&
			*c.ProxyUsername != "" && *c.ProxyPassword != "" {
			proxy.WithAuth(*c.ProxyUsername, *c.ProxyPassword)
		}
		return proxy
	}
	return globalProxy
}

// GetMachineID 返回凭据的 machineId（实现 machineid.CredentialProvider 接口）
func (c *KiroCredentials) GetMachineID() *string {
	return c.MachineID
}

// GetRefreshToken 返回凭据的 refreshToken（实现 machineid.CredentialProvider 接口）
func (c *KiroCredentials) GetRefreshToken() *string {
	return c.RefreshToken
}

// SupportsOpus FREE 账户不能使用 Opus 模型
func (c *KiroCredentials) SupportsOpus() bool {
	if c.SubscriptionTitle != nil {
		return !strings.Contains(strings.ToUpper(*c.SubscriptionTitle), "FREE")
	}
	return true
}

// Clone 深拷贝凭据
func (c *KiroCredentials) Clone() KiroCredentials {
	clone := *c
	// 深拷贝所有指针字段
	if c.ID != nil {
		v := *c.ID
		clone.ID = &v
	}
	if c.AccessToken != nil {
		v := *c.AccessToken
		clone.AccessToken = &v
	}
	if c.RefreshToken != nil {
		v := *c.RefreshToken
		clone.RefreshToken = &v
	}
	if c.ProfileArn != nil {
		v := *c.ProfileArn
		clone.ProfileArn = &v
	}
	if c.ExpiresAt != nil {
		v := *c.ExpiresAt
		clone.ExpiresAt = &v
	}
	if c.AuthMethod != nil {
		v := *c.AuthMethod
		clone.AuthMethod = &v
	}
	if c.ClientID != nil {
		v := *c.ClientID
		clone.ClientID = &v
	}
	if c.ClientSecret != nil {
		v := *c.ClientSecret
		clone.ClientSecret = &v
	}
	if c.Region != nil {
		v := *c.Region
		clone.Region = &v
	}
	if c.AuthRegion != nil {
		v := *c.AuthRegion
		clone.AuthRegion = &v
	}
	if c.ApiRegion != nil {
		v := *c.ApiRegion
		clone.ApiRegion = &v
	}
	if c.MachineID != nil {
		v := *c.MachineID
		clone.MachineID = &v
	}
	if c.Email != nil {
		v := *c.Email
		clone.Email = &v
	}
	if c.SubscriptionTitle != nil {
		v := *c.SubscriptionTitle
		clone.SubscriptionTitle = &v
	}
	if c.BalanceCurrentUsage != nil {
		v := *c.BalanceCurrentUsage
		clone.BalanceCurrentUsage = &v
	}
	if c.BalanceUsageLimit != nil {
		v := *c.BalanceUsageLimit
		clone.BalanceUsageLimit = &v
	}
	if c.BalanceRemaining != nil {
		v := *c.BalanceRemaining
		clone.BalanceRemaining = &v
	}
	if c.BalanceUsagePercentage != nil {
		v := *c.BalanceUsagePercentage
		clone.BalanceUsagePercentage = &v
	}
	if c.BalanceNextResetAt != nil {
		v := *c.BalanceNextResetAt
		clone.BalanceNextResetAt = &v
	}
	if c.BalanceUpdatedAt != nil {
		v := *c.BalanceUpdatedAt
		clone.BalanceUpdatedAt = &v
	}
	if c.ProxyUrl != nil {
		v := *c.ProxyUrl
		clone.ProxyUrl = &v
	}
	if c.ProxyUsername != nil {
		v := *c.ProxyUsername
		clone.ProxyUsername = &v
	}
	if c.ProxyPassword != nil {
		v := *c.ProxyPassword
		clone.ProxyPassword = &v
	}
	return clone
}

// CredentialsConfig 凭据配置，支持单凭据和多凭据格式
type CredentialsConfig struct{}

// Load 加载凭据文件，返回 (凭据列表, 是否为多凭据格式, 错误)
func (cc CredentialsConfig) Load(path string) ([]KiroCredentials, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}

	// 尝试解析为数组（多凭据格式）
	var list []KiroCredentials
	if err := json.Unmarshal(data, &list); err == nil {
		for i := range list {
			list[i].CanonicalizeAuthMethod()
		}
		sort.Slice(list, func(i, j int) bool {
			return list[i].Priority < list[j].Priority
		})
		return list, true, nil
	}

	// 尝试解析为单个对象（单凭据格式）
	var single KiroCredentials
	if err := json.Unmarshal(data, &single); err == nil {
		single.CanonicalizeAuthMethod()
		return []KiroCredentials{single}, false, nil
	}

	return nil, false, nil
}

// Save 保存凭据到文件（多凭据格式）
func (cc CredentialsConfig) Save(path string, credentials []KiroCredentials) error {
	data, err := json.MarshalIndent(credentials, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
