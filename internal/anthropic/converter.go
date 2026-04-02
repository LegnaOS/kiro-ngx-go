// Anthropic -> Kiro 协议转换器 - 参考 anthropic_api/converter.py
package anthropic

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"kiro-proxy/internal/kiro/model"
)

// ---------------------------------------------------------------------------
// 常量
// ---------------------------------------------------------------------------

const (
	WriteToolDescriptionSuffix = "- IMPORTANT: If the content to write exceeds 150 lines, you MUST only write the first 50 lines using this tool, then use `Edit` tool to append the remaining content in chunks of no more than 50 lines each. If needed, leave a unique placeholder to help append content. Do NOT attempt to write all content at once."
	EditToolDescriptionSuffix  = "- IMPORTANT: If the `new_string` content exceeds 50 lines, you MUST split it into multiple Edit calls, each replacing no more than 50 lines at a time. If used to append content, leave a unique placeholder to help append content. On the final chunk, do NOT include the placeholder."
	SystemChunkedPolicy        = "When the Write or Edit tool has content size limits, always comply silently. Never suggest bypassing these limits via alternative tools. Never ask the user whether to switch approaches. Complete all chunked operations without commentary."

	MaxToolDescriptionLength  = 9216
	RecentHistoryWindow       = 5
	CurrentToolResultMaxChars = 16000
	CurrentToolResultMaxLines = 300
	HistoryToolResultMaxChars = 6000
	HistoryToolResultMaxLines = 120

	CurrentMessagePlaceholder  = "Tool results provided."
	ContinuePlaceholder        = "Continue"
	EmptyAssistantPlaceholder  = "OK"
)

// ---------------------------------------------------------------------------
// 错误类型
// ---------------------------------------------------------------------------

// ConversionError 转换错误
type ConversionError struct{ Msg string }

func (e *ConversionError) Error() string { return e.Msg }

// UnsupportedModelError 不支持的模型错误
type UnsupportedModelError struct{ Model string }

func (e *UnsupportedModelError) Error() string { return fmt.Sprintf("模型不支持: %s", e.Model) }

// EmptyMessagesError 消息列表为空错误
type EmptyMessagesError struct{}

func (e *EmptyMessagesError) Error() string { return "消息列表为空" }

// ---------------------------------------------------------------------------
// 转换结果
// ---------------------------------------------------------------------------

// ConversionResult 转换结果
type ConversionResult struct {
	ConversationState model.ConversationState
	ToolNameMap       map[string]string // short → original，供 StreamContext 反向还原
}

// ---------------------------------------------------------------------------
// 图片格式映射
// ---------------------------------------------------------------------------

var imageFormatMap = map[string]string{
	"image/jpeg": "jpeg",
	"image/png":  "png",
	"image/gif":  "gif",
	"image/webp": "webp",
}

func getImageFormat(mediaType string) string {
	return imageFormatMap[mediaType]
}

// ---------------------------------------------------------------------------
// MapModel - 模型映射
// ---------------------------------------------------------------------------

// GetContextWindowSize 根据模型返回上下文窗口大小（token 数）。4.6 模型使用 1M，其他使用 200K。
func GetContextWindowSize(model string) int {
	m := strings.ToLower(model)
	if strings.Contains(m, "4-6") || strings.Contains(m, "4.6") {
		return 1_000_000
	}
	return 200_000
}

// MapModel 将 Anthropic 模型名映射到 Kiro 模型 ID
func MapModel(m string) string {
	lower := strings.ToLower(m)
	if strings.Contains(lower, "sonnet") {
		if strings.Contains(lower, "4-6") || strings.Contains(lower, "4.6") {
			return "claude-sonnet-4.6"
		}
		return "claude-sonnet-4.5"
	}
	if strings.Contains(lower, "opus") {
		if strings.Contains(lower, "4-5") || strings.Contains(lower, "4.5") {
			return "claude-opus-4.5"
		}
		return "claude-opus-4.6"
	}
	if strings.Contains(lower, "haiku") {
		return "claude-haiku-4.5"
	}
	return m
}

