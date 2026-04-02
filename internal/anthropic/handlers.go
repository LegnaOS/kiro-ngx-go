// Package anthropic Anthropic API 处理器 - 参考 anthropic_api/handlers.py
package anthropic

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"kiro-proxy/internal/apikeys"
	"kiro-proxy/internal/kiro/model"
	"kiro-proxy/internal/kiro/parser"
	"kiro-proxy/internal/logger"
	"kiro-proxy/internal/tokencount"
	"kiro-proxy/internal/tokenusage"
)

// 内置模型列表
var builtinModels = []Model{
	{ID: "claude-sonnet-4-6", Object: "model", Created: 1715000000, OwnedBy: "anthropic", DisplayName: "Claude Sonnet 4.6", Type: "chat", MaxTokens: 64000},
	{ID: "claude-sonnet-4-6-thinking", Object: "model", Created: 1715000000, OwnedBy: "anthropic", DisplayName: "Claude Sonnet 4.6 (Thinking)", Type: "chat", MaxTokens: 64000},
	{ID: "claude-sonnet-4-5", Object: "model", Created: 1715000000, OwnedBy: "anthropic", DisplayName: "Claude Sonnet 4.5", Type: "chat", MaxTokens: 64000},
	{ID: "claude-sonnet-4-5-thinking", Object: "model", Created: 1715000000, OwnedBy: "anthropic", DisplayName: "Claude Sonnet 4.5 (Thinking)", Type: "chat", MaxTokens: 64000},
	{ID: "claude-opus-4-6", Object: "model", Created: 1715000000, OwnedBy: "anthropic", DisplayName: "Claude Opus 4.6", Type: "chat", MaxTokens: 32000},
	{ID: "claude-opus-4-6-thinking", Object: "model", Created: 1715000000, OwnedBy: "anthropic", DisplayName: "Claude Opus 4.6 (Thinking)", Type: "chat", MaxTokens: 32000},
	{ID: "claude-opus-4-5", Object: "model", Created: 1715000000, OwnedBy: "anthropic", DisplayName: "Claude Opus 4.5", Type: "chat", MaxTokens: 32000},
	{ID: "claude-opus-4-5-thinking", Object: "model", Created: 1715000000, OwnedBy: "anthropic", DisplayName: "Claude Opus 4.5 (Thinking)", Type: "chat", MaxTokens: 32000},
	{ID: "claude-haiku-4-5", Object: "model", Created: 1715000000, OwnedBy: "anthropic", DisplayName: "Claude Haiku 4.5", Type: "chat", MaxTokens: 32000},
	{ID: "claude-haiku-4-5-thinking", Object: "model", Created: 1715000000, OwnedBy: "anthropic", DisplayName: "Claude Haiku 4.5 (Thinking)", Type: "chat", MaxTokens: 32000},
}

// HandleGetModels GET /v1/models
func HandleGetModels(w http.ResponseWriter, r *http.Request, state *AppState) {
	logger.Debugf("Received GET /v1/models request")
	writeJSON(w, 200, ModelsResponse{Object: "list", Data: builtinModels})
}

// HandleCountTokens POST /v1/messages/count_tokens
func HandleCountTokens(w http.ResponseWriter, r *http.Request, state *AppState) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, 400, NewErrorResponse("invalid_request_error", "读取请求体失败"))
		return
	}

	var req CountTokensRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, 400, NewErrorResponse("invalid_request_error", "请求体解析失败"))
		return
	}

	total := tokencount.CountAllTokens(req.Model, toSystemMaps(req.System), req.Messages, req.Tools, nil, nil)
	if total < 1 {
		total = 1
	}
	writeJSON(w, 200, CountTokensResponse{InputTokens: total})
}

