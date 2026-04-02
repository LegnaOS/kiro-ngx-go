package anthropic

import (
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"kiro-proxy/internal/kiro/model"
	"kiro-proxy/internal/logger"
)

// ContextWindowSize 默认上下文窗口大小（向后兼容），优先使用 GetContextWindowSize(model)
const ContextWindowSize = 200_000

// ---------------------------------------------------------------------------
// SseEvent
// ---------------------------------------------------------------------------

type SseEvent struct {
	Event string
	Data  interface{}
}

func (e *SseEvent) ToSSEString() string {
	bs, _ := json.Marshal(e.Data)
	return "event: " + e.Event + "\ndata: " + string(bs) + "\n\n"
}

// ---------------------------------------------------------------------------
// SseStateManager
// ---------------------------------------------------------------------------

type blockState struct {
	blockType string
	started   bool
	stopped   bool
}

type SseStateManager struct {
	messageStarted   bool
	messageDeltaSent bool
	activeBlocks     map[int]*blockState
	messageEnded     bool
	nextBlockIdx     int
	stopReason       string
	hasToolUse       bool
}

func NewSseStateManager() *SseStateManager {
	return &SseStateManager{
		activeBlocks: make(map[int]*blockState),
	}
}

func (s *SseStateManager) NextBlockIndex() int {
	idx := s.nextBlockIdx
	s.nextBlockIdx++
	return idx
}

func (s *SseStateManager) SetHasToolUse(has bool) { s.hasToolUse = has }
func (s *SseStateManager) SetStopReason(reason string) { s.stopReason = reason }

func (s *SseStateManager) GetStopReason() string {
	if s.stopReason != "" {
		return s.stopReason
	}
	if s.hasToolUse {
		return "tool_use"
	}
	return "end_turn"
}

func (s *SseStateManager) IsBlockOpenOfType(index int, expectedType string) bool {
	b, ok := s.activeBlocks[index]
	if !ok {
		return false
	}
	return b.started && !b.stopped && b.blockType == expectedType
}

func (s *SseStateManager) HasNonThinkingBlocks() bool {
	for _, b := range s.activeBlocks {
		if b.blockType != "thinking" {
			return true
		}
	}
	return false
}

func (s *SseStateManager) HandleMessageStart(eventData map[string]interface{}) *SseEvent {
	s.messageStarted = true
	return &SseEvent{Event: "message_start", Data: map[string]interface{}{"type": "message_start", "message": eventData}}
}

func (s *SseStateManager) HandleContentBlockStart(index int, blockType string, data map[string]interface{}) []*SseEvent {
	var events []*SseEvent

	// Auto-close any open text block before starting a tool_use block
	if blockType == "tool_use" {
		for idx, b := range s.activeBlocks {
			if b.blockType == "text" && b.started && !b.stopped {
				events = append(events, &SseEvent{
					Event: "content_block_stop",
					Data:  map[string]interface{}{"type": "content_block_stop", "index": idx},
				})
				b.stopped = true
			}
		}
	}

	s.activeBlocks[index] = &blockState{blockType: blockType, started: true, stopped: false}

	events = append(events, &SseEvent{
		Event: "content_block_start",
		Data:  map[string]interface{}{"type": "content_block_start", "index": index, "content_block": data},
	})
	return events
}

func (s *SseStateManager) HandleContentBlockDelta(index int, data map[string]interface{}) *SseEvent {
	return &SseEvent{
		Event: "content_block_delta",
		Data:  map[string]interface{}{"type": "content_block_delta", "index": index, "delta": data},
	}
}

func (s *SseStateManager) HandleContentBlockStop(index int) *SseEvent {
	if b, ok := s.activeBlocks[index]; ok {
		b.stopped = true
	}
	return &SseEvent{
		Event: "content_block_stop",
		Data:  map[string]interface{}{"type": "content_block_stop", "index": index},
	}
}