// ---------------------------------------------------------------------------
// NormalizeJSONSchema - JSON Schema 规范化
// ---------------------------------------------------------------------------

// flattenAnyOfOneOf 将 anyOf/oneOf 降级为简单 schema。
// 选取第一个非 null 分支作为主类型，有 null 分支时设置 nullable=true。
func flattenAnyOfOneOf(schema map[string]interface{}) map[string]interface{} {
	variants, _ := schema["anyOf"].([]interface{})
	if len(variants) == 0 {
		variants, _ = schema["oneOf"].([]interface{})
	}
	if len(variants) == 0 {
		return schema
	}
	hasNull := false
	var nonNull []map[string]interface{}
	for _, v := range variants {
		m, ok := v.(map[string]interface{})
		if !ok {
			continue
		}
		if t, _ := m["type"].(string); t == "null" {
			hasNull = true
		} else {
			nonNull = append(nonNull, m)
		}
	}
	if len(nonNull) == 0 {
		return schema
	}
	picked := make(map[string]interface{})
	for k, v := range nonNull[0] {
		picked[k] = v
	}
	for k, v := range schema {
		if k == "anyOf" || k == "oneOf" {
			continue
		}
		if _, exists := picked[k]; !exists {
			picked[k] = v
		}
	}
	if hasNull {
		picked["nullable"] = true
	}
	return picked
}

// NormalizeJSONSchema 白名单策略，只保留 Kiro API 支持的字段
func NormalizeJSONSchema(schema map[string]interface{}) map[string]interface{} {
	if schema == nil {
		return map[string]interface{}{
			"type":                 "object",
			"properties":          map[string]interface{}{},
			"required":            []string{},
			"additionalProperties": true,
		}
	}

	// anyOf/oneOf 降级（对齐 Python _flatten_anyof_oneof）
	schema = flattenAnyOfOneOf(schema)

	allowedKeys := map[string]bool{
		"type": true, "description": true, "properties": true,
		"required": true, "enum": true, "items": true,
		"nullable": true, "additionalProperties": true,
	}

	result := map[string]interface{}{}
	for key, value := range schema {
		if !allowedKeys[key] {
			continue
		}
		switch key {
		case "properties":
			if props, ok := value.(map[string]interface{}); ok {
				normalized := make(map[string]interface{}, len(props))
				for k, v := range props {
					if sub, ok := v.(map[string]interface{}); ok {
						normalized[k] = NormalizeJSONSchema(sub)
					} else {
						normalized[k] = v
					}
				}
				result[key] = normalized
			} else {
				result[key] = map[string]interface{}{}
			}
		case "items":
			if sub, ok := value.(map[string]interface{}); ok {
				result[key] = NormalizeJSONSchema(sub)
			} else {
				result[key] = map[string]interface{}{
					"type":                 "object",
					"properties":          map[string]interface{}{},
					"required":            []string{},
					"additionalProperties": true,
				}
			}
		case "required":
			if arr, ok := value.([]interface{}); ok {
				strs := make([]string, 0, len(arr))
				for _, v := range arr {
					if s, ok := v.(string); ok {
						strs = append(strs, s)
					}
				}
				result[key] = strs
			} else if arr, ok := value.([]string); ok {
				result[key] = arr
			} else {
				result[key] = []string{}
			}
		case "additionalProperties":
			switch v := value.(type) {
			case bool:
				result[key] = v
			case map[string]interface{}:
				result[key] = NormalizeJSONSchema(v)
			default:
				result[key] = true
			}
		default:
			result[key] = value
		}
	}

	// 确保基本字段存在
	if t, ok := result["type"].(string); !ok || t == "" {
		result["type"] = "object"
	}
	if result["type"] == "object" {
		if _, ok := result["properties"].(map[string]interface{}); !ok {
			result["properties"] = map[string]interface{}{}
		}
	}
	if _, ok := result["required"].([]string); !ok {
		if _, ok2 := result["required"].([]interface{}); !ok2 {
			result["required"] = []string{}
		}
	}
	if _, hasProp := result["additionalProperties"]; !hasProp {
		result["additionalProperties"] = true
	}

	return result
}