// overrideThinkingFromModelName 当模型名含 -thinking 后缀时自动启用 thinking 配置
// 4.6 模型使用 adaptive 类型，其他模型使用 enabled 类型
func overrideThinkingFromModelName(req *MessagesRequest) {
	modelLower := strings.ToLower(req.Model)
	if !strings.Contains(modelLower, "thinking") {
		return
	}
	// 4.6 模型支持 adaptive thinking
	is46 := strings.Contains(modelLower, "4-6") || strings.Contains(modelLower, "4.6")
	thinkingType := "enabled"
	if is46 {
		thinkingType = "adaptive"
	}
	logger.Infof("模型名包含 thinking 后缀，覆写 thinking 配置: type=%s, is_4_6=%v", thinkingType, is46)
	thinking := map[string]interface{}{
		"type":          thinkingType,
		"budget_tokens": float64(20000),
	}
	req.ThinkingRaw = &thinking
	// 4.6 模型同时设置 output_config effort=high
	if is46 {
		oc := map[string]interface{}{"effort": "high"}
		req.OutputConfig = &oc
	}
}

// HandlePostMessages POST /v1/messages 和 POST /cc/v1/messages
func HandlePostMessages(w http.ResponseWriter, r *http.Request, state *AppState, useBuffered bool) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, 400, NewErrorResponse("invalid_request_error", "读取请求体失败"))
		return
	}

	var req MessagesRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, 400, NewErrorResponse("invalid_request_error", "请求体解析失败"))
		return
	}

	logger.Infof("Received POST messages: model=%s, stream=%v, msgs=%d", req.Model, req.Stream, len(req.Messages))

	// 模型名含 -thinking 后缀时自动覆写 thinking 配置
	overrideThinkingFromModelName(&req)

	// 转换请求
	result, err := ConvertRequest(&req)
	if err != nil {
		switch e := err.(type) {
		case *UnsupportedModelError:
			writeJSON(w, 400, NewErrorResponse("invalid_request_error", fmt.Sprintf("模型不支持: %s", e.Model)))
		case *EmptyMessagesError:
			writeJSON(w, 400, NewErrorResponse("invalid_request_error", "消息列表为空"))
		default:
			writeJSON(w, 400, NewErrorResponse("invalid_request_error", err.Error()))
		}
		return
	}

	kiroReq := map[string]interface{}{
		"conversationState": result.ConversationState.ToDict(),
	}
	if state.ProfileArn != "" {
		kiroReq["profileArn"] = state.ProfileArn
	}

	// 主动压缩 + 紧急裁剪（对齐 Python handlers.py）
	CompressHistoryProactive(kiroReq, req.Model)
	PruneHistoryForCapacity(kiroReq, req.Model)

	requestBody, err := json.Marshal(kiroReq)
	if err != nil {
		writeJSON(w, 500, NewErrorResponse("api_error", "序列化请求失败"))
		return
	}

	// 请求大小预检
	if err := ValidateOutboundRequest(requestBody, req.Model); err != nil {
		writeJSON(w, 400, NewErrorResponse("invalid_request_error", err.Error()))
		return
	}

	inputTokens := tokencount.CountAllTokens(req.Model, toSystemMaps(req.System), req.Messages, req.Tools, nil, nil)

	thinking := req.GetThinking()
	thinkingEnabled := thinking != nil && thinking.IsEnabled()

	if req.Stream {
		handleStreamRequest(w, r, state, requestBody, req.Model, inputTokens, thinkingEnabled, useBuffered, result.ToolNameMap)
	} else {
		handleNonStreamRequest(w, r, state, requestBody, req.Model, inputTokens, result.ToolNameMap)
	}
}

