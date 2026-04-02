// Package tokenmanager Token 刷新逻辑 - 参考 kiro/token_manager.py
package tokenmanager

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"kiro-proxy/internal/config"
	"kiro-proxy/internal/httpclient"
	"kiro-proxy/internal/kiro/machineid"
	"kiro-proxy/internal/kiro/model"
)

const (
	// IdcAmzUserAgent IdC Token 刷新所需的 x-amz-user-agent header
	IdcAmzUserAgent = "aws-sdk-js/3.738.0 ua/2.1 os/other lang/js md/browser#unknown_unknown api/sso-oidc#3.738.0 m/E KiroIDE"
	// UsageLimitsAmzUserAgentPrefix getUsageLimits API 所需的 x-amz-user-agent 前缀
	UsageLimitsAmzUserAgentPrefix = "aws-sdk-js/1.0.0"
)

// refreshClientCache 缓存 Token 刷新用的 HTTP Client，按代理配置 key 索引，避免每次刷新都创建新连接
var (
	refreshClientMu    sync.RWMutex
	refreshClientCache = make(map[string]*http.Client)
)

// getRefreshClient 获取或创建 Token 刷新用的 HTTP Client（60s 超时）
func getRefreshClient(proxy *httpclient.ProxyConfig) (*http.Client, error) {
	key := refreshClientKey(proxy)

	refreshClientMu.RLock()
	if c, ok := refreshClientCache[key]; ok {
		refreshClientMu.RUnlock()
		return c, nil
	}
	refreshClientMu.RUnlock()

	refreshClientMu.Lock()
	defer refreshClientMu.Unlock()
	if c, ok := refreshClientCache[key]; ok {
		return c, nil
	}
	c, err := httpclient.BuildHTTPClient(proxy, 60)
	if err != nil {
		return nil, err
	}
	refreshClientCache[key] = c
	return c, nil
}

func refreshClientKey(proxy *httpclient.ProxyConfig) string {
	if proxy == nil {
		return ""
	}
	return proxy.URL + "\x00" + proxy.Username + "\x00" + proxy.Password
}

// parseRFC3339 解析 RFC3339 时间字符串
func parseRFC3339(s string) (time.Time, error) {
	s = strings.Replace(s, "Z", "+00:00", 1)
	t, err := time.Parse("2006-01-02T15:04:05.999999999-07:00", s)
	if err != nil {
		t, err = time.Parse("2006-01-02T15:04:05-07:00", s)
	}
	return t, err
}

// isTokenExpiringWithin 检查 Token 是否在指定分钟内过期
// 返回 nil 表示无法判断（无 expiresAt 字段）
func isTokenExpiringWithin(cred *model.KiroCredentials, minutes int) *bool {
	if cred.ExpiresAt == nil {
		return nil
	}
	expires, err := parseRFC3339(*cred.ExpiresAt)
	if err != nil {
		return nil
	}
	result := expires.Before(time.Now().UTC().Add(time.Duration(minutes) * time.Minute))
	return &result
}

// IsTokenExpired 检查 Token 是否已过期（提前 5 分钟判断）
func IsTokenExpired(cred *model.KiroCredentials) bool {
	result := isTokenExpiringWithin(cred, 5)
	if result == nil {
		return true // 无法判断时视为已过期
	}
	return *result
}

// IsTokenExpiringSoon 检查 Token 是否即将过期（10 分钟内）
func IsTokenExpiringSoon(cred *model.KiroCredentials) bool {
	result := isTokenExpiringWithin(cred, 10)
	if result == nil {
		return false
	}
	return *result
}

// ValidateRefreshToken 验证 refreshToken 的基本有效性
func ValidateRefreshToken(cred *model.KiroCredentials) error {
	rt := cred.RefreshToken
	if rt == nil {
		return fmt.Errorf("缺少 refreshToken")
	}
	if *rt == "" {
		return fmt.Errorf("refreshToken 为空")
	}
	if len(*rt) < 100 || strings.Contains(*rt, "...") {
		return fmt.Errorf(
			"refreshToken 已被截断（长度: %d 字符）。\n这通常是 Kiro IDE 为了防止凭证被第三方工具使用而故意截断的。",
			len(*rt),
		)
	}
	return nil
}

// RefreshToken 刷新 Token，根据 auth_method 选择 Social 或 IdC
func RefreshToken(ctx context.Context, cred *model.KiroCredentials, cfg *config.Config, proxy *httpclient.ProxyConfig) (*model.KiroCredentials, error) {
	if err := ValidateRefreshToken(cred); err != nil {
		return nil, err
	}

	authMethod := ""
	if cred.AuthMethod != nil {
		authMethod = strings.ToLower(*cred.AuthMethod)
	} else if cred.ClientID != nil && cred.ClientSecret != nil {
		authMethod = "idc"
	} else {
		authMethod = "social"
	}

	if authMethod == "idc" || authMethod == "builder-id" || authMethod == "iam" {
		return RefreshIdcToken(ctx, cred, cfg, proxy)
	}
	return RefreshSocialToken(ctx, cred, cfg, proxy)
}

