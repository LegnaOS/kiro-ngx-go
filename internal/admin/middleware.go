// Package admin Admin API 认证中间件 - 参考 admin/middleware.py
package admin

import (
	"crypto/hmac"
	"encoding/json"
	"net/http"

	"kiro-proxy/internal/common"
)

// NewAuthMiddleware 创建 Admin 认证中间件
func NewAuthMiddleware(next http.Handler, adminApiKey string) http.Handler {
	return AdminAuthMiddleware(adminApiKey, next)
}

// AdminAuthMiddleware Admin API Key 验证中间件
//
// 从请求头中提取 API Key（x-api-key 或 Authorization: Bearer），
// 使用 hmac.Equal 进行常量时间比较，验证失败返回 401。
func AdminAuthMiddleware(adminApiKey string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiKey := common.ExtractApiKey(r.Header)
		if apiKey != nil && hmac.Equal([]byte(*apiKey), []byte(adminApiKey)) {
			next.ServeHTTP(w, r)
			return
		}

		// 认证失败，返回 401
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		resp := AuthenticationError()
		json.NewEncoder(w).Encode(resp)
	})
}
