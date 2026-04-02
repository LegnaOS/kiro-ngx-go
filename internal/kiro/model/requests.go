package model

// 请求类型定义 - 参考 src/kiro/model/requests/

import (
	"encoding/json"
)

// ---------------------------------------------------------------------------
// 工具相关类型
// ---------------------------------------------------------------------------

// InputSchema 输入模式（JSON Schema）
type InputSchema struct {
	JSON map[string]interface{} `json:"json"`
}

// NewInputSchema 创建默认 InputSchema
func NewInputSchema() InputSchema {
	return InputSchema{
		JSON: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
	}
}

// InputSchemaFromDict 从字典构造 InputSchema
func InputSchemaFromDict(data map[string]interface{}) InputSchema {
	if j, ok := data["json"].(map[string]interface{}); ok {
		return InputSchema{JSON: j}
	}
	return NewInputSchema()
}

// ToDict 转换为字典
func (s *InputSchema) ToDict() map[string]interface{} {
	return map[string]interface{}{"json": s.JSON}
}

// ToolSpecification 工具规范
type ToolSpecification struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema InputSchema `json:"inputSchema"`
}

// ToolSpecificationFromDict 从字典构造 ToolSpecification
func ToolSpecificationFromDict(data map[string]interface{}) ToolSpecification {
	name, _ := data["name"].(string)
	desc, _ := data["description"].(string)
	var schema InputSchema
	if raw, ok := data["inputSchema"].(map[string]interface{}); ok {
		schema = InputSchemaFromDict(raw)
	} else {
		schema = NewInputSchema()
	}
	return ToolSpecification{Name: name, Description: desc, InputSchema: schema}
}

// ToDict 转换为字典
func (ts *ToolSpecification) ToDict() map[string]interface{} {
	return map[string]interface{}{
		"name":        ts.Name,
		"description": ts.Description,
		"inputSchema": ts.InputSchema.ToDict(),
	}
}

// Tool 工具定义
type Tool struct {
	ToolSpecification ToolSpecification `json:"toolSpecification"`
}

// ToolFromDict 从字典构造 Tool
func ToolFromDict(data map[string]interface{}) Tool {
	if raw, ok := data["toolSpecification"].(map[string]interface{}); ok {
		return Tool{ToolSpecification: ToolSpecificationFromDict(raw)}
	}
	return Tool{ToolSpecification: ToolSpecification{InputSchema: NewInputSchema()}}
}

// ToDict 转换为字典
func (t *Tool) ToDict() map[string]interface{} {
	return map[string]interface{}{
		"toolSpecification": t.ToolSpecification.ToDict(),
	}
}

// ToolResult 工具执行结果
type ToolResult struct {
	ToolUseID string                   `json:"toolUseId"`
	Content   []map[string]interface{} `json:"content"`
	Status    *string                  `json:"status,omitempty"`
	IsError   bool                     `json:"isError,omitempty"`
}

// ToolResultSuccess 创建成功的工具结果
func ToolResultSuccess(toolUseID, content string) ToolResult {
	status := "success"
	return ToolResult{
		ToolUseID: toolUseID,
		Content:   []map[string]interface{}{{"text": content}},
		Status:    &status,
		IsError:   false,
	}
}

// ToolResultError 创建失败的工具结果
func ToolResultError(toolUseID, errorMessage string) ToolResult {
	status := "error"
	return ToolResult{
		ToolUseID: toolUseID,
		Content:   []map[string]interface{}{{"text": errorMessage}},
		Status:    &status,
		IsError:   true,
	}
}

// ToolResultFromDict 从字典构造 ToolResult
func ToolResultFromDict(data map[string]interface{}) ToolResult {
	tr := ToolResult{}
	tr.ToolUseID, _ = data["toolUseId"].(string)
	if rawContent, ok := data["content"].([]interface{}); ok {
		for _, item := range rawContent {
			if m, ok := item.(map[string]interface{}); ok {
				tr.Content = append(tr.Content, m)
			}
		}
	}
	if tr.Content == nil {
		tr.Content = []map[string]interface{}{}
	}
	if v, ok := data["status"].(string); ok {
		tr.Status = &v
	}
	if v, ok := data["isError"].(bool); ok {
		tr.IsError = v
	}
	return tr
}

// ToDict 转换为字典
func (tr *ToolResult) ToDict() map[string]interface{} {
	d := map[string]interface{}{
		"toolUseId": tr.ToolUseID,
		"content":   tr.Content,
	}
	if tr.Status != nil {
		d["status"] = *tr.Status
	}
	if tr.IsError {
		d["isError"] = tr.IsError
	}
	return d
}