func (s *SseStateManager) GenerateFinalEvents(inputTokens, outputTokens int) []*SseEvent {
	var events []*SseEvent

	// Close all open blocks
	for idx, b := range s.activeBlocks {
		if b.started && !b.stopped {
			events = append(events, &SseEvent{
				Event: "content_block_stop",
				Data:  map[string]interface{}{"type": "content_block_stop", "index": idx},
			})
			b.stopped = true
		}
	}

	stopReason := s.GetStopReason()

	// message_delta
	if !s.messageDeltaSent {
		s.messageDeltaSent = true
		events = append(events, &SseEvent{
			Event: "message_delta",
			Data: map[string]interface{}{
				"type":  "message_delta",
				"delta": map[string]interface{}{"stop_reason": stopReason, "stop_sequence": nil},
				"usage": map[string]interface{}{"input_tokens": inputTokens, "output_tokens": outputTokens, "cache_creation_input_tokens": 0, "cache_read_input_tokens": 0},
			},
		})
	}

	// message_stop
	if !s.messageEnded {
		s.messageEnded = true
		events = append(events, &SseEvent{
			Event: "message_stop",
			Data:  map[string]interface{}{"type": "message_stop"},
		})
	}

	return events
}

// ---------------------------------------------------------------------------
// StreamContext
// ---------------------------------------------------------------------------

type StreamContext struct {
	StateManager                *SseStateManager
	Model                       string
	MessageID                   string
	InputTokens                 int
	ContextInputTokens          *int
	ContextTotalTokens          *int
	OutputTokens                int
	ToolBlockIndices            map[string]int
	ThinkingEnabled             bool
	thinkingBuf                 strings.Builder
	InThinkingBlock             bool
	ThinkingExtracted           bool
	ThinkingBlockIndex          *int
	TextBlockIndex              *int
	stripThinkingLeadingNewline bool
	accumulatedTextParts        []string
	WebSearchToolUses           []map[string]interface{}
	toolJSONBuffers             map[string]string
	toolNames                   map[string]string
	lastAssistantContent        *string
	toolNameReverseMap          map[string]string // short → original，还原 Kiro 返回的缩短名称
	contextWindowSize           int               // 动态上下文窗口大小，按模型决定
}

func NewStreamContext(mdl string, inputTokens int, thinkingEnabled bool) *StreamContext {
	return &StreamContext{
		StateManager:       NewSseStateManager(),
		Model:              mdl,
		MessageID:          "msg_" + uuid.New().String(),
		InputTokens:        inputTokens,
		ToolBlockIndices:   make(map[string]int),
		ThinkingEnabled:    thinkingEnabled,
		toolJSONBuffers:    make(map[string]string),
		toolNames:          make(map[string]string),
		toolNameReverseMap: make(map[string]string),
		contextWindowSize:  GetContextWindowSize(mdl),
	}
}

// SetToolNameMap 设置工具名反向映射（short → original）
func (sc *StreamContext) SetToolNameMap(m map[string]string) {
	if m != nil {
		sc.toolNameReverseMap = m
	}
}

func (sc *StreamContext) AccumulatedText() string {
	return strings.Join(sc.accumulatedTextParts, "")
}

func (sc *StreamContext) CreateMessageStartEvent() map[string]interface{} {
	return map[string]interface{}{
		"id":           sc.MessageID,
		"type":         "message",
		"role":         "assistant",
		"content":      []interface{}{},
		"model":        sc.Model,
		"stop_reason":  nil,
		"stop_sequence": nil,
		"usage": map[string]interface{}{
			"input_tokens":                sc.InputTokens,
			"output_tokens":               0,
			"cache_creation_input_tokens": 0,
			"cache_read_input_tokens":     0,
		},
	}
}

func (sc *StreamContext) GenerateInitialEvents() []*SseEvent {
	var events []*SseEvent

	msgStart := sc.StateManager.HandleMessageStart(sc.CreateMessageStartEvent())
	events = append(events, msgStart)

	if !sc.ThinkingEnabled {
		// Create initial text block
		idx := sc.StateManager.NextBlockIndex()
		idxCopy := idx
		sc.TextBlockIndex = &idxCopy
		blockEvents := sc.StateManager.HandleContentBlockStart(idx, "text", map[string]interface{}{"type": "text", "text": ""})
		events = append(events, blockEvents...)
	}

	return events
}