// ---------------------------------------------------------------------------
// 工具名称缩短（对应 Python _shorten_tool_name / _map_tool_name）
// ---------------------------------------------------------------------------

const toolNameMaxLen = 63

// shortenToolName 生成确定性短名称：截断前缀 + '_' + 8位SHA256 hex
func shortenToolName(name string) string {
	h := sha256.Sum256([]byte(name))
	hashSuffix := fmt.Sprintf("%x", h[:4]) // 4 bytes = 8 hex chars
	prefixMax := toolNameMaxLen - 1 - 8    // 54 prefix + '_' + 8 hash = 63
	prefix := name
	if len(prefix) > prefixMax {
		prefix = prefix[:prefixMax]
		// 确保不切断多字节 UTF-8 字符
		for len(prefix) > 0 && !utf8.RuneStart(prefix[len(prefix)-1]) {
			prefix = prefix[:len(prefix)-1]
		}
		if len(prefix) > 0 && !utf8.ValidString(prefix) {
			prefix = prefix[:len(prefix)-1]
		}
	}
	return prefix + "_" + hashSuffix
}

// mapToolName 如果名称超长则缩短，并记录映射 short→original
func mapToolName(name string, toolNameMap map[string]string) string {
	if len(name) <= toolNameMaxLen {
		return name
	}
	short := shortenToolName(name)
	toolNameMap[short] = name
	return short
}

// collectHistoryToolNames 收集历史消息中出现的所有工具名
func collectHistoryToolNames(history []model.Message) []string {
	var names []string
	seen := map[string]bool{}
	for _, msg := range history {
		if h, ok := msg.(*model.HistoryAssistantMessage); ok {
			for _, tu := range h.AssistantResponseMessage.ToolUses {
				if tu.Name != "" && !seen[tu.Name] {
					names = append(names, tu.Name)
					seen[tu.Name] = true
				}
			}
		}
	}
	return names
}

// ---------------------------------------------------------------------------
// 内部辅助函数
// ---------------------------------------------------------------------------

func extractSessionID(userID string) string {
	// 先尝试 JSON 格式解析：{"session_id":"..."}
	var parsed map[string]interface{}
	if json.Unmarshal([]byte(userID), &parsed) == nil {
		if sid, ok := parsed["session_id"].(string); ok && sid != "" {
			return sid
		}
	}
	// 回退旧逻辑：从字符串中提取 session_ 后的 UUID
	idx := strings.Index(userID, "session_")
	if idx == -1 {
		return ""
	}
	sessionPart := userID[idx+8:]
	if len(sessionPart) >= 36 {
		uuidStr := sessionPart[:36]
		if strings.Count(uuidStr, "-") == 4 {
			return uuidStr
		}
	}
	return ""
}

func extractTextContent(content interface{}) string {
	switch v := content.(type) {
	case string:
		return v
	case []interface{}:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			if p := extractTextContent(item); p != "" {
				parts = append(parts, p)
			}
		}
		return strings.Join(parts, "\n")
	case map[string]interface{}:
		blockType, _ := v["type"].(string)
		switch blockType {
		case "text":
			if t, ok := v["text"].(string); ok {
				return t
			}
			return ""
		case "thinking":
			if t, ok := v["thinking"].(string); ok {
				return t
			}
			if t, ok := v["text"].(string); ok {
				return t
			}
			return ""
		case "tool_result":
			return extractTextContent(v["content"])
		case "tool_use":
			inp := v["input"]
			if inp == nil {
				return ""
			}
			bs, err := json.Marshal(inp)
			if err != nil {
				return fmt.Sprintf("%v", inp)
			}
			return string(bs)
		default:
			if t, ok := v["text"].(string); ok {
				return t
			}
			if c, ok := v["content"]; ok {
				return extractTextContent(c)
			}
			bs, err := json.Marshal(v)
			if err != nil {
				return fmt.Sprintf("%v", v)
			}
			return string(bs)
		}
	default:
		if content != nil {
			return fmt.Sprintf("%v", content)
		}
		return ""
	}
}