// ToolUseEntry 工具使用条目（历史消息中记录工具调用）
type ToolUseEntry struct {
	ToolUseID string      `json:"toolUseId"`
	Name      string      `json:"name"`
	Input     interface{} `json:"input"`
}

// ToolUseEntryFromDict 从字典构造 ToolUseEntry
func ToolUseEntryFromDict(data map[string]interface{}) ToolUseEntry {
	e := ToolUseEntry{}
	e.ToolUseID, _ = data["toolUseId"].(string)
	e.Name, _ = data["name"].(string)
	if v, ok := data["input"]; ok {
		e.Input = v
	} else {
		e.Input = map[string]interface{}{}
	}
	return e
}

// ToDict 转换为字典
func (e *ToolUseEntry) ToDict() map[string]interface{} {
	return map[string]interface{}{
		"toolUseId": e.ToolUseID,
		"name":      e.Name,
		"input":     e.Input,
	}
}

// ---------------------------------------------------------------------------
// 图片类型
// ---------------------------------------------------------------------------

// KiroImageSource 图片数据源
type KiroImageSource struct {
	Bytes string `json:"bytes"`
}

// KiroImageSourceFromDict 从字典构造 KiroImageSource
func KiroImageSourceFromDict(data map[string]interface{}) KiroImageSource {
	b, _ := data["bytes"].(string)
	return KiroImageSource{Bytes: b}
}

// ToDict 转换为字典
func (s *KiroImageSource) ToDict() map[string]interface{} {
	return map[string]interface{}{"bytes": s.Bytes}
}

// KiroImage Kiro 图片
type KiroImage struct {
	Format string          `json:"format"`
	Source KiroImageSource `json:"source"`
}

// KiroImageFromBase64 从 base64 数据创建图片
func KiroImageFromBase64(format, data string) KiroImage {
	return KiroImage{Format: format, Source: KiroImageSource{Bytes: data}}
}

// KiroImageFromDict 从字典构造 KiroImage
func KiroImageFromDict(data map[string]interface{}) KiroImage {
	format, _ := data["format"].(string)
	var source KiroImageSource
	if raw, ok := data["source"].(map[string]interface{}); ok {
		source = KiroImageSourceFromDict(raw)
	}
	return KiroImage{Format: format, Source: source}
}

// ToDict 转换为字典
func (img *KiroImage) ToDict() map[string]interface{} {
	return map[string]interface{}{
		"format": img.Format,
		"source": img.Source.ToDict(),
	}
}

// ---------------------------------------------------------------------------
// 消息上下文
// ---------------------------------------------------------------------------

// UserInputMessageContext 用户输入消息上下文
type UserInputMessageContext struct {
	ToolResults []ToolResult `json:"toolResults,omitempty"`
	Tools       []Tool       `json:"tools,omitempty"`
}

// isDefault 判断是否为默认值（无工具和工具结果）
func (ctx *UserInputMessageContext) isDefault() bool {
	return len(ctx.Tools) == 0 && len(ctx.ToolResults) == 0
}

// UserInputMessageContextFromDict 从字典构造 UserInputMessageContext
func UserInputMessageContextFromDict(data map[string]interface{}) UserInputMessageContext {
	ctx := UserInputMessageContext{}
	if rawResults, ok := data["toolResults"].([]interface{}); ok {
		for _, item := range rawResults {
			if m, ok := item.(map[string]interface{}); ok {
				ctx.ToolResults = append(ctx.ToolResults, ToolResultFromDict(m))
			}
		}
	}
	if rawTools, ok := data["tools"].([]interface{}); ok {
		for _, item := range rawTools {
			if m, ok := item.(map[string]interface{}); ok {
				ctx.Tools = append(ctx.Tools, ToolFromDict(m))
			}
		}
	}
	return ctx
}

// ToDict 转换为字典，空字段不输出
func (ctx *UserInputMessageContext) ToDict() map[string]interface{} {
	d := map[string]interface{}{}
	if len(ctx.ToolResults) > 0 {
		results := make([]interface{}, len(ctx.ToolResults))
		for i := range ctx.ToolResults {
			results[i] = ctx.ToolResults[i].ToDict()
		}
		d["toolResults"] = results
	}
	if len(ctx.Tools) > 0 {
		tools := make([]interface{}, len(ctx.Tools))
		for i := range ctx.Tools {
			tools[i] = ctx.Tools[i].ToDict()
		}
		d["tools"] = tools
	}
	return d
}

// ---------------------------------------------------------------------------
// 用户输入消息（当前消息）
// ---------------------------------------------------------------------------