// handleNonStreamRequest 处理非流式请求
func handleNonStreamRequest(w http.ResponseWriter, r *http.Request, state *AppState, requestBody []byte, mdl string, inputTokens int, toolNameMap map[string]string) {
	var responseBytes []byte

	err := state.KiroProvider.PostMessages(r.Context(), requestBody, func(resp []byte) error {
		responseBytes = resp
		return nil
	})
	if err != nil {
		mapProviderError(w, err)
		return
	}

	dec := parser.NewEventStreamDecoder(0, 0)
	dec.Feed(responseBytes)

	var textParts []string
	type toolUseEntry struct {
		ID    string
		Name  string
		Input string
	}
	toolJSONParts := map[string][]string{}
	toolMeta := map[string]toolUseEntry{}
	hasToolUse := false
	stopReason := "end_turn"
	var contextTotalTokens *int

	// 严格解码
	var parsedEvents []interface{}
	for _, frame := range dec.DecodeAll() {
		ev := parseKiroFrame(frame)
		if ev != nil {
			parsedEvents = append(parsedEvents, ev)
		}
	}
	// 严格解码无产出时切换到 fallback JSON 解析
	if len(parsedEvents) == 0 {
		fbParser := NewKiroFallbackParser()
		parsedEvents = fbParser.Feed(responseBytes)
		if len(parsedEvents) > 0 {
			logger.Warnf("非流式严格事件解码无产出，切换到 fallback JSON 解析")
		}
	}

	for _, ev := range parsedEvents {
		switch e := ev.(type) {
		case model.AssistantResponseEvent:
			textParts = append(textParts, e.Content)
		case model.ToolUseEvent:
			toolJSONParts[e.ToolUseID] = append(toolJSONParts[e.ToolUseID], e.Input)
			// 只在首次见到该 tool_use_id 时记录 meta（name 只在第一个事件中有值）
			if _, exists := toolMeta[e.ToolUseID]; !exists {
				toolMeta[e.ToolUseID] = toolUseEntry{ID: e.ToolUseID, Name: e.Name}
			}
			// 仅在 stop=true 时才标记 hasToolUse（与 Python 一致）
			if e.Stop {
				hasToolUse = true
			}
		case model.ContextUsageEvent:
			actual := int(e.ContextUsagePercentage * float64(GetContextWindowSize(mdl)) / 100.0)
			contextTotalTokens = &actual
			if e.ContextUsagePercentage >= 100.0 {
				stopReason = "model_context_window_exceeded"
			}
		case map[string]interface{}:
			if etype, _ := e["type"].(string); etype == "exception" {
				if e["exception_type"] == "ContentLengthExceededException" {
					stopReason = "max_tokens"
				}
			}
		}
	}

	if hasToolUse && stopReason == "end_turn" {
		stopReason = "tool_use"
	}

	var content []interface{}
	textContent := strings.Join(textParts, "")

	// Bracket tool call 提取（对齐 Python _extract_bracket_tool_calls）
	bracketCalls, cleanedText := extractBracketToolCalls(textContent)
	if len(bracketCalls) > 0 {
		textContent = cleanedText
		hasToolUse = true
		if stopReason == "end_turn" {
			stopReason = "tool_use"
		}
	}

	if textContent != "" {
		content = append(content, map[string]interface{}{"type": "text", "text": textContent})
	}
	for id, meta := range toolMeta {
		buf := strings.Join(toolJSONParts[id], "")
		var inp interface{}
		if err := json.Unmarshal([]byte(buf), &inp); err != nil {
			// 尝试修复不完整的 JSON
			repaired := repairPartialJSON(buf)
			if err2 := json.Unmarshal([]byte(repaired), &inp); err2 != nil {
				inp = map[string]interface{}{}
			}
		}
		// 反向映射：还原 Kiro 返回的缩短工具名
		name := meta.Name
		if toolNameMap != nil {
			if original, ok := toolNameMap[name]; ok {
				name = original
			}
		}
		content = append(content, map[string]interface{}{
			"type":  "tool_use",
			"id":    meta.ID,
			"name":  name,
			"input": inp,
		})
	}
	// 追加 bracket tool calls
	for _, bc := range bracketCalls {
		content = append(content, bc)
	}

	outputTokens := EstimateTokens(textContent)
	finalInput := inputTokens
	if contextTotalTokens != nil {
		if v := *contextTotalTokens - outputTokens; v > 0 {
			finalInput = v
		} else {
			finalInput = 0
		}
	}

	// 上报 token 用量
	reportTokenUsage(r, mdl, finalInput, outputTokens)

	writeJSON(w, 200, map[string]interface{}{
		"id":            "msg_" + uuid.New().String(),
		"type":          "message",
		"role":          "assistant",
		"content":       content,
		"model":         mdl,
		"stop_reason":   stopReason,
		"stop_sequence": nil,
		"usage": map[string]interface{}{
			"input_tokens":                finalInput,
			"output_tokens":               outputTokens,
			"cache_creation_input_tokens": 0,
			"cache_read_input_tokens":     0,
		},
	})
}