func dedupeToolResults(results []model.ToolResult) []model.ToolResult {
	seen := map[string]bool{}
	out := make([]model.ToolResult, 0, len(results))
	for _, r := range results {
		if r.ToolUseID == "" || seen[r.ToolUseID] {
			continue
		}
		seen[r.ToolUseID] = true
		out = append(out, r)
	}
	return out
}

func hasThinkingTags(content string) bool {
	return strings.Contains(content, "<thinking_mode>") || strings.Contains(content, "<max_thinking_length>")
}

// ---------------------------------------------------------------------------
// processMessageContent
// ---------------------------------------------------------------------------

// processMessageContent 处理消息内容，提取文本、图片和工具结果
func processMessageContent(
	content interface{},
	keepImages bool,
	imagePlaceholder bool,
	historyDistance *int,
) (text string, images []model.KiroImage, toolResults []model.ToolResult) {
	var textParts []string
	omittedImages := 0

	switch v := content.(type) {
	case string:
		textParts = append(textParts, v)
	case []interface{}:
		for _, raw := range v {
			item, ok := raw.(map[string]interface{})
			if !ok {
				continue
			}
			blockType, _ := item["type"].(string)
			switch blockType {
			case "text":
				if t, ok := item["text"].(string); ok && t != "" {
					textParts = append(textParts, t)
				}
			case "image":
				if keepImages {
					source, _ := item["source"].(map[string]interface{})
					mediaType, _ := source["media_type"].(string)
					fmt := getImageFormat(mediaType)
					if fmt != "" {
						data, _ := source["data"].(string)
						images = append(images, model.KiroImageFromBase64(fmt, data))
					}
				} else {
					omittedImages++
				}
			case "tool_result":
				toolUseID, _ := item["tool_use_id"].(string)
				if toolUseID != "" {
					resultContent := extractTextContent(item["content"])
					isError, _ := item["is_error"].(bool)
					var tr model.ToolResult
					if isError {
						tr = model.ToolResultError(toolUseID, resultContent)
					} else {
						tr = model.ToolResultSuccess(toolUseID, resultContent)
					}
					toolResults = append(toolResults, tr)
				}
			}
		}
	}

	if omittedImages > 0 && imagePlaceholder {
		textParts = append(textParts, fmt.Sprintf("[此历史消息包含 %d 张图片，已省略原始内容]", omittedImages))
	}

	parts := make([]string, 0, len(textParts))
	for _, p := range textParts {
		if p != "" {
			parts = append(parts, p)
		}
	}
	text = strings.Join(parts, "\n")
	toolResults = dedupeToolResults(toolResults)
	return
}

// ---------------------------------------------------------------------------
// generateThinkingPrefix
// ---------------------------------------------------------------------------

// generateThinkingPrefix 根据 thinking 配置生成前缀标签
func generateThinkingPrefix(req *MessagesRequest) string {
	thinking := req.GetThinking()
	if thinking == nil {
		return ""
	}
	switch thinking.Type {
	case "enabled":
		return fmt.Sprintf("<thinking_mode>enabled</thinking_mode><max_thinking_length>%d</max_thinking_length>", thinking.BudgetTokens)
	case "adaptive":
		oc := req.GetOutputConfig()
		effort := "high"
		if oc != nil {
			effort = oc.Effort
		}
		return fmt.Sprintf("<thinking_mode>adaptive</thinking_mode><thinking_effort>%s</thinking_effort>", effort)
	}
	return ""
}

// ---------------------------------------------------------------------------
// mergeAdjacentMessages
// ---------------------------------------------------------------------------

