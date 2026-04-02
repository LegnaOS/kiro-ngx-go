// Anthropic API 路由注册 - 参考 anthropic_api/router.py
package anthropic

import "net/http"

// RegisterRoutes 注册所有 Anthropic API 路由
func RegisterRoutes(mux *http.ServeMux, state *AppState) {
	// 模型列表
	mux.HandleFunc("GET /v1/models", func(w http.ResponseWriter, r *http.Request) {
		HandleGetModels(w, r, state)
	})

	// 消息接口（标准）
	mux.HandleFunc("POST /v1/messages", func(w http.ResponseWriter, r *http.Request) {
		HandlePostMessages(w, r, state, false)
	})

	// Token 计数
	mux.HandleFunc("POST /v1/messages/count_tokens", func(w http.ResponseWriter, r *http.Request) {
		HandleCountTokens(w, r, state)
	})

	// Claude Code 兼容端点（使用缓冲流）
	mux.HandleFunc("POST /cc/v1/messages", func(w http.ResponseWriter, r *http.Request) {
		HandlePostMessages(w, r, state, true)
	})
	mux.HandleFunc("POST /cc/v1/messages/count_tokens", func(w http.ResponseWriter, r *http.Request) {
		HandleCountTokens(w, r, state)
	})
}