// RefreshSocialToken 刷新 Social Token
func RefreshSocialToken(ctx context.Context, cred *model.KiroCredentials, cfg *config.Config, proxy *httpclient.ProxyConfig) (*model.KiroCredentials, error) {
	if cred.RefreshToken == nil {
		return nil, fmt.Errorf("缺少 refreshToken")
	}

	region := cred.EffectiveAuthRegion(cfg)
	refreshURL := fmt.Sprintf("https://prod.%s.auth.desktop.kiro.dev/refreshToken", region)
	refreshDomain := fmt.Sprintf("prod.%s.auth.desktop.kiro.dev", region)
	machineID := machineid.GenerateFromCredentials(cred, cfg)
	if machineID == "" {
		return nil, fmt.Errorf("无法生成 machineId")
	}

	effectiveProxy := cred.EffectiveProxy(proxy)
	client, err := getRefreshClient(effectiveProxy)
	if err != nil {
		return nil, fmt.Errorf("构建 HTTP 客户端失败: %w", err)
	}

	reqBody := model.RefreshRequest{RefreshToken: *cred.RefreshToken}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("序列化请求体失败: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, refreshURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", fmt.Sprintf("aws-sdk-js/1.0.27 ua/2.1 os/%s lang/js md/nodejs#%s api/codewhispererstreaming#1.0.27 m/E KiroIDE-%s-%s",
		cfg.SystemVersion, cfg.NodeVersion, cfg.KiroVersion, machineID))
	req.Header.Set("Accept-Encoding", "gzip, compress, deflate, br")
	req.Header.Set("host", refreshDomain)
	req.Header.Set("Connection", "close")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Social Token 刷新请求失败: %w", err)
	}
	defer resp.Body.Close()

	respBytes, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		msg := socialErrorMessage(resp.StatusCode)
		return nil, fmt.Errorf("%s: %d %s", msg, resp.StatusCode, string(respBytes))
	}

	var data model.RefreshResponse
	if err := json.Unmarshal(respBytes, &data); err != nil {
		return nil, fmt.Errorf("解析 Social Token 响应失败: %w", err)
	}
	if data.AccessToken == nil {
		return nil, fmt.Errorf("Social Token 响应缺少 accessToken")
	}

	newCred := cred.Clone()
	newCred.AccessToken = data.AccessToken
	if data.RefreshToken != nil {
		newCred.RefreshToken = data.RefreshToken
	}
	if data.ProfileArn != nil {
		newCred.ProfileArn = data.ProfileArn
	}
	if data.ExpiresIn != nil {
		expiresAt := time.Now().UTC().Add(time.Duration(*data.ExpiresIn) * time.Second).Format(time.RFC3339)
		newCred.ExpiresAt = &expiresAt
	}
	return &newCred, nil
}

// socialErrorMessage 根据状态码返回 Social 刷新错误信息
func socialErrorMessage(statusCode int) string {
	switch statusCode {
	case 401:
		return "OAuth 凭证已过期或无效，需要重新认证"
	case 403:
		return "权限不足，无法刷新 Token"
	case 429:
		return "请求过于频繁，已被限流"
	default:
		if statusCode >= 500 {
			return "服务器错误，AWS OAuth 服务暂时不可用"
		}
		return "Token 刷新失败"
	}
}

// RefreshIdcToken 刷新 IdC Token (AWS SSO OIDC)
func RefreshIdcToken(ctx context.Context, cred *model.KiroCredentials, cfg *config.Config, proxy *httpclient.ProxyConfig) (*model.KiroCredentials, error) {
	if cred.RefreshToken == nil {
		return nil, fmt.Errorf("缺少 refreshToken")
	}
	if cred.ClientID == nil || *cred.ClientID == "" {
		return nil, fmt.Errorf("IdC 刷新需要 clientId")
	}
	if cred.ClientSecret == nil || *cred.ClientSecret == "" {
		return nil, fmt.Errorf("IdC 刷新需要 clientSecret")
	}

	region := cred.EffectiveAuthRegion(cfg)
	refreshURL := fmt.Sprintf("https://oidc.%s.amazonaws.com/token", region)

	effectiveProxy := cred.EffectiveProxy(proxy)
	client, err := getRefreshClient(effectiveProxy)
	if err != nil {
		return nil, fmt.Errorf("构建 HTTP 客户端失败: %w", err)
	}

	reqBody := model.NewIdcRefreshRequest(*cred.ClientID, *cred.ClientSecret, *cred.RefreshToken)
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("序列化请求体失败: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, refreshURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Host", fmt.Sprintf("oidc.%s.amazonaws.com", region))
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("x-amz-user-agent", IdcAmzUserAgent)
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Language", "*")
	req.Header.Set("sec-fetch-mode", "cors")
	req.Header.Set("User-Agent", "node")
	req.Header.Set("Accept-Encoding", "br, gzip, deflate")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("IdC Token 刷新请求失败: %w", err)
	}
	defer resp.Body.Close()

	respBytes, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		msg := idcErrorMessage(resp.StatusCode)
		return nil, fmt.Errorf("%s: %d %s", msg, resp.StatusCode, string(respBytes))
	}

	var data model.IdcRefreshResponse
	if err := json.Unmarshal(respBytes, &data); err != nil {
		return nil, fmt.Errorf("解析 IdC Token 响应失败: %w", err)
	}
	if data.AccessToken == nil {
		return nil, fmt.Errorf("IdC Token 响应缺少 accessToken")
	}

	newCred := cred.Clone()
	newCred.AccessToken = data.AccessToken
	if data.RefreshToken != nil {
		newCred.RefreshToken = data.RefreshToken
	}
	if data.ExpiresIn != nil {
		expiresAt := time.Now().UTC().Add(time.Duration(*data.ExpiresIn) * time.Second).Format(time.RFC3339)
		newCred.ExpiresAt = &expiresAt
	}
	return &newCred, nil
}