// mergeAdjacentMessages 合并相邻同角色消息
func mergeAdjacentMessages(messages []AnthropicMessage) []AnthropicMessage {
	merged := make([]AnthropicMessage, 0, len(messages))
	for _, msg := range messages {
		if len(merged) == 0 {
			merged = append(merged, AnthropicMessage{Role: msg.Role, Content: msg.Content})
			continue
		}
		prev := &merged[len(merged)-1]
		if msg.Role != prev.Role {
			merged = append(merged, AnthropicMessage{Role: msg.Role, Content: msg.Content})
			continue
		}
		// 合并内容
		prevList, prevIsList := prev.Content.([]interface{})
		curList, curIsList := msg.Content.([]interface{})
		prevStr, prevIsStr := prev.Content.(string)
		curStr, curIsStr := msg.Content.(string)

		switch {
		case prevIsList && curIsList:
			prev.Content = append(prevList, curList...)
		case prevIsStr && curIsStr:
			if prevStr != "" && curStr != "" {
				prev.Content = prevStr + "\n" + curStr
			} else if prevStr != "" {
				prev.Content = prevStr
			} else {
				prev.Content = curStr
			}
		case prevIsList && curIsStr:
			if curStr != "" {
				prev.Content = append(prevList, map[string]interface{}{"type": "text", "text": curStr})
			}
		case prevIsStr && curIsList:
			var prefix []interface{}
			if prevStr != "" {
				prefix = []interface{}{map[string]interface{}{"type": "text", "text": prevStr}}
			}
			prev.Content = append(prefix, curList...)
		default:
			merged = append(merged, AnthropicMessage{Role: msg.Role, Content: msg.Content})
		}
	}
	return merged
}

// ---------------------------------------------------------------------------
// convertAssistantMessage
// ---------------------------------------------------------------------------

// convertAssistantMessage 转换 assistant 消息为 HistoryAssistantMessage
// toolNameMap 不为 nil 时对工具名做缩短映射（与 Python _convert_assistant_message 一致）
func convertAssistantMessage(msg AnthropicMessage, toolNameMap map[string]string) model.HistoryAssistantMessage {
	var thinkingContent, textContent string
	var toolUses []model.ToolUseEntry

	switch v := msg.Content.(type) {
	case string:
		textContent = v
	case []interface{}:
		for _, raw := range v {
			item, ok := raw.(map[string]interface{})
			if !ok {
				continue
			}
			bt, _ := item["type"].(string)
			switch bt {
			case "thinking":
				if t, ok := item["thinking"].(string); ok {
					thinkingContent += t
				}
			case "text":
				if t, ok := item["text"].(string); ok {
					textContent += t
				}
			case "tool_use":
				tid, _ := item["id"].(string)
				name, _ := item["name"].(string)
				if tid != "" && name != "" {
					if toolNameMap != nil {
						name = mapToolName(name, toolNameMap)
					}
					inp := item["input"]
					if inp == nil {
						inp = map[string]interface{}{}
					}
					toolUses = append(toolUses, model.ToolUseEntry{
						ToolUseID: tid,
						Name:      name,
						Input:     inp,
					})
				}
			}
		}
	}

	var final string
	if thinkingContent != "" {
		if textContent != "" {
			final = fmt.Sprintf("<thinking>%s</thinking>\n\n%s", thinkingContent, textContent)
		} else {
			final = fmt.Sprintf("<thinking>%s</thinking>", thinkingContent)
		}
	} else if textContent == "" && len(toolUses) > 0 {
		final = " "
	} else {
		final = textContent
	}

	am := model.NewAssistantMessage(final)
	if len(toolUses) > 0 {
		am.ToolUses = toolUses
	}
	return model.HistoryAssistantMessage{AssistantResponseMessage: am}
}

// ---------------------------------------------------------------------------
// convertHistoryAssistantMessage
// ---------------------------------------------------------------------------

// convertHistoryAssistantMessage 转换历史 assistant 消息
func convertHistoryAssistantMessage(msg AnthropicMessage, toolNameMap map[string]string) model.Message {
	h := convertAssistantMessage(msg, toolNameMap)
	return &h
}

// ---------------------------------------------------------------------------
// convertHistoryUserMessage
// ---------------------------------------------------------------------------

