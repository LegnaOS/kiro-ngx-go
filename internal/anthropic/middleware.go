// Anthropic API 中间件 - 参考 anthropic_api/middleware.py
package anthropic

import (
	"context"
	"crypto/hmac"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"kiro-proxy/internal/apikeys"
	"kiro-proxy/internal/common"
	"kiro-proxy/internal/kiro/provider"
)

// AppState 应用共享状态
type AppState struct {
	ApiKey       string
	KiroProvider *provider.KiroProvider
	ProfileArn   string
}

// contextKey 上下文键类型
type contextKey string

const (
	// apiKeyIDKey 存储在 context 中的 API key ID
	apiKeyIDKey contextKey = "api_key_id"
	// appStateKey 存储在 context 中的 AppState
	appStateKey contextKey = "app_state"
)

// GetApiKeyID 从请求上下文中获取 API key ID
func GetApiKeyID(ctx context.Context) string {
	if v, ok := ctx.Value(apiKeyIDKey).(string); ok {
		return v
	}
	return ""
}

// GetAppState 从请求上下文中获取 AppState
func GetAppState(ctx context.Context) *AppState {
	if v, ok := ctx.Value(appStateKey).(*AppState); ok {
		return v
	}
	return nil
}

// AuthMiddleware 验证 /v1/ 和 /cc/ 路径的 API key
func AuthMiddleware(apiKey string, state *AppState, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		// 仅对 /v1/ 和 /cc/ 路径进行认证
		if !strings.HasPrefix(path, "/v1/") && !strings.HasPrefix(path, "/cc/") {
			next.ServeHTTP(w, r)
			return
		}

		key := common.ExtractApiKey(r.Header)
		if key == nil {
			writeJSON(w, 401, NewErrorResponse("authentication_error", "Invalid API key"))
			return
		}

		// 管理员 key — 无限制
		if hmac.Equal([]byte(*key), []byte(apiKey)) {
			ctx := r.Context()
			ctx = context.WithValue(ctx, apiKeyIDKey, *key)
			ctx = context.WithValue(ctx, appStateKey, state)
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		// 多 key 查找 via api_keys manager
		if mgr := apikeys.GetApiKeyManager(); mgr != nil {
			entry := mgr.Lookup(*key)
			if entry != nil {
				// 启用/禁用检查
				if !entry.Enabled {
					writeJSON(w, 403, map[string]interface{}{
						"type": "error",
						"error": map[string]interface{}{
							"type":    "forbidden",
							"message": "API key is disabled",
						},
					})
					return
				}
				// 月度配额检查
				allowed, _ := mgr.CheckQuota(*key)
				if !allowed {
					// 重新查找获取最新用量
					info := mgr.Lookup(*key)
					used := int64(0)
					quota := -1
					if info != nil {
						used = int64(info.BilledTokens)
						quota = effectiveQuotaFromEntry(mgr, info)
					}
					writeJSON(w, 429, map[string]interface{}{
						"type": "error",
						"error": map[string]interface{}{
							"type":    "rate_limit_error",
							"message": fmt.Sprintf("Your API key has exceeded its monthly token quota. Used: %s, Limit: %s. Please contact your administrator to increase your quota or wait for the monthly reset.", fmtTokens(used), fmtTokens(int64(quota))),
						},
					})
					return
				}
				// 认证通过
				ctx := r.Context()
				ctx = context.WithValue(ctx, apiKeyIDKey, *key)
				ctx = context.WithValue(ctx, appStateKey, state)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
		}

		writeJSON(w, 401, NewErrorResponse("authentication_error", "Invalid API key"))
	})
}

// NewAuthMiddleware 创建认证中间件
func NewAuthMiddleware(next http.Handler, state *AppState) http.Handler {
	return AuthMiddleware(state.ApiKey, state, next)
}

// CORSMiddleware 添加 CORS 响应头
func CORSMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "*")
		w.Header().Set("Access-Control-Allow-Headers", "*")
		if r.Method == http.MethodOptions {
			w.WriteHeader(204)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// NewCORSMiddleware 创建 CORS 中间件
func NewCORSMiddleware(next http.Handler) http.Handler {
	return CORSMiddleware(next)
}

// writeJSON 写入 JSON 响应
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// effectiveQuotaFromEntry 获取 key 的有效配额（key 级别优先，否则取分组，否则 -1）
func effectiveQuotaFromEntry(mgr *apikeys.ApiKeyManager, entry *apikeys.KeyEntry) int {
	if entry.MonthlyQuota != nil {
		return *entry.MonthlyQuota
	}
	groups := mgr.GetGroups()
	if g, ok := groups[entry.Group]; ok {
		return g.MonthlyQuota
	}
	return -1
}

// fmtTokens 格式化 token 数量为可读字符串
func fmtTokens(n int64) string {
	if n < 0 {
		return "unlimited"
	}
	if n >= 1_000_000_000 {
		return fmt.Sprintf("%.1fB", float64(n)/1e9)
	}
	if n >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	}
	if n >= 1_000 {
		return fmt.Sprintf("%.1fK", float64(n)/1e3)
	}
	return fmt.Sprintf("%d", n)
}
