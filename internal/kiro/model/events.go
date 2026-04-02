package model

// 事件类型定义 - 参考 src/kiro/model/events/base.rs

import (
	"encoding/json"
	"fmt"

	"kiro-proxy/internal/logger"
)

// EventType 事件类型常量
type EventType string

const (
	// EventTypeAssistantResponse 助手响应事件
	EventTypeAssistantResponse EventType = "assistantResponseEvent"
	// EventTypeToolUse 工具使用事件
	EventTypeToolUse EventType = "toolUseEvent"
	// EventTypeMetering 计量事件
	EventTypeMetering EventType = "meteringEvent"
	// EventTypeContextUsage 上下文使用率事件
	EventTypeContextUsage EventType = "contextUsageEvent"
	// EventTypeUnknown 未知事件
	EventTypeUnknown EventType = "unknown"
)

// EventTypeFromStr 从字符串解析事件类型，未知类型返回 EventTypeUnknown
func EventTypeFromStr(s string) EventType {
	switch s {
	case string(EventTypeAssistantResponse):
		return EventTypeAssistantResponse
	case string(EventTypeToolUse):
		return EventTypeToolUse
	case string(EventTypeMetering):
		return EventTypeMetering
	case string(EventTypeContextUsage):
		return EventTypeContextUsage
	default:
		return EventTypeUnknown
	}
}

// AssistantResponseEvent 助手响应事件
type AssistantResponseEvent struct {
	Content string `json:"content"`
}

// AssistantResponseEventFromDict 从字典构造助手响应事件
func AssistantResponseEventFromDict(data map[string]interface{}) AssistantResponseEvent {
	content, _ := data["content"].(string)
	return AssistantResponseEvent{Content: content}
}

// ToolUseEvent 工具使用事件
type ToolUseEvent struct {
	Name      string `json:"name"`
	ToolUseID string `json:"toolUseId"`
	Input     string `json:"input"`
	Stop      bool   `json:"stop"`
}

// ToolUseEventFromDict 从字典构造工具使用事件
// input 字段：如果不是字符串类型，则序列化为 JSON 字符串
func ToolUseEventFromDict(data map[string]interface{}) ToolUseEvent {
	name, _ := data["name"].(string)
	toolUseID, _ := data["toolUseId"].(string)
	stop, _ := data["stop"].(bool)

	var input string
	rawInput := data["input"]
	switch v := rawInput.(type) {
	case string:
		input = v
	case nil:
		input = ""
	default:
		// Kiro 偶尔可能返回 JSON 对象而非字符串片段，转为 JSON 字符串
		logger.Warnf("ToolUseEvent.input 类型异常: %v (type=%T), 转为 JSON 字符串", v, v)
		bs, err := json.Marshal(v)
		if err != nil {
			input = ""
		} else {
			input = string(bs)
		}
	}

	return ToolUseEvent{
		Name:      name,
		ToolUseID: toolUseID,
		Input:     input,
		Stop:      stop,
	}
}

// ContextUsageEvent 上下文使用率事件
type ContextUsageEvent struct {
	ContextUsagePercentage float64 `json:"contextUsagePercentage"`
}

// ContextUsageEventFromDict 从字典构造上下文使用率事件
func ContextUsageEventFromDict(data map[string]interface{}) ContextUsageEvent {
	pct, _ := data["contextUsagePercentage"].(float64)
	return ContextUsageEvent{ContextUsagePercentage: pct}
}

// FormattedPercentage 格式化百分比字符串
func (e *ContextUsageEvent) FormattedPercentage() string {
	return fmt.Sprintf("%.2f%%", e.ContextUsagePercentage)
}