// UserInputMessage 用户输入消息
type UserInputMessage struct {
	Context UserInputMessageContext `json:"userInputMessageContext"`
	Content string                 `json:"content"`
	ModelID string                 `json:"modelId"`
	Images  []KiroImage            `json:"images,omitempty"`
	Origin  *string                `json:"origin,omitempty"`
}

// NewUserInputMessage 创建用户输入消息
func NewUserInputMessage(content, modelID string) UserInputMessage {
	origin := "AI_EDITOR"
	return UserInputMessage{
		Content: content,
		ModelID: modelID,
		Origin:  &origin,
	}
}

// UserInputMessageFromDict 从字典构造 UserInputMessage
func UserInputMessageFromDict(data map[string]interface{}) UserInputMessage {
	msg := UserInputMessage{}
	msg.Content, _ = data["content"].(string)
	msg.ModelID, _ = data["modelId"].(string)
	if raw, ok := data["userInputMessageContext"].(map[string]interface{}); ok {
		msg.Context = UserInputMessageContextFromDict(raw)
	}
	if rawImages, ok := data["images"].([]interface{}); ok {
		for _, item := range rawImages {
			if m, ok := item.(map[string]interface{}); ok {
				msg.Images = append(msg.Images, KiroImageFromDict(m))
			}
		}
	}
	if v, ok := data["origin"].(string); ok {
		msg.Origin = &v
	}
	return msg
}

// ToDict 转换为字典
func (msg *UserInputMessage) ToDict() map[string]interface{} {
	d := map[string]interface{}{
		"userInputMessageContext": msg.Context.ToDict(),
		"content":                msg.Content,
		"modelId":                msg.ModelID,
	}
	if len(msg.Images) > 0 {
		images := make([]interface{}, len(msg.Images))
		for i := range msg.Images {
			images[i] = msg.Images[i].ToDict()
		}
		d["images"] = images
	}
	if msg.Origin != nil {
		d["origin"] = *msg.Origin
	}
	return d
}

// ---------------------------------------------------------------------------
// 当前消息容器
// ---------------------------------------------------------------------------

// CurrentMessage 当前消息容器
type CurrentMessage struct {
	UserInputMessage UserInputMessage `json:"userInputMessage"`
}

// CurrentMessageFromDict 从字典构造 CurrentMessage
func CurrentMessageFromDict(data map[string]interface{}) CurrentMessage {
	cm := CurrentMessage{}
	if raw, ok := data["userInputMessage"].(map[string]interface{}); ok {
		cm.UserInputMessage = UserInputMessageFromDict(raw)
	}
	return cm
}

// ToDict 转换为字典
func (cm *CurrentMessage) ToDict() map[string]interface{} {
	return map[string]interface{}{
		"userInputMessage": cm.UserInputMessage.ToDict(),
	}
}

// ---------------------------------------------------------------------------
// 历史消息中的用户消息
// ---------------------------------------------------------------------------

// UserMessage 用户消息（历史记录中使用）
type UserMessage struct {
	Content string                 `json:"content"`
	ModelID string                 `json:"modelId"`
	Origin  *string                `json:"origin,omitempty"`
	Images  []KiroImage            `json:"images,omitempty"`
	Context UserInputMessageContext `json:"userInputMessageContext,omitempty"`
}

// NewUserMessage 创建用户消息
func NewUserMessage(content, modelID string) UserMessage {
	origin := "AI_EDITOR"
	return UserMessage{Content: content, ModelID: modelID, Origin: &origin}
}

// UserMessageFromDict 从字典构造 UserMessage
func UserMessageFromDict(data map[string]interface{}) UserMessage {
	msg := UserMessage{}
	msg.Content, _ = data["content"].(string)
	msg.ModelID, _ = data["modelId"].(string)
	if v, ok := data["origin"].(string); ok {
		msg.Origin = &v
	}
	if rawImages, ok := data["images"].([]interface{}); ok {
		for _, item := range rawImages {
			if m, ok := item.(map[string]interface{}); ok {
				msg.Images = append(msg.Images, KiroImageFromDict(m))
			}
		}
	}
	if raw, ok := data["userInputMessageContext"].(map[string]interface{}); ok {
		msg.Context = UserInputMessageContextFromDict(raw)
	}
	return msg
}

// ToDict 转换为字典
func (msg *UserMessage) ToDict() map[string]interface{} {
	d := map[string]interface{}{
		"content": msg.Content,
		"modelId": msg.ModelID,
	}
	if msg.Origin != nil {
		d["origin"] = *msg.Origin
	}
	if len(msg.Images) > 0 {
		images := make([]interface{}, len(msg.Images))
		for i := range msg.Images {
			images[i] = msg.Images[i].ToDict()
		}
		d["images"] = images
	}
	if !msg.Context.isDefault() {
		d["userInputMessageContext"] = msg.Context.ToDict()
	}
	return d
}