// handleStreamRequest 处理流式请求
func handleStreamRequest(w http.ResponseWriter, r *http.Request, state *AppState, requestBody []byte, mdl string, inputTokens int, thinkingEnabled bool, useBuffered bool, toolNameMap map[string]string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, 500, NewErrorResponse("api_error", "不支持流式响应"))
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	if useBuffered {
		handleBufferedStream(w, flusher, r, state, requestBody, mdl, inputTokens, thinkingEnabled, toolNameMap)
	} else {
		handleDirectStream(w, flusher, r, state, requestBody, mdl, inputTokens, thinkingEnabled, toolNameMap)
	}
}

// handleDirectStream 直接流式输出
func handleDirectStream(w http.ResponseWriter, flusher http.Flusher, r *http.Request, state *AppState, requestBody []byte, mdl string, inputTokens int, thinkingEnabled bool, toolNameMap map[string]string) {
	ctx := NewStreamContext(mdl, inputTokens, thinkingEnabled)
	ctx.SetToolNameMap(toolNameMap)

	// 发送初始事件
	for _, evt := range ctx.GenerateInitialEvents() {
		fmt.Fprint(w, evt.ToSSEString())
	}
	flusher.Flush()

	dec := parser.NewEventStreamDecoder(0, 0)
	fallbackParser := NewKiroFallbackParser()
	fallbackMode := false
	strictNoOutputChunks := 0
	pingTicker := time.NewTicker(time.Duration(globalStreamLimits.PingIntervalSecs) * time.Second)
	if globalStreamLimits.PingIntervalSecs <= 0 {
		pingTicker = time.NewTicker(15 * time.Second)
	}
	defer pingTicker.Stop()

	done := make(chan error, 1)
	chunks := make(chan []byte, 64)
	idlePings := 0 // 空闲 ping 计数器
	streamStarted := false
	earlyRetryAttempts := 1

startStream:
	go func() {
		done <- state.KiroProvider.PostMessagesStream(r.Context(), requestBody, func(chunk []byte) error {
			cp := make([]byte, len(chunk))
			copy(cp, chunk)
			select {
			case chunks <- cp:
				return nil
			case <-r.Context().Done():
				return r.Context().Err()
			}
		})
		close(chunks)
	}()

	for {
		select {
		case chunk, ok := <-chunks:
			if !ok {
				// 流结束
				for _, evt := range ctx.GenerateFinalEvents() {
					fmt.Fprint(w, evt.ToSSEString())
				}
				flusher.Flush()
				reportTokenUsage(r, mdl, ctx.ResolveInputTokens(), ctx.OutputTokens)
				return
			}
			streamStarted = true
			idlePings = 0 // 收到数据，重置空闲计数
			var parsedEvents []interface{}
			if fallbackMode {
				parsedEvents = fallbackParser.Feed(chunk)
			} else {
				dec.Feed(chunk)
				for _, frame := range dec.DecodeAll() {
					ev := parseKiroFrame(frame)
					if ev != nil {
						parsedEvents = append(parsedEvents, ev)
					}
				}
				if len(parsedEvents) > 0 {
					strictNoOutputChunks = 0
					fallbackParser.Reset()
				} else {
					strictNoOutputChunks++
					fbEvents := fallbackParser.Feed(chunk)
					if len(fbEvents) > 0 {
						fallbackMode = true
						parsedEvents = fbEvents
						logger.Warnf("严格事件解码连续 %d 个 chunk 无产出，切换到 fallback JSON 解析", strictNoOutputChunks)
					}
				}
			}
			for _, ev := range parsedEvents {
				for _, sseEvt := range ctx.ProcessKiroEvent(ev) {
					fmt.Fprint(w, sseEvt.ToSSEString())
				}
			}
			flusher.Flush()
		case <-pingTicker.C:
			idlePings++
			if globalStreamLimits.WarnAfterPings > 0 && idlePings >= globalStreamLimits.WarnAfterPings {
				pingInterval := globalStreamLimits.PingIntervalSecs
				if pingInterval <= 0 {
					pingInterval = 15
				}
				maxTimeout := 0
				if globalStreamLimits.MaxIdlePings > 0 {
					maxTimeout = pingInterval * globalStreamLimits.MaxIdlePings
				}
				logger.Warnf("上游流空闲中：连续 %d 次 ping 周期未收到数据（interval=%ds, timeout=%ds）", idlePings, pingInterval, maxTimeout)
			}
			if globalStreamLimits.MaxIdlePings > 0 && idlePings >= globalStreamLimits.MaxIdlePings {
				logger.Errorf("上游流空闲超时：连续 %d 次 ping 周期未收到数据，终止流", idlePings)
				for _, evt := range ctx.GenerateFinalEvents() {
					fmt.Fprint(w, evt.ToSSEString())
				}
				flusher.Flush()
				reportTokenUsage(r, mdl, ctx.ResolveInputTokens(), ctx.OutputTokens)
				return
			}
			fmt.Fprint(w, "event: ping\ndata: {\"type\": \"ping\"}\n\n")
			flusher.Flush()
		case <-r.Context().Done():
			return
		case err := <-done:
			if err != nil {
				// 首个有效数据前失败，尝试重试
				if !streamStarted && earlyRetryAttempts > 0 {
					earlyRetryAttempts--
					logger.Warnf("首个有效事件前流失败，尝试重新建立流: %v", err)
					done = make(chan error, 1)
					chunks = make(chan []byte, 64)
					dec = parser.NewEventStreamDecoder(0, 0)
					idlePings = 0
					goto startStream
				}
				logger.Errorf("流式请求失败: %v", err)
			}
			for _, evt := range ctx.GenerateFinalEvents() {
				fmt.Fprint(w, evt.ToSSEString())
			}
			flusher.Flush()
			reportTokenUsage(r, mdl, ctx.ResolveInputTokens(), ctx.OutputTokens)
			return
		}
	}
}