func (sc *StreamContext) ProcessKiroEvent(event interface{}) []*SseEvent {
	switch e := event.(type) {
	case model.AssistantResponseEvent:
		return sc.processAssistantResponse(e.Content)
	case model.ToolUseEvent:
		return sc.processToolUse(e)
	case model.ContextUsageEvent:
		if e.ContextUsagePercentage > 0 {
			total := int(float64(sc.contextWindowSize) * e.ContextUsagePercentage / 100.0)
			totalCopy := total
			sc.ContextTotalTokens = &totalCopy
			inputCopy := total - sc.OutputTokens
			if inputCopy < 0 {
				inputCopy = 0
			}
			sc.ContextInputTokens = &inputCopy
			if e.ContextUsagePercentage >= 100.0 {
				sc.StateManager.SetStopReason("model_context_window_exceeded")
			}
		}
		return nil
	case map[string]interface{}:
		eventType, _ := e["type"].(string)
		switch eventType {
		case string(model.EventTypeAssistantResponse):
			ae := model.AssistantResponseEventFromDict(e)
			return sc.processAssistantResponse(ae.Content)
		case string(model.EventTypeToolUse):
			te := model.ToolUseEventFromDict(e)
			return sc.processToolUse(te)
		case string(model.EventTypeContextUsage):
			ce := model.ContextUsageEventFromDict(e)
			if ce.ContextUsagePercentage > 0 {
				total := int(float64(sc.contextWindowSize) * ce.ContextUsagePercentage / 100.0)
				totalCopy := total
				sc.ContextTotalTokens = &totalCopy
				inputCopy := total - sc.OutputTokens
				if inputCopy < 0 {
					inputCopy = 0
				}
				sc.ContextInputTokens = &inputCopy
				if ce.ContextUsagePercentage >= 100.0 {
					sc.StateManager.SetStopReason("model_context_window_exceeded")
				}
			}
		case "exception":
			// ContentLengthExceededException → max_tokens
			if e["exception_type"] == "ContentLengthExceededException" {
				sc.StateManager.SetStopReason("max_tokens")
			}
		}
		return nil
	}
	return nil
}

func (sc *StreamContext) processAssistantResponse(content string) []*SseEvent {
	if content == "" {
		return nil
	}
	// 去重：跳过与上次完全相同的 assistant content（Kiro 偶尔重复发送）
	if sc.lastAssistantContent != nil && *sc.lastAssistantContent == content {
		return nil
	}
	s := content
	sc.lastAssistantContent = &s
	sc.OutputTokens += EstimateTokens(content)

	if sc.ThinkingEnabled {
		return sc.processContentWithThinking(content)
	}
	return sc.createTextDeltaEvents(content)
}

