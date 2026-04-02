// Package httpclient HTTP 客户端构建 - 参考 src/http_client.rs
package httpclient

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"
)

const (
	// DefaultConnectTimeout 默认连接超时
	DefaultConnectTimeout = 30 * time.Second
	// DefaultWriteTimeout 默认写入超时
	DefaultWriteTimeout = 30 * time.Second
	// DefaultPoolTimeout 默认连接池超时
	DefaultPoolTimeout = 8 * time.Second
)

// ProxyConfig 代理配置
type ProxyConfig struct {
	URL      string
	Username string
	Password string
}

// WithAuth 返回带有认证信息的新 ProxyConfig
func (p ProxyConfig) WithAuth(username, password string) ProxyConfig {
	return ProxyConfig{
		URL:      p.URL,
		Username: username,
		Password: password,
	}
}

// buildProxyURL 构建带认证信息的代理 URL
func buildProxyURL(proxy *ProxyConfig) (*url.URL, error) {
	proxyURL, err := url.Parse(proxy.URL)
	if err != nil {
		return nil, fmt.Errorf("解析代理 URL 失败: %w", err)
	}

	// 如果提供了用户名和密码，插入认证信息
	if proxy.Username != "" && proxy.Password != "" {
		proxyURL.User = url.UserPassword(proxy.Username, proxy.Password)
	}

	return proxyURL, nil
}

// BuildHTTPClient 构建 *http.Client，支持代理和超时配置
func BuildHTTPClient(proxy *ProxyConfig, timeoutSecs int) (*http.Client, error) {
	timeout := time.Duration(timeoutSecs) * time.Second

	dialTimeout := minDuration(DefaultConnectTimeout, timeout)
	dialer := &net.Dialer{
		Timeout:   dialTimeout,
		KeepAlive: 30 * time.Second,
	}
	transport := &http.Transport{
		DialContext:           dialer.DialContext,
		MaxIdleConns:          400,
		MaxIdleConnsPerHost:   200,
		IdleConnTimeout:       120 * time.Second,
		TLSHandshakeTimeout:   dialTimeout,
		ResponseHeaderTimeout: minDuration(DefaultConnectTimeout, timeout),
		ForceAttemptHTTP2:     true,
	}

	// 配置代理
	if proxy != nil {
		proxyURL, err := buildProxyURL(proxy)
		if err != nil {
			return nil, err
		}
		transport.Proxy = http.ProxyURL(proxyURL)
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   timeout,
	}

	return client, nil
}

// minDuration 返回两个 Duration 中较小的一个
func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