// handleBufferedStream 缓冲流式输出（用于 /cc/v1/messages）
func handleBufferedStream(w http.ResponseWriter, flusher http.Flusher, r *http.Request, state *AppState, requestBody []byte, mdl string, inputTokens int, thinkingEnabled bool, toolNameMap map[string]string) {
	bctx := NewBufferedStreamContext(mdl, inputTokens, thinkingEnabled)
	bctx.Inner.SetToolNameMap(toolNameMap)

	dec := parser.NewEventStreamDecoder(0, 0)
	fallbackParserBuf := NewKiroFallbackParser()
	fallbackModeBuf := false
	strictNoOutputChunksBuf := 0

	// 使用异步模式以支持空闲超时检测
	pingInterval := globalStreamLimits.PingIntervalSecs
	if pingInterval <= 0 {
		pingInterval = 15
	}
	pingTicker := time.NewTicker(time.Duration(pingInterval) * time.Second)
	defer pingTicker.Stop()

	done := make(chan error, 1)
	chunks := make(chan []byte, 64)
	idlePings := 0
	processedAnyEvents := false
	earlyRetryAttempts := 1

startBufferedStream:
	go func() {
		done <- state.KiroProvider.PostMessagesStream(r.Context(), requestBody, func(chunk []byte) error {
			cp := make([]byte, len(chunk))
			copy(cp, chunk)
			select {
			case chunks <- cp:
				return nil
			case <-r.Context().Done():
				return r.Context().Err()
			}
		})
		close(chunks)
	}()

	timedOut := false
	for !timedOut {
		select {
		case chunk, ok := <-chunks:
			if !ok {
				goto finish
			}
			processedAnyEvents = true
			idlePings = 0
			var parsedEvents []interface{}
			if fallbackModeBuf {
				parsedEvents = fallbackParserBuf.Feed(chunk)
			} else {
				dec.Feed(chunk)
				for _, frame := range dec.DecodeAll() {
					ev := parseKiroFrame(frame)
					if ev != nil {
						parsedEvents = append(parsedEvents, ev)
					}
				}
				if len(parsedEvents) > 0 {
					strictNoOutputChunksBuf = 0
					fallbackParserBuf.Reset()
				} else {
					strictNoOutputChunksBuf++
					fbEvents := fallbackParserBuf.Feed(chunk)
					if len(fbEvents) > 0 {
						fallbackModeBuf = true
						parsedEvents = fbEvents
						logger.Warnf("缓冲流严格事件解码连续 %d 个 chunk 无产出，切换到 fallback JSON 解析", strictNoOutputChunksBuf)
					}
				}
			}
			for _, ev := range parsedEvents {
				bctx.ProcessAndBuffer(ev)
			}
		case <-pingTicker.C:
			idlePings++
			if globalStreamLimits.WarnAfterPings > 0 && idlePings >= globalStreamLimits.WarnAfterPings {
				maxTimeout := 0
				if globalStreamLimits.MaxIdlePings > 0 {
					maxTimeout = pingInterval * globalStreamLimits.MaxIdlePings
				}
				logger.Warnf("上游流空闲中（buffered）：连续 %d 次 ping 周期未收到数据（interval=%ds, timeout=%ds）", idlePings, pingInterval, maxTimeout)
			}
			if globalStreamLimits.MaxIdlePings > 0 && idlePings >= globalStreamLimits.MaxIdlePings {
				logger.Errorf("上游流空闲超时（buffered）：连续 %d 次 ping 周期未收到数据，终止流", idlePings)
				timedOut = true
			}
		case <-r.Context().Done():
			return
		case err := <-done:
			if err != nil {
				// 首个有效数据前失败，尝试重试
				if !processedAnyEvents && earlyRetryAttempts > 0 {
					earlyRetryAttempts--
					logger.Warnf("缓冲流在首个有效事件前失败，尝试重新建立流: %v", err)
					done = make(chan error, 1)
					chunks = make(chan []byte, 64)
					dec = parser.NewEventStreamDecoder(0, 0)
					idlePings = 0
					goto startBufferedStream
				}
				mapProviderError(w, err)
				return
			}
			goto finish
		}
	}

finish:
	for _, evt := range bctx.FinishAndGetAllEvents() {
		fmt.Fprint(w, evt.ToSSEString())
	}
	flusher.Flush()
	reportTokenUsage(r, mdl, bctx.Inner.ResolveInputTokens(), bctx.Inner.OutputTokens)
}

