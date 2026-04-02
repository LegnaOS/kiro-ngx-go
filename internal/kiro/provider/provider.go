// Package provider Kiro API Provider - 参考 clauldcode-proxy/kiro/provider.py
package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/google/uuid"
	"kiro-proxy/internal/httpclient"
	"kiro-proxy/internal/kiro/machineid"
	"kiro-proxy/internal/kiro/model"
	"kiro-proxy/internal/kiro/tokenmanager"
)

const (
	// MaxRetriesPerCredential 每个凭据的最大重试次数
	MaxRetriesPerCredential = 3
	// MaxTotalRetries 总重试次数硬上限
	MaxTotalRetries = 9
)

// KiroProvider Kiro API Provider，支持多凭据故障转移和重试
type KiroProvider struct {
	tokenManager *tokenmanager.MultiTokenManager
	globalProxy  *httpclient.ProxyConfig
	// clientCache 用代理配置的字符串表示作 key，避免指针比较导致缓存未命中
	clientCache map[string]*http.Client
	cacheLock   sync.RWMutex
}

// proxyKey 将代理配置转换为可比较的字符串 key
func proxyKey(proxy *httpclient.ProxyConfig) string {
	if proxy == nil {
		return ""
	}
	return proxy.URL + "\x00" + proxy.Username + "\x00" + proxy.Password
}

// NewKiroProvider 创建 KiroProvider 实例
func NewKiroProvider(tm *tokenmanager.MultiTokenManager, proxy *httpclient.ProxyConfig) *KiroProvider {
	p := &KiroProvider{
		tokenManager: tm,
		globalProxy:  proxy,
		clientCache:  make(map[string]*http.Client),
	}

	// 预热：构建全局代理对应的 Client
	client, _ := httpclient.BuildHTTPClient(proxy, 720)
	p.clientCache[proxyKey(proxy)] = client

	return p
}

// getClientFor 根据代理配置获取 HTTP Client
func (p *KiroProvider) getClientFor(credentials *model.KiroCredentials) (*http.Client, error) {
	effective := credentials.EffectiveProxy(p.globalProxy)
	key := proxyKey(effective)

	p.cacheLock.RLock()
	if client, ok := p.clientCache[key]; ok {
		p.cacheLock.RUnlock()
		return client, nil
	}
	p.cacheLock.RUnlock()

	p.cacheLock.Lock()
	defer p.cacheLock.Unlock()

	// 双重检查
	if client, ok := p.clientCache[key]; ok {
		return client, nil
	}

	client, err := httpclient.BuildHTTPClient(effective, 720)
	if err != nil {
		return nil, err
	}

	p.clientCache[key] = client
	return client, nil
}

// BaseURL 返回基础 URL
func (p *KiroProvider) BaseURL() string {
	cfg := p.tokenManager.Config()
	return fmt.Sprintf("https://q.%s.amazonaws.com/generateAssistantResponse", cfg.EffectiveApiRegion())
}

// BaseURLFor 返回指定凭据的基础 URL
func (p *KiroProvider) BaseURLFor(credentials *model.KiroCredentials) string {
	cfg := p.tokenManager.Config()
	return fmt.Sprintf("https://q.%s.amazonaws.com/generateAssistantResponse", credentials.EffectiveApiRegion(cfg))
}

// BuildHeaders 构建 API 请求头
func (p *KiroProvider) BuildHeaders(ctx context.Context, credentials *model.KiroCredentials, token string) (http.Header, error) {
	cfg := p.tokenManager.Config()
	machineID := machineid.GenerateFromCredentials(credentials, cfg)
	if machineID == "" {
		return nil, fmt.Errorf("无法生成 machine_id，请检查凭证配置")
	}
	
	kv := cfg.KiroVersion
	osName := cfg.SystemVersion
	nv := cfg.NodeVersion
	
	xAmzUa := fmt.Sprintf("aws-sdk-js/1.0.27 ua/2.1 os/%s lang/js md/nodejs#%s api/codewhispererstreaming#1.0.27 m/E KiroIDE-%s-%s",
		osName, nv, kv, machineID)
	ua := fmt.Sprintf("aws-sdk-js/1.0.27 ua/2.1 os/%s lang/js md/nodejs#%s api/codewhispererstreaming#1.0.27 m/E KiroIDE-%s-%s",
		osName, nv, kv, machineID)

	headers := http.Header{}
	headers.Set("x-amz-user-agent", xAmzUa)
	headers.Set("User-Agent", ua)
	headers.Set("host", p.baseDomainFor(credentials))
	headers.Set("amz-sdk-invocation-id", generateUUID())
	headers.Set("amz-sdk-request", "attempt=1; max=3")
	headers.Set("Authorization", "Bearer "+token)
	headers.Set("Connection", "keep-alive")
	headers.Set("Content-Type", "application/json")
	headers.Set("Accept", "*/*")
	headers.Set("Accept-Language", "*")
	headers.Set("sec-fetch-mode", "cors")
	headers.Set("Accept-Encoding", "br, gzip, deflate")
	
	return headers, nil
}

func (p *KiroProvider) baseDomainFor(credentials *model.KiroCredentials) string {
	cfg := p.tokenManager.Config()
	return fmt.Sprintf("q.%s.amazonaws.com", credentials.EffectiveApiRegion(cfg))
}

// ExtractModelFromRequest 从请求体中提取模型信息
func ExtractModelFromRequest(requestBody string) (string, error) {
	var data map[string]interface{}
	if err := json.Unmarshal([]byte(requestBody), &data); err != nil {
		return "", err
	}
	
	conversationState, ok := data["conversationState"].(map[string]interface{})
	if !ok {
		return "", nil
	}
	
	currentMessage, ok := conversationState["currentMessage"].(map[string]interface{})
	if !ok {
		return "", nil
	}
	
	userInputMessage, ok := currentMessage["userInputMessage"].(map[string]interface{})
	if !ok {
		return "", nil
	}
	
	modelID, ok := userInputMessage["modelId"].(string)
	if !ok {
		return "", nil
	}
	
	return modelID, nil
}