func (sc *StreamContext) processContentWithThinking(content string) []*SseEvent {
	var events []*SseEvent

	sc.thinkingBuf.WriteString(content)

	for {
		buf := sc.thinkingBuf.String()
		if !sc.InThinkingBlock && !sc.ThinkingExtracted {
			// Look for <thinking> start tag
			startIdx := FindRealThinkingStartTag(buf)
			if startIdx == -1 {
				// No thinking tag found yet; if buffer is large enough, flush as text
				// Keep a small tail in case the tag spans chunks
				const safeLen = 20
				if len(buf) > safeLen {
					cutPoint := len(buf) - safeLen
					// 确保切点落在 UTF-8 字符边界上，避免切断多字节字符导致乱码
					for cutPoint > 0 && !utf8.RuneStart(buf[cutPoint]) {
						cutPoint--
					}
					if cutPoint == 0 {
						break
					}
					flush := buf[:cutPoint]
					tail := buf[cutPoint:]
					sc.thinkingBuf.Reset()
					sc.thinkingBuf.WriteString(tail)
					events = append(events, sc.createTextDeltaEvents(flush)...)
				}
				break
			}

			// Flush any text before the thinking tag
			if startIdx > 0 {
				pre := buf[:startIdx]
				events = append(events, sc.createTextDeltaEvents(pre)...)
			}
			sc.thinkingBuf.Reset()
			sc.thinkingBuf.WriteString(buf[startIdx+len("<thinking>"):])
			sc.InThinkingBlock = true
			continue
		}

		if sc.InThinkingBlock {
			endIdx := FindRealThinkingEndTag(buf)
			if endIdx == -1 {
				// Check if end tag might be at buffer end (partial)
				partialIdx := findRealThinkingEndTagAtBufferEnd(buf)
				if partialIdx == -1 {
					// Stream all buffered content as thinking delta
					if buf != "" {
						sc.thinkingBuf.Reset()
						events = append(events, sc.streamThinkingContent(buf)...)
					}
				}
				// else: hold the partial end tag in buffer
				break
			}

			// Found complete end tag
			thinkingContent := buf[:endIdx]
			// Skip </thinking>\n\n
			afterEnd := buf[endIdx+len("</thinking>"):]
			// Strip leading \n\n
			afterEnd = strings.TrimPrefix(afterEnd, "\n\n")
			sc.thinkingBuf.Reset()
			sc.thinkingBuf.WriteString(afterEnd)

			// Stream remaining thinking content
			if thinkingContent != "" {
				events = append(events, sc.streamThinkingContent(thinkingContent)...)
			}

			// Close thinking block
			if sc.ThinkingBlockIndex != nil {
				events = append(events, sc.StateManager.HandleContentBlockStop(*sc.ThinkingBlockIndex))
				sc.ThinkingBlockIndex = nil
			}

			sc.InThinkingBlock = false
			sc.ThinkingExtracted = true
			sc.stripThinkingLeadingNewline = true

			// Continue loop to process remaining buffer as text
			continue
		}

		break
	}

	// After thinking extracted, flush remaining buffer as text
	if sc.ThinkingExtracted && !sc.InThinkingBlock && sc.thinkingBuf.Len() > 0 {
		text := sc.thinkingBuf.String()
		sc.thinkingBuf.Reset()
		if sc.stripThinkingLeadingNewline {
			text = strings.TrimPrefix(text, "\n")
			sc.stripThinkingLeadingNewline = false
		}
		if text != "" {
			events = append(events, sc.createTextDeltaEvents(text)...)
		}
	}

	return events
}

// streamThinkingContent streams thinking content, creating the thinking block if needed.
func (sc *StreamContext) streamThinkingContent(thinking string) []*SseEvent {
	var events []*SseEvent

	if sc.ThinkingBlockIndex == nil {
		idx := sc.StateManager.NextBlockIndex()
		idxCopy := idx
		sc.ThinkingBlockIndex = &idxCopy
		blockEvents := sc.StateManager.HandleContentBlockStart(idx, "thinking", map[string]interface{}{"type": "thinking", "thinking": ""})
		events = append(events, blockEvents...)
	}

	events = append(events, sc.createThinkingDelta(*sc.ThinkingBlockIndex, thinking))
	return events
}

func (sc *StreamContext) createTextDeltaEvents(text string) []*SseEvent {
	if text == "" {
		return nil
	}

	sc.accumulatedTextParts = append(sc.accumulatedTextParts, text)

	var events []*SseEvent

	if sc.TextBlockIndex == nil {
		idx := sc.StateManager.NextBlockIndex()
		idxCopy := idx
		sc.TextBlockIndex = &idxCopy
		blockEvents := sc.StateManager.HandleContentBlockStart(idx, "text", map[string]interface{}{"type": "text", "text": ""})
		events = append(events, blockEvents...)
	}

	events = append(events, sc.StateManager.HandleContentBlockDelta(*sc.TextBlockIndex, map[string]interface{}{
		"type":  "text_delta",
		"text":  text,
	}))
	return events
}