// parseKiroFrame 将解析出的 frame 转换为 Kiro 事件
func parseKiroFrame(frame *parser.Frame) interface{} {
	if frame == nil {
		return nil
	}
	eventType := frame.Headers.EventType()
	if eventType == nil || *eventType == "" {
		return nil
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(frame.Payload, &payload); err != nil {
		return nil
	}

	switch *eventType {
	case string(model.EventTypeAssistantResponse):
		return model.AssistantResponseEventFromDict(payload)
	case string(model.EventTypeToolUse):
		return model.ToolUseEventFromDict(payload)
	case string(model.EventTypeContextUsage):
		return model.ContextUsageEventFromDict(payload)
	default:
		return payload
	}
}

// toSystemMaps 将 system 字段转换为 []map[string]interface{}
func toSystemMaps(system interface{}) []map[string]interface{} {
	if system == nil {
		return nil
	}
	switch v := system.(type) {
	case string:
		if v == "" {
			return nil
		}
		return []map[string]interface{}{{"type": "text", "text": v}}
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

// reportTokenUsage 上报 token 用量到 tokenusage 和 apikeys
func reportTokenUsage(r *http.Request, model string, inputTokens, outputTokens int) {
	if tracker := tokenusage.GetTokenUsageTracker(); tracker != nil {
		tracker.Report(model, inputTokens, outputTokens)
	}
	apiKey := GetApiKeyID(r.Context())
	if apiKey != "" {
		if mgr := apikeys.GetApiKeyManager(); mgr != nil {
			mgr.ReportUsage(apiKey, inputTokens, outputTokens, model)
		}
	}
}

// mapProviderError 将 provider 错误映射为 HTTP 响应
func mapProviderError(w http.ResponseWriter, err error) {
	errStr := err.Error()
	if strings.Contains(errStr, "INVALID_MODEL_ID") {
		writeJSON(w, 400, NewErrorResponse("invalid_request_error", "模型不支持，请选择其他模型。"))
		return
	}
	if strings.Contains(errStr, "CONTENT_LENGTH_EXCEEDS_THRESHOLD") {
		writeJSON(w, 400, NewErrorResponse("invalid_request_error", "Request payload is too large for the upstream Kiro API."))
		return
	}
	if strings.Contains(errStr, "Input is too long") {
		writeJSON(w, 400, NewErrorResponse("invalid_request_error", "Conversation context is too long."))
		return
	}
	logger.Errorf("Kiro API 调用失败: %v", err)
	writeJSON(w, 502, NewErrorResponse("api_error", fmt.Sprintf("上游 API 调用失败: %v", err)))
}

// ---------------------------------------------------------------------------
// Bracket Tool Call 提取 — 解析 [Called xxx with args: {...}] 格式
// ---------------------------------------------------------------------------

// findMatchingSquareBracket 查找匹配的右方括号（处理字符串转义和嵌套）
func findMatchingSquareBracket(text string, start int) int {
	depth := 0
	inString := false
	escapeNext := false
	for i := start; i < len(text); i++ {
		ch := text[i]
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
		if inString {
			continue
		}
		if ch == '[' {
			depth++
		} else if ch == ']' {
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// parseSingleBracketToolCall 解析单个 [Called xxx with args: {...}] 文本
func parseSingleBracketToolCall(segment string) map[string]interface{} {
	// 提取函数名：[Called <name> with args:
	calledIdx := strings.Index(segment, "Called")
	if calledIdx < 0 {
		return nil
	}
	afterCalled := segment[calledIdx+6:]
	afterCalled = strings.TrimLeft(afterCalled, " \t")
	withIdx := strings.Index(strings.ToLower(afterCalled), " with args:")
	if withIdx < 0 {
		return nil
	}
	funcName := strings.TrimSpace(afterCalled[:withIdx])
	if funcName == "" {
		return nil
	}

	// 提取 JSON 参数
	markerLower := " with args:"
	markerPos := strings.Index(strings.ToLower(segment), markerLower)
	if markerPos < 0 {
		return nil
	}
	argsStart := markerPos + len(markerLower)
	argsEnd := strings.LastIndex(segment, "]")
	if argsEnd <= argsStart {
		return nil
	}
	jsonCandidate := strings.TrimSpace(segment[argsStart:argsEnd])
	if jsonCandidate == "" {
		return nil
	}

	var inputObj interface{}
	if err := json.Unmarshal([]byte(jsonCandidate), &inputObj); err != nil {
		inputObj = map[string]interface{}{"raw_arguments": jsonCandidate}
	} else if _, ok := inputObj.(map[string]interface{}); !ok {
		inputObj = map[string]interface{}{"raw_arguments": jsonCandidate}
	}

	return map[string]interface{}{
		"type":  "tool_use",
		"id":    "toolu_bracket_" + uuid.New().String()[:12],
		"name":  funcName,
		"input": inputObj,
	}
}

// extractBracketToolCalls 从响应文本中提取 [Called xxx with args: {...}] 格式的工具调用
// 返回解析出的工具调用列表和清理后的文本
func extractBracketToolCalls(text string) ([]map[string]interface{}, string) {
	if text == "" || !strings.Contains(text, "[Called") {
		return nil, text
	}

	var parsedCalls []map[string]interface{}
	type removeRange struct{ start, end int }
	var ranges []removeRange
	searchFrom := 0

	for {
		startPos := strings.Index(text[searchFrom:], "[Called")
		if startPos < 0 {
			break
		}
		startPos += searchFrom
		endPos := findMatchingSquareBracket(text, startPos)
		if endPos < 0 {
			break
		}
		segment := text[startPos : endPos+1]
		parsed := parseSingleBracketToolCall(segment)
		if parsed != nil {
			parsedCalls = append(parsedCalls, parsed)
			ranges = append(ranges, removeRange{startPos, endPos + 1})
		}
		searchFrom = endPos + 1
	}

	if len(ranges) == 0 {
		return nil, text
	}

	// 从文本中移除已提取的 bracket tool call
	var builder strings.Builder
	cursor := 0
	for _, r := range ranges {
		if cursor < r.start {
			builder.WriteString(text[cursor:r.start])
		}
		cursor = r.end
	}
	if cursor < len(text) {
		builder.WriteString(text[cursor:])
	}
	cleaned := strings.TrimSpace(builder.String())
	return parsedCalls, cleaned
}