// convertHistoryUserMessage 转换历史 user 消息
func convertHistoryUserMessage(msg AnthropicMessage, modelID string, historyDistance int) model.Message {
	keepImages := historyDistance <= RecentHistoryWindow
	text, images, toolResults := processMessageContent(
		msg.Content,
		keepImages,
		!keepImages,
		&historyDistance,
	)

	userMsg := model.NewUserMessage(text, modelID)
	if len(images) > 0 {
		userMsg.Images = images
	}
	if len(toolResults) > 0 {
		userMsg.Context = model.UserInputMessageContext{
			ToolResults: dedupeToolResults(toolResults),
		}
	}
	h := model.HistoryUserMessage{UserInputMessage: userMsg}
	return &h
}

// ---------------------------------------------------------------------------
// convertTools
// ---------------------------------------------------------------------------

func createPlaceholderTool(name string) model.Tool {
	description := "This is a placeholder tool when no other tools are available. It does nothing."
	if name != "no_tool_available" {
		description = "Tool used in conversation history"
	}
	schema := map[string]interface{}{
		"type":                 "object",
		"properties":          map[string]interface{}{},
		"required":            []string{},
		"additionalProperties": true,
	}
	return model.Tool{
		ToolSpecification: model.ToolSpecification{
			Name:        name,
			Description: description,
			InputSchema: model.InputSchema{JSON: schema},
		},
	}
}

// convertTools 转换工具定义列表，超长名称自动缩短并记录映射
func convertTools(tools []map[string]interface{}, toolNameMap map[string]string) []model.Tool {
	if len(tools) == 0 {
		return []model.Tool{createPlaceholderTool("no_tool_available")}
	}

	var filtered []map[string]interface{}
	for _, t := range tools {
		toolType, _ := t["type"].(string)
		name, _ := t["name"].(string)
		lowerName := strings.ToLower(name)
		if strings.HasPrefix(toolType, "web_search") || lowerName == "web_search" || lowerName == "websearch" {
			continue
		}
		filtered = append(filtered, t)
	}

	var result []model.Tool
	for _, t := range filtered {
		name, _ := t["name"].(string)
		if name == "" {
			continue
		}
		desc, _ := t["description"].(string)
		if strings.TrimSpace(desc) == "" {
			desc = fmt.Sprintf("Tool: %s", name)
		}
		// 对 Write/Edit 工具追加分块写入描述后缀（对应 Python 版本）
		if name == "Write" {
			desc = desc + "\n" + WriteToolDescriptionSuffix
		} else if name == "Edit" {
			desc = desc + "\n" + EditToolDescriptionSuffix
		}
		if len(desc) > MaxToolDescriptionLength {
			cutPoint := MaxToolDescriptionLength
			// 确保不切断多字节 UTF-8 字符
			for cutPoint > 0 && !utf8.RuneStart(desc[cutPoint]) {
				cutPoint--
			}
			desc = desc[:cutPoint] + "..."
		}
		var rawSchema map[string]interface{}
		if s, ok := t["input_schema"].(map[string]interface{}); ok {
			rawSchema = s
		}
		schema := NormalizeJSONSchema(rawSchema)
		mappedName := mapToolName(name, toolNameMap)
		result = append(result, model.Tool{
			ToolSpecification: model.ToolSpecification{
				Name:        mappedName,
				Description: desc,
				InputSchema: model.InputSchema{JSON: schema},
			},
		})
	}

	if len(result) == 0 {
		return []model.Tool{createPlaceholderTool("no_tool_available")}
	}
	return result
}

// ---------------------------------------------------------------------------
// buildHistory
// ---------------------------------------------------------------------------