func (sc *StreamContext) createThinkingDelta(index int, thinking string) *SseEvent {
	return sc.StateManager.HandleContentBlockDelta(index, map[string]interface{}{
		"type":    "thinking_delta",
		"thinking": thinking,
	})
}

func (sc *StreamContext) processToolUse(toolUse model.ToolUseEvent) []*SseEvent {
	var events []*SseEvent

	id := toolUse.ToolUseID
	if id == "" {
		id = "toolu_" + uuid.New().String()
	}

	// Track tool name
	if toolUse.Name != "" {
		sc.toolNames[id] = toolUse.Name
	}

	// Buffer JSON fragments
	sc.toolJSONBuffers[id] += toolUse.Input

	if !toolUse.Stop {
		return nil
	}

	// Tool use complete - emit events
	sc.StateManager.SetHasToolUse(true)

	name := sc.toolNames[id]
	// 反向映射：将 Kiro 返回的缩短名称还原为客户端期望的原始名称
	if original, ok := sc.toolNameReverseMap[name]; ok {
		name = original
	}
	inputJSON := sc.toolJSONBuffers[id]
	delete(sc.toolJSONBuffers, id)
	delete(sc.toolNames, id)

	// Parse input JSON
	var inputObj interface{}
	if inputJSON != "" {
		if err := json.Unmarshal([]byte(inputJSON), &inputObj); err != nil {
			inputObj = map[string]interface{}{}
		}
	} else {
		inputObj = map[string]interface{}{}
	}

	// Track web search tool uses
	if name == "web_search" || strings.Contains(name, "search") {
		sc.WebSearchToolUses = append(sc.WebSearchToolUses, map[string]interface{}{
			"id":    id,
			"name":  name,
			"input": inputObj,
		})
	}

	idx, exists := sc.ToolBlockIndices[id]
	if !exists {
		idx = sc.StateManager.NextBlockIndex()
		sc.ToolBlockIndices[id] = idx
	}

	blockEvents := sc.StateManager.HandleContentBlockStart(idx, "tool_use", map[string]interface{}{
		"type":  "tool_use",
		"id":    id,
		"name":  name,
		"input": map[string]interface{}{},
	})
	events = append(events, blockEvents...)

	// Emit input_json_delta
	events = append(events, sc.StateManager.HandleContentBlockDelta(idx, map[string]interface{}{
		"type":        "input_json_delta",
		"partial_json": inputJSON,
	}))

	events = append(events, sc.StateManager.HandleContentBlockStop(idx))

	return events
}