// CallContext 调用上下文
type CallContext struct {
	Credentials *model.KiroCredentials
	Token       string
	RetryCount  int
}

// StreamHandler 流式响应处理函数
type StreamHandler func(chunk []byte) error

// NonStreamHandler 非流式响应处理函数
type NonStreamHandler func(response []byte) error

// PostMessages 发送消息请求（非流式）
func (p *KiroProvider) PostMessages(ctx context.Context, requestBody []byte, handler NonStreamHandler) error {
	totalRetries := 0
	credentialRetries := 0
	
	for totalRetries < MaxTotalRetries && credentialRetries < MaxRetriesPerCredential {
		cred := p.tokenManager.GetCurrentCredential()
		if cred == nil {
			return fmt.Errorf("没有可用的凭据")
		}
		
		token := cred.AccessToken
		if token == nil || *token == "" || tokenmanager.IsTokenExpired(cred) {
			// Token 为空或已过期，刷新
			credIdx := p.tokenManager.GetCurrentIndex()
			newCred, err := tokenmanager.RefreshToken(ctx, cred, p.tokenManager.Config(), p.globalProxy)
			if err != nil {
				credentialRetries++
				totalRetries++
				_ = p.tokenManager.SwitchToNext()
				continue
			}
			p.tokenManager.UpdateCredentialToken(credIdx, newCred)
			token = newCred.AccessToken
		}

		headers, err := p.BuildHeaders(ctx, cred, *token)
		if err != nil {
			return err
		}

		client, err := p.getClientFor(cred)
		if err != nil {
			return err
		}

		url := p.BaseURLFor(cred)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(requestBody)))
		if err != nil {
			return err
		}
		req.Header = headers

		resp, err := client.Do(req)
		if err != nil {
			credentialRetries++
			totalRetries++
			_ = p.tokenManager.SwitchToNext()
			continue
		}
		defer resp.Body.Close()

		if resp.StatusCode >= 400 {
			body, _ := io.ReadAll(resp.Body)
			credentialRetries++
			totalRetries++

			if resp.StatusCode == 401 || resp.StatusCode == 403 {
				// 认证失败，切换凭据
				_ = p.tokenManager.SwitchToNext()
			}

			return fmt.Errorf("API 请求失败：%d %s", resp.StatusCode, string(body))
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}

		// 记录成功请求统计
		credID := 0
		if cred.ID != nil {
			credID = *cred.ID
		}
		mdl, _ := ExtractModelFromRequest(string(requestBody))
		p.tokenManager.RecordRequest(credID, mdl)

		return handler(body)
	}

	return fmt.Errorf("达到最大重试次数")
}

// PostMessagesStream 发送消息请求（流式）
func (p *KiroProvider) PostMessagesStream(ctx context.Context, requestBody []byte, handler StreamHandler) error {
	totalRetries := 0
	credentialRetries := 0

	for totalRetries < MaxTotalRetries && credentialRetries < MaxRetriesPerCredential {
		cred := p.tokenManager.GetCurrentCredential()
		if cred == nil {
			return fmt.Errorf("没有可用的凭据")
		}

		token := cred.AccessToken
		if token == nil || *token == "" || tokenmanager.IsTokenExpired(cred) {
			// Token 为空或已过期，刷新
			credIdx := p.tokenManager.GetCurrentIndex()
			newCred, err := tokenmanager.RefreshToken(ctx, cred, p.tokenManager.Config(), p.globalProxy)
			if err != nil {
				credentialRetries++
				totalRetries++
				_ = p.tokenManager.SwitchToNext()
				continue
			}
			p.tokenManager.UpdateCredentialToken(credIdx, newCred)
			token = newCred.AccessToken
		}
		
		headers, err := p.BuildHeaders(ctx, cred, *token)
		if err != nil {
			return err
		}
		
		client, err := p.getClientFor(cred)
		if err != nil {
			return err
		}
		
		url := p.BaseURLFor(cred)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(requestBody)))
		if err != nil {
			return err
		}
		req.Header = headers
		
		resp, err := client.Do(req)
		if err != nil {
			credentialRetries++
			totalRetries++
			_ = p.tokenManager.SwitchToNext()
			continue
		}
		
		if resp.StatusCode >= 400 {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			credentialRetries++
			totalRetries++
			
			if resp.StatusCode == 401 || resp.StatusCode == 403 {
				_ = p.tokenManager.SwitchToNext()
			}
			
			return fmt.Errorf("API 请求失败：%d %s", resp.StatusCode, string(body))
		}
		
		// 记录成功请求统计（流式）
		credID := 0
		if cred.ID != nil {
			credID = *cred.ID
		}
		mdl, _ := ExtractModelFromRequest(string(requestBody))
		p.tokenManager.RecordRequest(credID, mdl)

		// 处理流式响应（复用 buffer，避免每次循环分配）
		defer resp.Body.Close()

		buf := make([]byte, 32*1024)
		for {
			n, err := resp.Body.Read(buf)
			if n > 0 {
				if herr := handler(buf[:n]); herr != nil {
					return herr
				}
			}
			if err != nil {
				if err == io.EOF {
					break
				}
				credentialRetries++
				totalRetries++
				_ = p.tokenManager.SwitchToNext()
				break
			}
		}

		return nil
	}

	return fmt.Errorf("达到最大重试次数")
}

// generateUUID 生成标准 UUID v4
func generateUUID() string {
	return uuid.New().String()
}