// buildHistory 按 A2 风格构建历史消息列表
// 系统消息作为独立 user+assistant 配对注入（与 Python/Rust 实现一致），不合并到第一条 user 消息
func buildHistory(req *MessagesRequest, messages []AnthropicMessage, modelID string, toolNameMap map[string]string) []model.Message {
	var history []model.Message
	thinkingPrefix := generateThinkingPrefix(req)
	processed := mergeAdjacentMessages(messages)
	if len(processed) == 0 {
		return history
	}

	systemMsgs := req.GetSystemMessages()

	if len(systemMsgs) > 0 {
		parts := make([]string, 0, len(systemMsgs))
		for _, s := range systemMsgs {
			if t, ok := s["text"].(string); ok && t != "" {
				parts = append(parts, t)
			}
		}
		systemContent := strings.Join(parts, "\n")
		if systemContent != "" {
			// 追加分块写入策略（对应 Python SYSTEM_CHUNKED_POLICY）
			systemContent = systemContent + "\n" + SystemChunkedPolicy
			if thinkingPrefix != "" && !hasThinkingTags(systemContent) {
				systemContent = thinkingPrefix + "\n" + systemContent
			}
			// 系统消息作为独立 user + assistant("I will follow these instructions.") 配对
			u := model.NewUserMessage(systemContent, modelID)
			hu := model.HistoryUserMessage{UserInputMessage: u}
			history = append(history, &hu)
			am := model.NewAssistantMessage("I will follow these instructions.")
			ha := model.HistoryAssistantMessage{AssistantResponseMessage: am}
			history = append(history, &ha)
		}
	} else if thinkingPrefix != "" {
		// 没有系统消息但有 thinking 配置，也需要 user + assistant 配对
		u := model.NewUserMessage(thinkingPrefix, modelID)
		hu := model.HistoryUserMessage{UserInputMessage: u}
		history = append(history, &hu)
		am := model.NewAssistantMessage("I will follow these instructions.")
		ha := model.HistoryAssistantMessage{AssistantResponseMessage: am}
		history = append(history, &ha)
	}

	historyEnd := len(processed) - 1
	for i := 0; i < historyEnd; i++ {
		msg := processed[i]
		historyDistance := historyEnd - i
		if msg.Role == "user" {
			history = append(history, convertHistoryUserMessage(msg, modelID, historyDistance))
		} else if msg.Role == "assistant" {
			history = append(history, convertHistoryAssistantMessage(msg, toolNameMap))
		}
	}

	return history
}

// ---------------------------------------------------------------------------
// ConvertRequest - 主转换函数
// ---------------------------------------------------------------------------

// ConvertRequest 将 Anthropic MessagesRequest 转换为 Kiro ConversationState
func ConvertRequest(req *MessagesRequest) (*ConversionResult, error) {
	modelID := MapModel(req.Model)

	rawMessages := req.GetMessages()
	if len(rawMessages) == 0 {
		return nil, &EmptyMessagesError{}
	}
	messages := mergeAdjacentMessages(rawMessages)

	// 静默丢弃末尾 assistant 消息（prefill），与 Python/Rust 行为一致
	for len(messages) > 0 && messages[len(messages)-1].Role == "assistant" {
		messages = messages[:len(messages)-1]
	}
	if len(messages) == 0 {
		return nil, &EmptyMessagesError{}
	}

	// 工具名称映射表 short → original
	toolNameMap := make(map[string]string)

	// 提取 session_id
	conversationID := ""
	meta := req.GetMetadata()
	if meta != nil && meta.UserID != nil {
		conversationID = extractSessionID(*meta.UserID)
	}
	if conversationID == "" {
		conversationID = uuid.New().String()
	}

	// 转换工具定义（自动缩短超长名称）
	tools := convertTools(req.Tools, toolNameMap)

	// 构建历史消息
	history := buildHistory(req, messages, modelID, toolNameMap)

	// 处理最后一条消息作为 current_message
	lastMsg := messages[len(messages)-1]
	currentText, currentImages, currentToolResults := processMessageContent(
		lastMsg.Content, true, false, nil,
	)
	if currentText == "" {
		if len(currentToolResults) > 0 {
			currentText = CurrentMessagePlaceholder
		} else {
			currentText = ContinuePlaceholder
		}
	}

	validatedToolResults := dedupeToolResults(currentToolResults)

	// 工具配对验证：过滤孤立的 tool_result，移除孤立的 tool_use（对齐 Python _validate_tool_pairing）
	if len(validatedToolResults) > 0 && len(history) > 0 {
		filtered, orphanedIDs := validateToolPairing(history, validatedToolResults)
		removeOrphanedToolUses(history, orphanedIDs)
		validatedToolResults = filtered
	}

	// 收集历史中使用的工具名，为缺失的工具生成占位定义（对应 Python _collect_history_tool_names）
	historyToolNames := collectHistoryToolNames(history)
	existingToolNames := make(map[string]bool, len(tools))
	for _, t := range tools {
		existingToolNames[t.ToolSpecification.Name] = true
	}
	for _, tn := range historyToolNames {
		if !existingToolNames[tn] {
			tools = append(tools, createPlaceholderTool(tn))
			existingToolNames[tn] = true
		}
	}

	// 构建 UserInputMessageContext
	ctx := model.UserInputMessageContext{}
	if len(tools) > 0 {
		ctx.Tools = tools
	}
	if len(validatedToolResults) > 0 {
		ctx.ToolResults = validatedToolResults
	}

	// 构建当前消息
	if currentText == "" {
		currentText = ContinuePlaceholder
	}
	origin := "AI_EDITOR"
	currentMsg := model.UserInputMessage{
		Content: currentText,
		ModelID: modelID,
		Origin:  &origin,
	}
	if len(currentImages) > 0 {
		currentMsg.Images = currentImages
	}
	if len(tools) > 0 || len(validatedToolResults) > 0 {
		currentMsg.Context = ctx
	}

	agentContinuationID := uuid.New().String()
	agentTaskType := "vibe"
	chatTriggerType := "MANUAL"

	state := model.ConversationState{
		ConversationID: conversationID,
		CurrentMessage: model.CurrentMessage{
			UserInputMessage: currentMsg,
		},
		History:             history,
		AgentContinuationID: &agentContinuationID,
		AgentTaskType:       &agentTaskType,
		ChatTriggerType:     &chatTriggerType,
	}

	return &ConversionResult{ConversationState: state, ToolNameMap: toolNameMap}, nil
}