func (sc *StreamContext) GenerateFinalEvents() []*SseEvent {
	// Flush any remaining thinking buffer as text
	var extra []*SseEvent
	if sc.ThinkingEnabled && sc.thinkingBuf.Len() > 0 {
		remaining := sc.thinkingBuf.String()
		sc.thinkingBuf.Reset()
		if sc.InThinkingBlock {
			extra = append(extra, sc.streamThinkingContent(remaining)...)
			if sc.ThinkingBlockIndex != nil {
				extra = append(extra, sc.StateManager.HandleContentBlockStop(*sc.ThinkingBlockIndex))
				sc.ThinkingBlockIndex = nil
			}
		} else {
			text := remaining
			if sc.stripThinkingLeadingNewline {
				text = strings.TrimPrefix(text, "\n")
			}
			if text != "" {
				extra = append(extra, sc.createTextDeltaEvents(text)...)
			}
		}
	}

	// 仅有 thinking 块时（无文本块），补发空文本块并设置 max_tokens（与 Python 版本一致）
	if sc.ThinkingEnabled && sc.ThinkingBlockIndex != nil && !sc.StateManager.HasNonThinkingBlocks() {
		sc.StateManager.SetStopReason("max_tokens")
		extra = append(extra, sc.createTextDeltaEvents(" ")...)
	}

	// 未完成的 tool_use 在流末尾收尾输出（与 Python 版本 A2 风格一致）
	for tid, inputJSON := range sc.toolJSONBuffers {
		toolName := sc.toolNames[tid]
		if toolName == "" {
			toolName = "unknown_tool"
		}
		// 反向映射还原
		if original, ok := sc.toolNameReverseMap[toolName]; ok {
			toolName = original
		}
		logger.Warnf("检测到未完成 tool_use，按 A2 风格收尾输出: tool_use_id=%s, name=%s, pending_input_len=%d", tid, toolName, len(inputJSON))
		// 尝试修复不完整的 JSON
		if inputJSON != "" {
			inputJSON = repairPartialJSON(inputJSON)
		}
		sc.StateManager.SetHasToolUse(true)
		blockIdx, exists := sc.ToolBlockIndices[tid]
		if !exists {
			blockIdx = sc.StateManager.NextBlockIndex()
			sc.ToolBlockIndices[tid] = blockIdx
		}
		extra = append(extra, sc.StateManager.HandleContentBlockStart(blockIdx, "tool_use", map[string]interface{}{
			"type":  "tool_use",
			"id":    tid,
			"name":  toolName,
			"input": map[string]interface{}{},
		})...)
		if inputJSON != "" {
			extra = append(extra, sc.StateManager.HandleContentBlockDelta(blockIdx, map[string]interface{}{
				"type":        "input_json_delta",
				"partial_json": inputJSON,
			}))
		}
		extra = append(extra, sc.StateManager.HandleContentBlockStop(blockIdx))
		delete(sc.toolJSONBuffers, tid)
		delete(sc.toolNames, tid)
		delete(sc.ToolBlockIndices, tid)
	}

	finalEvents := sc.StateManager.GenerateFinalEvents(sc.ResolveInputTokens(), sc.OutputTokens)
	return append(extra, finalEvents...)
}

func (sc *StreamContext) ResolveInputTokens() int {
	if sc.ContextInputTokens != nil {
		return *sc.ContextInputTokens
	}
	return sc.InputTokens
}

// ---------------------------------------------------------------------------
// BufferedStreamContext
// ---------------------------------------------------------------------------

type BufferedStreamContext struct {
	Inner                  *StreamContext
	EventBuffer            []*SseEvent
	EstimatedInputTokens   int
	initialEventsGenerated bool
}

func NewBufferedStreamContext(mdl string, estimatedInputTokens int, thinkingEnabled bool) *BufferedStreamContext {
	return &BufferedStreamContext{
		Inner:                NewStreamContext(mdl, estimatedInputTokens, thinkingEnabled),
		EstimatedInputTokens: estimatedInputTokens,
	}
}

func (b *BufferedStreamContext) ProcessAndBuffer(event interface{}) {
	if !b.initialEventsGenerated {
		b.initialEventsGenerated = true
		b.EventBuffer = append(b.EventBuffer, b.Inner.GenerateInitialEvents()...)
	}
	events := b.Inner.ProcessKiroEvent(event)
	b.EventBuffer = append(b.EventBuffer, events...)
}

func (b *BufferedStreamContext) GetWebSearchToolUses() []map[string]interface{} {
	return b.Inner.WebSearchToolUses
}

func (b *BufferedStreamContext) FinishAndGetAllEvents() []*SseEvent {
	if !b.initialEventsGenerated {
		b.initialEventsGenerated = true
		b.EventBuffer = append(b.EventBuffer, b.Inner.GenerateInitialEvents()...)
	}

	finalEvents := b.Inner.GenerateFinalEvents()
	b.EventBuffer = append(b.EventBuffer, finalEvents...)

	// Correct input_tokens in message_start event
	actualInputTokens := b.Inner.ResolveInputTokens()
	for _, e := range b.EventBuffer {
		if e.Event == "message_start" {
			if data, ok := e.Data.(map[string]interface{}); ok {
				if msg, ok := data["message"].(map[string]interface{}); ok {
					if usage, ok := msg["usage"].(map[string]interface{}); ok {
						usage["input_tokens"] = actualInputTokens
					}
				}
			}
			break
		}
	}

	return b.EventBuffer
}

// ---------------------------------------------------------------------------
// Helper functions
// ---------------------------------------------------------------------------

