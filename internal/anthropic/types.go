// Anthropic API 类型定义 - 参考 anthropic_api/types.py
package anthropic

import (
	"encoding/json"
)

// MaxBudgetTokens 不限制，设为 0 表示透传客户端原始值
const MaxBudgetTokens = 0

// ---------------------------------------------------------------------------
// 错误响应
// ---------------------------------------------------------------------------

// ErrorDetail 错误详情
type ErrorDetail struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// ErrorResponse 错误响应
type ErrorResponse struct {
	Error ErrorDetail `json:"error"`
}

// NewErrorResponse 创建错误响应
func NewErrorResponse(errorType, message string) ErrorResponse {
	return ErrorResponse{
		Error: ErrorDetail{Type: errorType, Message: message},
	}
}

// AuthenticationError 创建认证错误响应
func AuthenticationError() ErrorResponse {
	return NewErrorResponse("authentication_error", "Invalid API key")
}

// ---------------------------------------------------------------------------
// Models 端点类型
// ---------------------------------------------------------------------------

// Model 模型信息
type Model struct {
	ID          string `json:"id"`
	Object      string `json:"object"`
	Created     int64  `json:"created"`
	OwnedBy     string `json:"owned_by"`
	DisplayName string `json:"display_name"`
	Type        string `json:"type"`
	MaxTokens   int    `json:"max_tokens"`
}

// ModelsResponse 模型列表响应
type ModelsResponse struct {
	Object string  `json:"object"`
	Data   []Model `json:"data"`
}

// ---------------------------------------------------------------------------
// Thinking 配置
// ---------------------------------------------------------------------------

// Thinking 思考模式配置
type Thinking struct {
	Type         string `json:"type"`
	BudgetTokens int    `json:"budget_tokens"`
}

// IsEnabled 判断是否启用思考模式（enabled 或 adaptive）
func (t *Thinking) IsEnabled() bool {
	return t.Type == "enabled" || t.Type == "adaptive"
}

// OutputConfig 输出配置
type OutputConfig struct {
	Effort string `json:"effort"`
}

// Metadata 请求元数据
type Metadata struct {
	UserID *string `json:"user_id,omitempty"`
}

// ---------------------------------------------------------------------------
// MessagesRequest - 主请求类型
// ---------------------------------------------------------------------------

// MessagesRequest Anthropic Messages 请求体
type MessagesRequest struct {
	Model        string                   `json:"model"`
	MaxTokens    int                      `json:"max_tokens"`
	Messages     []map[string]interface{} `json:"messages"`
	Stream       bool                     `json:"stream"`
	System       interface{}              `json:"system,omitempty"`       // string 或 []map[string]interface{}
	Tools        []map[string]interface{} `json:"tools,omitempty"`
	ToolChoice   interface{}              `json:"tool_choice,omitempty"`
	ThinkingRaw  *map[string]interface{}  `json:"thinking,omitempty"`
	OutputConfig *map[string]interface{}  `json:"output_config,omitempty"`
	MetadataRaw  *map[string]interface{}  `json:"metadata,omitempty"`
}

// NormalizeSystem 将 string 类型的 system 转换为 [{type: "text", text: ...}] 格式
func (r *MessagesRequest) NormalizeSystem() {
	if r.System == nil {
		return
	}
	switch v := r.System.(type) {
	case string:
		if v == "" {
			r.System = nil
			return
		}
		r.System = []map[string]interface{}{
			{"type": "text", "text": v},
		}
	case []interface{}:
		if len(v) == 0 {
			r.System = nil
			return
		}
		// 转换为 []map[string]interface{}
		result := make([]map[string]interface{}, 0, len(v))
		for _, item := range v {
			if m, ok := item.(map[string]interface{}); ok {
				result = append(result, m)
			}
		}
		if len(result) == 0 {
			r.System = nil
		} else {
			r.System = result
		}
	case []map[string]interface{}:
		if len(v) == 0 {
			r.System = nil
		}
		// 已经是目标格式，无需转换
	}
}

// GetThinking 获取 Thinking 配置
func (r *MessagesRequest) GetThinking() *Thinking {
	if r.ThinkingRaw == nil {
		return nil
	}
	raw := *r.ThinkingRaw
	t := &Thinking{
		Type:         "disabled",
		BudgetTokens: 20000,
	}
	if v, ok := raw["type"].(string); ok {
		t.Type = v
	}
	if v, ok := raw["budget_tokens"].(float64); ok {
		t.BudgetTokens = int(v)
	}
	// 不限制 budget_tokens，透传客户端原始值
	return t
}

// GetOutputConfig 获取输出配置
func (r *MessagesRequest) GetOutputConfig() *OutputConfig {
	if r.OutputConfig == nil {
		return nil
	}
	raw := *r.OutputConfig
	oc := &OutputConfig{Effort: "high"}
	if v, ok := raw["effort"].(string); ok {
		oc.Effort = v
	}
	return oc
}

// GetMetadata 获取元数据
func (r *MessagesRequest) GetMetadata() *Metadata {
	if r.MetadataRaw == nil {
		return nil
	}
	raw := *r.MetadataRaw
	m := &Metadata{}
	if v, ok := raw["user_id"].(string); ok {
		m.UserID = &v
	}
	return m
}

// GetSystemMessages 获取 system 消息列表（已归一化为 []map[string]interface{} 格式）
func (r *MessagesRequest) GetSystemMessages() []map[string]interface{} {
	if r.System == nil {
		return nil
	}
	switch v := r.System.(type) {
	case string:
		if v == "" {
			return nil
		}
		return []map[string]interface{}{{"text": v}}
	case []map[string]interface{}:
		return v
	case []interface{}:
		result := make([]map[string]interface{}, 0, len(v))
		for _, item := range v {
			if m, ok := item.(map[string]interface{}); ok {
				result = append(result, m)
			}
		}
		return result
	}
	return nil
}

// GetMessages 获取消息列表，转换为 AnthropicMessage 切片
func (r *MessagesRequest) GetMessages() []AnthropicMessage {
	result := make([]AnthropicMessage, 0, len(r.Messages))
	for _, m := range r.Messages {
		role, _ := m["role"].(string)
		content := m["content"] // interface{}, 可能是 string 或 []interface{}
		if content == nil {
			content = ""
		}
		result = append(result, AnthropicMessage{Role: role, Content: content})
	}
	return result
}

// UnmarshalJSON 自定义反序列化，处理 system 字段的灵活类型
func (r *MessagesRequest) UnmarshalJSON(data []byte) error {
	// 使用别名避免递归调用
	type Alias MessagesRequest
	aux := &struct {
		*Alias
	}{
		Alias: (*Alias)(r),
	}
	if err := json.Unmarshal(data, aux); err != nil {
		return err
	}
	// 反序列化后归一化 system 字段
	r.NormalizeSystem()
	return nil
}

// ---------------------------------------------------------------------------
// 消息辅助类型
// ---------------------------------------------------------------------------

// AnthropicMessage Anthropic 格式的消息
type AnthropicMessage struct {
	Role    string
	Content interface{} // string 或 []interface{}
}

// ---------------------------------------------------------------------------
// CountTokens 端点类型
// ---------------------------------------------------------------------------

// CountTokensRequest token 计数请求
type CountTokensRequest struct {
	Model    string                   `json:"model"`
	Messages []map[string]interface{} `json:"messages"`
	System   interface{}              `json:"system,omitempty"`
	Tools    []map[string]interface{} `json:"tools,omitempty"`
}

// CountTokensResponse token 计数响应
type CountTokensResponse struct {
	InputTokens int `json:"input_tokens"`
}