// ---------------------------------------------------------------------------
// 历史消息中的助手消息
// ---------------------------------------------------------------------------

// AssistantMessage 助手消息（历史记录中使用）
type AssistantMessage struct {
	Content  string         `json:"content"`
	ToolUses []ToolUseEntry `json:"toolUses,omitempty"`
}

// NewAssistantMessage 创建助手消息
func NewAssistantMessage(content string) AssistantMessage {
	return AssistantMessage{Content: content}
}

// AssistantMessageFromDict 从字典构造 AssistantMessage
func AssistantMessageFromDict(data map[string]interface{}) AssistantMessage {
	msg := AssistantMessage{}
	msg.Content, _ = data["content"].(string)
	if rawUses, ok := data["toolUses"].([]interface{}); ok {
		msg.ToolUses = make([]ToolUseEntry, 0, len(rawUses))
		for _, item := range rawUses {
			if m, ok := item.(map[string]interface{}); ok {
				msg.ToolUses = append(msg.ToolUses, ToolUseEntryFromDict(m))
			}
		}
	}
	// 注意: ToolUses 为 nil 表示字段不存在，空切片表示字段存在但为空
	return msg
}

// ToDict 转换为字典
func (msg *AssistantMessage) ToDict() map[string]interface{} {
	d := map[string]interface{}{"content": msg.Content}
	if msg.ToolUses != nil {
		uses := make([]interface{}, len(msg.ToolUses))
		for i := range msg.ToolUses {
			uses[i] = msg.ToolUses[i].ToDict()
		}
		d["toolUses"] = uses
	}
	return d
}

// ---------------------------------------------------------------------------
// 历史消息包装
// ---------------------------------------------------------------------------

// HistoryUserMessage 历史用户消息
type HistoryUserMessage struct {
	UserInputMessage UserMessage `json:"userInputMessage"`
}

// NewHistoryUserMessage 创建历史用户消息
func NewHistoryUserMessage(content, modelID string) HistoryUserMessage {
	return HistoryUserMessage{UserInputMessage: NewUserMessage(content, modelID)}
}

// HistoryUserMessageFromDict 从字典构造 HistoryUserMessage
func HistoryUserMessageFromDict(data map[string]interface{}) HistoryUserMessage {
	h := HistoryUserMessage{}
	if raw, ok := data["userInputMessage"].(map[string]interface{}); ok {
		h.UserInputMessage = UserMessageFromDict(raw)
	}
	return h
}

// ToDict 转换为字典
func (h *HistoryUserMessage) ToDict() map[string]interface{} {
	return map[string]interface{}{
		"userInputMessage": h.UserInputMessage.ToDict(),
	}
}

// HistoryAssistantMessage 历史助手消息
type HistoryAssistantMessage struct {
	AssistantResponseMessage AssistantMessage `json:"assistantResponseMessage"`
}

// NewHistoryAssistantMessage 创建历史助手消息
func NewHistoryAssistantMessage(content string) HistoryAssistantMessage {
	return HistoryAssistantMessage{AssistantResponseMessage: NewAssistantMessage(content)}
}

// HistoryAssistantMessageFromDict 从字典构造 HistoryAssistantMessage
func HistoryAssistantMessageFromDict(data map[string]interface{}) HistoryAssistantMessage {
	h := HistoryAssistantMessage{}
	if raw, ok := data["assistantResponseMessage"].(map[string]interface{}); ok {
		h.AssistantResponseMessage = AssistantMessageFromDict(raw)
	}
	return h
}

// ToDict 转换为字典
func (h *HistoryAssistantMessage) ToDict() map[string]interface{} {
	return map[string]interface{}{
		"assistantResponseMessage": h.AssistantResponseMessage.ToDict(),
	}
}

// ---------------------------------------------------------------------------
// Message 接口 - 历史消息（用户或助手），使用 untagged 风格序列化
// ---------------------------------------------------------------------------

// Message 历史消息接口
type Message interface {
	ToDict() map[string]interface{}
	isMessage() // 私有方法，限制实现
}

// 确保 HistoryUserMessage 和 HistoryAssistantMessage 实现 Message 接口
func (h *HistoryUserMessage) isMessage()      {}
func (h *HistoryAssistantMessage) isMessage() {}

// MessageUser 创建用户历史消息
func MessageUser(content, modelID string) Message {
	m := NewHistoryUserMessage(content, modelID)
	return &m
}