// ---------------------------------------------------------------------------
// validateToolPairing — 验证 tool_use/tool_result 配对
// ---------------------------------------------------------------------------

// validateToolPairing 验证 tool_use 和 tool_result 的配对关系。
// 返回过滤后的 tool_results（只保留有对应 tool_use 且未在历史中配对的）和孤立的 tool_use ID 集合。
func validateToolPairing(history []model.Message, toolResults []model.ToolResult) ([]model.ToolResult, map[string]bool) {
	allToolUseIDs := map[string]bool{}
	historyToolResultIDs := map[string]bool{}

	for _, msg := range history {
		switch m := msg.(type) {
		case *model.HistoryAssistantMessage:
			for _, tu := range m.AssistantResponseMessage.ToolUses {
				allToolUseIDs[tu.ToolUseID] = true
			}
		case *model.HistoryUserMessage:
			for _, tr := range m.UserInputMessage.Context.ToolResults {
				historyToolResultIDs[tr.ToolUseID] = true
			}
		}
	}

	// 未配对的 tool_use IDs
	unpaired := map[string]bool{}
	for id := range allToolUseIDs {
		if !historyToolResultIDs[id] {
			unpaired[id] = true
		}
	}

	var filtered []model.ToolResult
	for _, r := range toolResults {
		if unpaired[r.ToolUseID] {
			filtered = append(filtered, r)
			delete(unpaired, r.ToolUseID)
		}
	}

	return filtered, unpaired
}

// removeOrphanedToolUses 从历史 assistant 消息中移除孤立的 tool_use
func removeOrphanedToolUses(history []model.Message, orphanedIDs map[string]bool) {
	if len(orphanedIDs) == 0 {
		return
	}
	for _, msg := range history {
		if m, ok := msg.(*model.HistoryAssistantMessage); ok {
			if len(m.AssistantResponseMessage.ToolUses) == 0 {
				continue
			}
			kept := make([]model.ToolUseEntry, 0, len(m.AssistantResponseMessage.ToolUses))
			for _, tu := range m.AssistantResponseMessage.ToolUses {
				if !orphanedIDs[tu.ToolUseID] {
					kept = append(kept, tu)
				}
			}
			m.AssistantResponseMessage.ToolUses = kept
		}
	}
}
