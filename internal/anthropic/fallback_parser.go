// Package anthropic Fallback JSON 解析器 — 当 EventStream 严格解码失败时从原始文本中提取事件
// 参考 Python handlers.py _KiroFallbackEventParser / _parse_kiro_json_events_from_buffer
package anthropic

import (
	"encoding/json"
	"strings"

	"kiro-proxy/internal/kiro/model"
	"kiro-proxy/internal/logger"
)

const fallbackBufferLimitChars = 256_000

var fallbackJSONCandidatePrefixes = []string{
	`{"content":`,
	`{"name":`,
	`{"followupPrompt":`,
	`{"input":`,
	`{"stop":`,
	`{"contextUsagePercentage":`,
}

// KiroFallbackParser 从原始字节流中提取 JSON 事件对象
type KiroFallbackParser struct {
	buffer           string
	lastContent      string
	lastEmittedKind  string
	currentToolUseID string
	currentToolName  string
}

// NewKiroFallbackParser 创建 fallback 解析器
func NewKiroFallbackParser() *KiroFallbackParser {
	return &KiroFallbackParser{}
}

// Reset 重置 fallback 解析器状态（严格解码恢复正常时调用）
func (p *KiroFallbackParser) Reset() {
	p.buffer = ""
	p.lastContent = ""
	p.lastEmittedKind = ""
}

// Feed 喂入原始 chunk，返回解析出的 Kiro 事件列表
func (p *KiroFallbackParser) Feed(chunk []byte) []interface{} {
	text := string(chunk)
	if text == "" {
		return nil
	}
	p.buffer += text
	if len(p.buffer) > fallbackBufferLimitChars {
		half := fallbackBufferLimitChars / 2
		p.buffer = p.buffer[len(p.buffer)-half:]
	}
	rawEvents, remainder := parseKiroJSONEventsFromBuffer(p.buffer)
	p.buffer = remainder

	var events []interface{}
	for _, raw := range rawEvents {
		kind, _ := raw["kind"].(string)
		switch kind {
		case "content":
			content, _ := raw["content"].(string)
			if p.lastEmittedKind == "content" && p.lastContent == content {
				continue
			}
			p.lastContent = content
			p.lastEmittedKind = "content"
			events = append(events, model.AssistantResponseEvent{Content: content})
		case "tool_use_start":
			toolUseID, _ := raw["tool_use_id"].(string)
			name, _ := raw["name"].(string)
			input, _ := raw["input"].(string)
			stop, _ := raw["stop"].(bool)
			if toolUseID != "" {
				p.currentToolUseID = toolUseID
			}
			if name != "" {
				p.currentToolName = name
			}
			p.lastEmittedKind = "tool_use"
			events = append(events, model.ToolUseEvent{
				Name:      p.currentToolName,
				ToolUseID: p.currentToolUseID,
				Input:     input,
				Stop:      stop,
			})
		case "tool_use_input":
			input, _ := raw["input"].(string)
			p.lastEmittedKind = "tool_use"
			events = append(events, model.ToolUseEvent{
				Name:      p.currentToolName,
				ToolUseID: p.currentToolUseID,
				Input:     input,
				Stop:      false,
			})
		case "tool_use_stop":
			stop, _ := raw["stop"].(bool)
			p.lastEmittedKind = "tool_use"
			events = append(events, model.ToolUseEvent{
				Name:      p.currentToolName,
				ToolUseID: p.currentToolUseID,
				Input:     "",
				Stop:      stop,
			})
		case "context_usage":
			pct, _ := raw["context_usage_percentage"].(float64)
			p.lastEmittedKind = "context_usage"
			events = append(events, model.ContextUsageEvent{ContextUsagePercentage: pct})
		}
	}
	return events
}

// findFallbackJSONStart 查找下一个候选 JSON 对象起始位置
func findFallbackJSONStart(buffer string, searchStart int) int {
	best := -1
	for _, prefix := range fallbackJSONCandidatePrefixes {
		idx := indexOf(buffer, prefix, searchStart)
		if idx >= 0 && (best < 0 || idx < best) {
			best = idx
		}
	}
	return best
}

// indexOf 从 searchStart 开始查找子串
func indexOf(s, substr string, searchStart int) int {
	if searchStart >= len(s) {
		return -1
	}
	idx := -1
	for i := searchStart; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			idx = i
			break
		}
	}
	return idx
}