// MessageAssistant 创建助手历史消息
func MessageAssistant(content string) Message {
	m := NewHistoryAssistantMessage(content)
	return &m
}

// MessageFromDict 从字典解析历史消息，根据键名判断类型
func MessageFromDict(data map[string]interface{}) Message {
	if _, ok := data["userInputMessage"]; ok {
		m := HistoryUserMessageFromDict(data)
		return &m
	}
	if _, ok := data["assistantResponseMessage"]; ok {
		m := HistoryAssistantMessageFromDict(data)
		return &m
	}
	return nil
}

// IsUserMessage 判断是否为用户消息
func IsUserMessage(m Message) bool {
	_, ok := m.(*HistoryUserMessage)
	return ok
}

// IsAssistantMessage 判断是否为助手消息
func IsAssistantMessage(m Message) bool {
	_, ok := m.(*HistoryAssistantMessage)
	return ok
}

// ---------------------------------------------------------------------------
// 对话状态
// ---------------------------------------------------------------------------

// ConversationState 对话状态
type ConversationState struct {
	ConversationID      string   `json:"conversationId"`
	CurrentMessage      CurrentMessage `json:"currentMessage"`
	History             []Message      `json:"-"` // 自定义序列化
	AgentContinuationID *string  `json:"agentContinuationId,omitempty"`
	AgentTaskType       *string  `json:"agentTaskType,omitempty"`
	ChatTriggerType     *string  `json:"chatTriggerType,omitempty"`
}

// ConversationStateFromDict 从字典构造 ConversationState
func ConversationStateFromDict(data map[string]interface{}) ConversationState {
	cs := ConversationState{}
	cs.ConversationID, _ = data["conversationId"].(string)
	if raw, ok := data["currentMessage"].(map[string]interface{}); ok {
		cs.CurrentMessage = CurrentMessageFromDict(raw)
	}
	if rawHistory, ok := data["history"].([]interface{}); ok {
		for _, item := range rawHistory {
			if m, ok := item.(map[string]interface{}); ok {
				msg := MessageFromDict(m)
				if msg != nil {
					cs.History = append(cs.History, msg)
				}
			}
		}
	}
	if v, ok := data["agentContinuationId"].(string); ok {
		cs.AgentContinuationID = &v
	}
	if v, ok := data["agentTaskType"].(string); ok {
		cs.AgentTaskType = &v
	}
	if v, ok := data["chatTriggerType"].(string); ok {
		cs.ChatTriggerType = &v
	}
	return cs
}

// ToDict 转换为字典
func (cs *ConversationState) ToDict() map[string]interface{} {
	d := map[string]interface{}{
		"currentMessage": cs.CurrentMessage.ToDict(),
		"conversationId": cs.ConversationID,
	}
	if cs.AgentContinuationID != nil {
		d["agentContinuationId"] = *cs.AgentContinuationID
	}
	if cs.AgentTaskType != nil {
		d["agentTaskType"] = *cs.AgentTaskType
	}
	if cs.ChatTriggerType != nil {
		d["chatTriggerType"] = *cs.ChatTriggerType
	}
	if len(cs.History) > 0 {
		history := make([]interface{}, len(cs.History))
		for i, m := range cs.History {
			history[i] = m.ToDict()
		}
		d["history"] = history
	}
	return d
}

// ---------------------------------------------------------------------------
// Kiro 请求
// ---------------------------------------------------------------------------

// KiroRequest Kiro API 请求
type KiroRequest struct {
	ConversationState ConversationState `json:"conversationState"`
	ProfileArn        *string           `json:"profileArn,omitempty"`
}

// KiroRequestFromDict 从字典构造 KiroRequest
func KiroRequestFromDict(data map[string]interface{}) KiroRequest {
	req := KiroRequest{}
	if raw, ok := data["conversationState"].(map[string]interface{}); ok {
		req.ConversationState = ConversationStateFromDict(raw)
	}
	if v, ok := data["profileArn"].(string); ok {
		req.ProfileArn = &v
	}
	return req
}

// ToDict 转换为字典
func (req *KiroRequest) ToDict() map[string]interface{} {
	d := map[string]interface{}{
		"conversationState": req.ConversationState.ToDict(),
	}
	if req.ProfileArn != nil {
		d["profileArn"] = *req.ProfileArn
	}
	return d
}

// ToJSON 序列化为 JSON 字符串
func (req *KiroRequest) ToJSON() string {
	bs, err := json.Marshal(req.ToDict())
	if err != nil {
		return "{}"
	}
	return string(bs)
}
