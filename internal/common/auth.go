// Package common 认证工具函数 - 参考 src/common/auth.rs
package common

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"strings"
)

// ExtractApiKey 从请求头中提取 API Key
//
// 支持两种方式：
//  1. x-api-key header
//  2. Authorization: Bearer <token>
func ExtractApiKey(headers http.Header) *string {
	// 优先检查 x-api-key
	if apiKey := headers.Get("x-api-key"); apiKey != "" {
		return &apiKey
	}

	// 检查 Authorization: Bearer
	auth := headers.Get("Authorization")
	if strings.HasPrefix(strings.ToLower(auth), "bearer ") {
		token := strings.TrimSpace(auth[7:])
		if token != "" {
			return &token
		}
	}

	return nil
}

// SHA256Hex 返回字符串的 SHA-256 hex 摘要
func SHA256Hex(value string) string {
	h := sha256.Sum256([]byte(value))
	return fmt.Sprintf("%x", h)
}