// findFallbackJSONEnd 查找匹配的 JSON 对象结束位置（处理字符串转义）
func findFallbackJSONEnd(buffer string, start int) int {
	braceCount := 0
	inString := false
	escapeNext := false

	for i := start; i < len(buffer); i++ {
		ch := buffer[i]
		if escapeNext {
			escapeNext = false
			continue
		}
		if ch == '\\' {
			escapeNext = true
			continue
		}
		if ch == '"' {
			inString = !inString
			continue
		}
		if !inString {
			if ch == '{' {
				braceCount++
			} else if ch == '}' {
				braceCount--
				if braceCount == 0 {
					return i
				}
			}
		}
	}
	return -1
}

// parseKiroJSONEventsFromBuffer 从缓冲区解析所有完整的 JSON 事件
func parseKiroJSONEventsFromBuffer(buffer string) ([]map[string]interface{}, string) {
	var events []map[string]interface{}
	searchStart := 0
	lastConsumed := 0

	for {
		jsonStart := findFallbackJSONStart(buffer, searchStart)
		if jsonStart < 0 {
			break
		}
		jsonEnd := findFallbackJSONEnd(buffer, jsonStart)
		if jsonEnd < 0 {
			remainder := buffer[jsonStart:]
			if len(remainder) > fallbackBufferLimitChars {
				half := fallbackBufferLimitChars / 2
				remainder = remainder[len(remainder)-half:]
			}
			return events, remainder
		}

		jsonStr := buffer[jsonStart : jsonEnd+1]
		var parsed map[string]interface{}
		if err := json.Unmarshal([]byte(jsonStr), &parsed); err != nil {
			searchStart = jsonStart + 1
			continue
		}

		// 分类事件
		if parsed["content"] != nil && parsed["followupPrompt"] == nil {
			content := ""
			if c, ok := parsed["content"].(string); ok {
				content = c
			}
			events = append(events, map[string]interface{}{"kind": "content", "content": content})
		} else if parsed["name"] != nil && parsed["toolUseId"] != nil {
			name, _ := parsed["name"].(string)
			toolUseID, _ := parsed["toolUseId"].(string)
			input, _ := parsed["input"].(string)
			stop, _ := parsed["stop"].(bool)
			events = append(events, map[string]interface{}{
				"kind": "tool_use_start", "name": name,
				"tool_use_id": toolUseID, "input": input, "stop": stop,
			})
		} else if parsed["input"] != nil && parsed["name"] == nil {
			input, _ := parsed["input"].(string)
			events = append(events, map[string]interface{}{"kind": "tool_use_input", "input": input})
		} else if parsed["stop"] != nil && parsed["contextUsagePercentage"] == nil {
			stop, _ := parsed["stop"].(bool)
			events = append(events, map[string]interface{}{"kind": "tool_use_stop", "stop": stop})
		} else if parsed["contextUsagePercentage"] != nil {
			pct, _ := parsed["contextUsagePercentage"].(float64)
			events = append(events, map[string]interface{}{"kind": "context_usage", "context_usage_percentage": pct})
		}

		searchStart = jsonEnd + 1
		lastConsumed = searchStart
	}

	var remainder string
	if lastConsumed > 0 {
		remainder = buffer[lastConsumed:]
	} else {
		remainder = buffer
	}
	if len(remainder) > fallbackBufferLimitChars {
		half := fallbackBufferLimitChars / 2
		remainder = remainder[len(remainder)-half:]
	}
	return events, remainder
}

func init() {
	// 确保 logger/strings 包被引用
	_ = logger.Infof
	_ = strings.Contains
}

// repairPartialJSON 尝试修复不完整的 JSON 字符串（未完成的 tool_use input）
// 策略：补齐缺失的引号和花括号
func repairPartialJSON(s string) string {
	if s == "" {
		return "{}"
	}
	// 已经是合法 JSON
	var test interface{}
	if json.Unmarshal([]byte(s), &test) == nil {
		return s
	}
	// 尝试补齐：计算未闭合的花括号和方括号
	inString := false
	escapeNext := false
	braces := 0
	brackets := 0
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if escapeNext {
			escapeNext = false
			continue
		}
		if ch == '\\' && inString {
			escapeNext = true
			continue
		}
		if ch == '"' {
			inString = !inString
			continue
		}
		if !inString {
			switch ch {
			case '{':
				braces++
			case '}':
				braces--
			case '[':
				brackets++
			case ']':
				brackets--
			}
		}
	}
	// 如果在字符串内，先闭合字符串
	result := s
	if inString {
		result += `"`
	}
	for brackets > 0 {
		result += "]"
		brackets--
	}
	for braces > 0 {
		result += "}"
		braces--
	}
	// 验证修复后是否合法
	if json.Unmarshal([]byte(result), &test) == nil {
		return result
	}
	return "{}"
}