// EstimateTokens estimates token count: ~1.5 chars/token for CJK, ~4 chars/token otherwise.
func EstimateTokens(text string) int {
	if text == "" {
		return 0
	}
	cjkCount := 0
	otherCount := 0
	for _, r := range text {
		if isCJK(r) {
			cjkCount++
		} else {
			otherCount++
		}
	}
	tokens := float64(cjkCount)/1.5 + float64(otherCount)/4.0
	result := int(tokens)
	if result == 0 && utf8.RuneCountInString(text) > 0 {
		result = 1
	}
	return result
}

func isCJK(r rune) bool {
	return (r >= 0x4E00 && r <= 0x9FFF) || // CJK Unified Ideographs
		(r >= 0x3400 && r <= 0x4DBF) || // CJK Extension A
		(r >= 0x20000 && r <= 0x2A6DF) || // CJK Extension B
		(r >= 0xF900 && r <= 0xFAFF) || // CJK Compatibility Ideographs
		(r >= 0x2E80 && r <= 0x2EFF) || // CJK Radicals Supplement
		(r >= 0x3000 && r <= 0x303F) || // CJK Symbols and Punctuation
		(r >= 0xFF00 && r <= 0xFFEF) // Halfwidth and Fullwidth Forms
}

// quoteChars is the set of ASCII characters considered "quote-like" for thinking tag detection.
const quoteChars = "`\"'\\#!@$%^&*()-_=+[]{};:<>,.?/"

func isQuoteChar(r rune) bool {
	if r >= 128 {
		return false
	}
	return strings.ContainsRune(quoteChars, r)
}

// FindRealThinkingStartTag finds the index of <thinking> not surrounded by quote characters.
// Returns -1 if not found.
func FindRealThinkingStartTag(buffer string) int {
	const tag = "<thinking>"
	searchFrom := 0
	for {
		idx := strings.Index(buffer[searchFrom:], tag)
		if idx == -1 {
			return -1
		}
		absIdx := searchFrom + idx

		// Check character before the tag
		if absIdx > 0 {
			prevRune, _ := utf8.DecodeLastRuneInString(buffer[:absIdx])
			if isQuoteChar(prevRune) {
				searchFrom = absIdx + len(tag)
				continue
			}
		}

		// Check character after the tag
		afterIdx := absIdx + len(tag)
		if afterIdx < len(buffer) {
			nextRune, _ := utf8.DecodeRuneInString(buffer[afterIdx:])
			if isQuoteChar(nextRune) {
				searchFrom = absIdx + len(tag)
				continue
			}
		}

		return absIdx
	}
}

// FindRealThinkingEndTag finds </thinking> followed by \n\n.
// Returns the index of </thinking>, or -1 if not found.
func FindRealThinkingEndTag(buffer string) int {
	const tag = "</thinking>"
	searchFrom := 0
	for {
		idx := strings.Index(buffer[searchFrom:], tag)
		if idx == -1 {
			return -1
		}
		absIdx := searchFrom + idx
		afterIdx := absIdx + len(tag)

		// Must be followed by \n\n
		if afterIdx+1 < len(buffer) && buffer[afterIdx] == '\n' && buffer[afterIdx+1] == '\n' {
			return absIdx
		}

		searchFrom = absIdx + len(tag)
	}
}

// findRealThinkingEndTagAtBufferEnd finds </thinking> where the remainder after it is only whitespace
// (i.e., the \n\n might not have arrived yet). Returns -1 if not found.
func findRealThinkingEndTagAtBufferEnd(buffer string) int {
	const tag = "</thinking>"
	searchFrom := 0
	for {
		idx := strings.Index(buffer[searchFrom:], tag)
		if idx == -1 {
			return -1
		}
		absIdx := searchFrom + idx
		afterIdx := absIdx + len(tag)
		remainder := buffer[afterIdx:]

		if strings.TrimRight(remainder, " \t\n\r") == "" {
			return absIdx
		}

		searchFrom = absIdx + len(tag)
	}
}