// idcErrorMessage 根据状态码返回 IdC 刷新错误信息
func idcErrorMessage(statusCode int) string {
	switch statusCode {
	case 401:
		return "IdC 凭证已过期或无效，需要重新认证"
	case 403:
		return "权限不足，无法刷新 Token"
	case 429:
		return "请求过于频繁，已被限流"
	default:
		if statusCode >= 500 {
			return "服务器错误，AWS OIDC 服务暂时不可用"
		}
		return "IdC Token 刷新失败"
	}
}

// GetUsageLimits 查询使用额度信息
func GetUsageLimits(ctx context.Context, cred *model.KiroCredentials, cfg *config.Config, token string, proxy *httpclient.ProxyConfig) (*model.UsageLimitsResponse, error) {
	region := cred.EffectiveApiRegion(cfg)
	host := fmt.Sprintf("q.%s.amazonaws.com", region)
	machineID := machineid.GenerateFromCredentials(cred, cfg)
	if machineID == "" {
		return nil, fmt.Errorf("无法生成 machineId")
	}

	rawURL := fmt.Sprintf("https://%s/getUsageLimits?origin=AI_EDITOR&resourceType=AGENTIC_REQUEST", host)
	if cred.ProfileArn != nil && *cred.ProfileArn != "" {
		rawURL += "&profileArn=" + url.QueryEscape(*cred.ProfileArn)
	}

	userAgent := fmt.Sprintf(
		"aws-sdk-js/1.0.0 ua/2.1 os/%s lang/js md/nodejs#%s api/codewhispererruntime#1.0.0 m/N,E KiroIDE-%s-%s",
		cfg.SystemVersion, cfg.NodeVersion, cfg.KiroVersion, machineID,
	)
	amzUserAgent := fmt.Sprintf("%s ua/2.1 os/%s lang/js md/nodejs#%s api/codewhispererruntime#1.0.0 m/N,E KiroIDE-%s-%s",
		UsageLimitsAmzUserAgentPrefix, cfg.SystemVersion, cfg.NodeVersion, cfg.KiroVersion, machineID)

	effectiveProxy := cred.EffectiveProxy(proxy)
	client, err := getRefreshClient(effectiveProxy)
	if err != nil {
		return nil, fmt.Errorf("构建 HTTP 客户端失败: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}
	req.Header.Set("x-amz-user-agent", amzUserAgent)
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("host", host)
	req.Header.Set("amz-sdk-invocation-id", uuid.New().String())
	req.Header.Set("amz-sdk-request", "attempt=1; max=1")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Connection", "close")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("获取使用额度请求失败: %w", err)
	}
	defer resp.Body.Close()

	respBytes, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		msg := usageLimitsErrorMessage(resp.StatusCode)
		return nil, fmt.Errorf("%s: %d %s", msg, resp.StatusCode, string(respBytes))
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(respBytes, &raw); err != nil {
		return nil, fmt.Errorf("解析使用额度响应失败: %w", err)
	}
	result := model.UsageLimitsResponseFromDict(raw)
	return &result, nil
}

// usageLimitsErrorMessage 根据状态码返回使用额度查询错误信息
func usageLimitsErrorMessage(statusCode int) string {
	switch statusCode {
	case 401:
		return "认证失败，Token 无效或已过期"
	case 403:
		return "权限不足，无法获取使用额度"
	case 429:
		return "请求过于频繁，已被限流"
	default:
		if statusCode >= 500 {
			return "服务器错误，AWS 服务暂时不可用"
		}
		return "获取使用额度失败"
	}
}
