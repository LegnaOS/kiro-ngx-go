// Package anthropic 历史消息裁剪与压缩 — 对齐 Python handlers.py
package anthropic

import (
	"encoding/json"
	"fmt"

	"kiro-proxy/internal/logger"
	"kiro-proxy/internal/tokencount"
)

// ---------------------------------------------------------------------------
// 常量
// ---------------------------------------------------------------------------

const (
	EmergencyHistoryMinMessages = 28
	EmergencyHistoryDropBatch   = 10
	ProactiveCompressRatio      = 0.60
	ProactiveRecentKeep         = 10
	ProactiveToolResultMaxChars = 1500
	ProactiveAssistantMaxChars  = 800
	LocalRequestMaxBytes        = 8 * 1024 * 1024
	LocalRequestMaxChars        = 2_000_000
)

// ---------------------------------------------------------------------------
// 请求预检 (Task 0.2)
// ---------------------------------------------------------------------------

// ValidateOutboundRequest 发送前预检：bytes/chars/tokens 三重限制
func ValidateOutboundRequest(body []byte, model string) error {
	bodyBytes := len(body)
	bodyChars := len([]rune(string(body)))

	if bodyBytes > LocalRequestMaxBytes {
		return fmt.Errorf("Request payload is too large before sending. Reduce large tool results, history, images, or tools.")
	}
	if bodyChars > LocalRequestMaxChars {
		return fmt.Errorf("Request payload text is too large before sending. Reduce large tool results, history, or system prompt.")
	}

	// token 预检
	var payload interface{}
	if err := json.Unmarshal(body, &payload); err == nil {
		metrics := tokencount.EstimateKiroPayloadMetrics(payload)
		limit := int(float64(GetContextWindowSize(model)) * 0.92)
		if metrics.Tokens > limit {
			return fmt.Errorf("Estimated conversation context is too large before sending. Reduce message history, system prompt, or tool definitions.")
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// 辅助：history item 类型判断
// ---------------------------------------------------------------------------

func isUserItem(item map[string]interface{}) bool {
	_, ok := item["userInputMessage"]
	return ok
}

func isAssistantItem(item map[string]interface{}) bool {
	_, ok := item["assistantResponseMessage"]
	return ok
}

// collectToolUseIDsFromItem 从单条 assistant item 提取所有 toolUseId
func collectToolUseIDsFromItem(item map[string]interface{}) map[string]bool {
	ids := map[string]bool{}
	am, _ := item["assistantResponseMessage"].(map[string]interface{})
	if am == nil {
		return ids
	}
	tus, _ := am["toolUses"].([]interface{})
	for _, raw := range tus {
		tu, _ := raw.(map[string]interface{})
		if tu == nil {
			continue
		}
		if tid, _ := tu["toolUseId"].(string); tid != "" {
			ids[tid] = true
		}
	}
	return ids
}

// ---------------------------------------------------------------------------
// 文本截断（保留头尾，中间插入压缩标记）
// ---------------------------------------------------------------------------

func truncateTextMiddle(text string, maxChars int, label string) string {
	runes := []rune(text)
	if len(runes) <= maxChars {
		return text
	}
	half := maxChars / 2
	if half < 1 {
		half = 1
	}
	return string(runes[:half]) +
		fmt.Sprintf("\n[%s: %d chars compressed to %d]\n", label, len(runes), maxChars) +
		string(runes[len(runes)-half:])
}

// ---------------------------------------------------------------------------
// stripOrphanedToolResults 清理 history 首条 user 消息中的孤立 toolResults
// ---------------------------------------------------------------------------

func stripOrphanedToolResults(history []interface{}, orphanedIDs map[string]bool) int {
	if len(history) == 0 || len(orphanedIDs) == 0 {
		return 0
	}
	first, _ := history[0].(map[string]interface{})
	if first == nil {
		return 0
	}
	um, _ := first["userInputMessage"].(map[string]interface{})
	if um == nil {
		return 0
	}
	ctx, _ := um["userInputMessageContext"].(map[string]interface{})
	if ctx == nil {
		return 0
	}
	trs, _ := ctx["toolResults"].([]interface{})
	if len(trs) == 0 {
		return 0
	}
	cleaned := make([]interface{}, 0, len(trs))
	for _, raw := range trs {
		tr, _ := raw.(map[string]interface{})
		if tr != nil {
			tid, _ := tr["toolUseId"].(string)
			if orphanedIDs[tid] {
				continue
			}
		}
		cleaned = append(cleaned, raw)
	}
	removed := len(trs) - len(cleaned)
	if removed > 0 {
		ctx["toolResults"] = cleaned
	}
	return removed
}

// ---------------------------------------------------------------------------
// findPairBoundary 找到 history 开头 count 条附近的完整 user+assistant 配对边界
// ---------------------------------------------------------------------------

func findPairBoundary(history []interface{}, count int) int {
	if count <= 0 {
		return 0
	}
	boundary := count
	if boundary > len(history) {
		boundary = len(history)
	}
	// 向下对齐到偶数（完整 user+assistant 对）
	if boundary%2 != 0 {
		boundary--
	}
	// 确保切割点后面是 user 消息
	for boundary < len(history) {
		item, _ := history[boundary].(map[string]interface{})
		if item != nil && isUserItem(item) {
			break
		}
		boundary++
	}
	return boundary
}

// ---------------------------------------------------------------------------
// dropHistoryHead 从 history 头部安全丢弃消息，返回 (实际丢弃数, orphanedIDs)
// ---------------------------------------------------------------------------

func dropHistoryHead(history *[]interface{}, dropCount int) (int, map[string]bool) {
	boundary := findPairBoundary(*history, dropCount)
	if boundary <= 0 {
		return 0, nil
	}

	orphanedIDs := map[string]bool{}
	for i := 0; i < boundary; i++ {
		item, _ := (*history)[i].(map[string]interface{})
		if item != nil {
			for id := range collectToolUseIDsFromItem(item) {
				orphanedIDs[id] = true
			}
		}
	}

	*history = (*history)[boundary:]
	stripOrphanedToolResults(*history, orphanedIDs)
	return boundary, orphanedIDs
}

// ---------------------------------------------------------------------------
// ValidateHistoryStructure 验证并修复 history 结构合法性
// ---------------------------------------------------------------------------

func ValidateHistoryStructure(history []interface{}) []interface{} {
	if len(history) == 0 {
		return history
	}

	validated := make([]interface{}, 0, len(history))
	expectUser := true
	seenToolUseIDs := map[string]bool{}

	for _, raw := range history {
		item, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}

		isUser := isUserItem(item)
		isAsst := isAssistantItem(item)
		if !isUser && !isAsst {
			continue
		}

		if expectUser && !isUser {
			// 期望 user 但来了 assistant → 跳过
			continue
		}
		if !expectUser && !isAsst {
			// 期望 assistant 但来了 user → 插入空 assistant
			validated = append(validated, map[string]interface{}{
				"assistantResponseMessage": map[string]interface{}{"content": " "},
			})
			seenToolUseIDs = map[string]bool{}
			expectUser = true
		}

		if isUser {
			// 清理 orphaned tool_results
			um, _ := item["userInputMessage"].(map[string]interface{})
			if um != nil {
				ctx, _ := um["userInputMessageContext"].(map[string]interface{})
				if ctx != nil {
					trs, _ := ctx["toolResults"].([]interface{})
					if len(trs) > 0 && len(seenToolUseIDs) > 0 {
						cleaned := make([]interface{}, 0, len(trs))
						for _, raw := range trs {
							tr, _ := raw.(map[string]interface{})
							if tr != nil {
								tid, _ := tr["toolUseId"].(string)
								if tid != "" && !seenToolUseIDs[tid] {
									continue
								}
							}
							cleaned = append(cleaned, raw)
						}
						if len(cleaned) != len(trs) {
							ctx["toolResults"] = cleaned
						}
					}
				}
			}
			validated = append(validated, item)
			expectUser = false
		} else if isAsst {
			for id := range collectToolUseIDsFromItem(item) {
				seenToolUseIDs[id] = true
			}
			validated = append(validated, item)
			expectUser = true
		}
	}

	// 末尾 user 缺 assistant → 丢弃
	if len(validated) > 0 && !expectUser {
		validated = validated[:len(validated)-1]
	}

	return validated
}

// ---------------------------------------------------------------------------
// compressToolResultsInItem 压缩单条 user 消息中的 tool results
// ---------------------------------------------------------------------------

func compressToolResultsInItem(item map[string]interface{}, maxChars int) int {
	um, _ := item["userInputMessage"].(map[string]interface{})
	if um == nil {
		return 0
	}
	ctx, _ := um["userInputMessageContext"].(map[string]interface{})
	if ctx == nil {
		return 0
	}
	trs, _ := ctx["toolResults"].([]interface{})
	if len(trs) == 0 {
		return 0
	}
	compressed := 0
	for _, raw := range trs {
		tr, _ := raw.(map[string]interface{})
		if tr == nil {
			continue
		}
		content, _ := tr["content"].([]interface{})
		if len(content) == 0 {
			continue
		}
		for _, bRaw := range content {
			block, _ := bRaw.(map[string]interface{})
			if block == nil {
				continue
			}
			text, _ := block["text"].(string)
			if len([]rune(text)) > maxChars {
				block["text"] = truncateTextMiddle(text, maxChars, "tool_result")
				compressed++
			}
		}
	}
	return compressed
}

// compressAssistantInItem 压缩单条 assistant 消息内容
func compressAssistantInItem(item map[string]interface{}, maxChars int) bool {
	am, _ := item["assistantResponseMessage"].(map[string]interface{})
	if am == nil {
		return false
	}
	content, _ := am["content"].(string)
	if len([]rune(content)) > maxChars {
		am["content"] = truncateTextMiddle(content, maxChars, "assistant")
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// CompressHistoryProactive 三级主动压缩（对齐 Python _compress_history_proactive）
// ---------------------------------------------------------------------------

// CompressHistoryProactive 在超过 60% 上下文窗口时逐级压缩，避免触发紧急裁剪。
// 返回 (是否做了压缩, error)。
func CompressHistoryProactive(kiroReq map[string]interface{}, model string) (bool, error) {
	limit := int(float64(GetContextWindowSize(model)) * 0.92)
	threshold := int(float64(limit) * ProactiveCompressRatio)

	metrics := tokencount.EstimateKiroPayloadMetrics(kiroReq)
	if metrics.Tokens <= threshold {
		return false, nil
	}

	cs, _ := kiroReq["conversationState"].(map[string]interface{})
	if cs == nil {
		return false, nil
	}
	history, _ := cs["history"].([]interface{})
	if len(history) <= ProactiveRecentKeep {
		return false, nil
	}

	compressible := len(history) - ProactiveRecentKeep
	didCompress := false

	// === Level 1: 压缩旧 tool_results ===
	trCompressed := 0
	for i := 0; i < compressible; i++ {
		item, _ := history[i].(map[string]interface{})
		if item != nil {
			trCompressed += compressToolResultsInItem(item, ProactiveToolResultMaxChars)
		}
	}
	if trCompressed > 0 {
		didCompress = true
		metrics = tokencount.EstimateKiroPayloadMetrics(kiroReq)
		logger.Infof("主动压缩 Level 1: 压缩 %d 个旧 tool_results (threshold=%d, tokens=%d)", trCompressed, threshold, metrics.Tokens)
		if metrics.Tokens <= threshold {
			return true, nil
		}
	}

	// === Level 2: 压缩旧 assistant 内容 ===
	asstCompressed := 0
	for i := 0; i < compressible; i++ {
		item, _ := history[i].(map[string]interface{})
		if item != nil && compressAssistantInItem(item, ProactiveAssistantMaxChars) {
			asstCompressed++
		}
	}
	if asstCompressed > 0 {
		didCompress = true
		metrics = tokencount.EstimateKiroPayloadMetrics(kiroReq)
		logger.Infof("主动压缩 Level 2: 压缩 %d 条旧 assistant 消息 (threshold=%d, tokens=%d)", asstCompressed, threshold, metrics.Tokens)
		if metrics.Tokens <= threshold {
			return true, nil
		}
	}

	// === Level 3: 丢弃最旧的消息对 ===
	totalDropped := 0
	for metrics.Tokens > threshold {
		compressible = len(history) - ProactiveRecentKeep
		if compressible <= 0 {
			break
		}
		dropTarget := compressible
		if dropTarget > EmergencyHistoryDropBatch {
			dropTarget = EmergencyHistoryDropBatch
		}
		actualDropped, _ := dropHistoryHead(&history, dropTarget)
		if actualDropped == 0 {
			break
		}
		totalDropped += actualDropped
		didCompress = true
		cs["history"] = history
		metrics = tokencount.EstimateKiroPayloadMetrics(kiroReq)
	}

	if totalDropped > 0 {
		validated := ValidateHistoryStructure(history)
		if len(validated) != len(history) {
			cs["history"] = validated
			metrics = tokencount.EstimateKiroPayloadMetrics(kiroReq)
		}
		logger.Infof("主动压缩 Level 3: 丢弃 %d 条旧 history (threshold=%d, tokens=%d)", totalDropped, threshold, metrics.Tokens)
	}

	return didCompress, nil
}

// ---------------------------------------------------------------------------
// PruneHistoryForCapacity 紧急裁剪（对齐 Python _prune_history_for_capacity）
// ---------------------------------------------------------------------------

// PruneHistoryForCapacity 在请求体超过硬限制时紧急裁剪历史。
// 返回丢弃的消息数。
func PruneHistoryForCapacity(kiroReq map[string]interface{}, model string) int {
	cs, _ := kiroReq["conversationState"].(map[string]interface{})
	if cs == nil {
		return 0
	}
	history, _ := cs["history"].([]interface{})
	if len(history) == 0 {
		return 0
	}

	limit := int(float64(GetContextWindowSize(model)) * 0.92)
	dropped := 0

	for {
		metrics := tokencount.EstimateKiroPayloadMetrics(kiroReq)
		if metrics.Tokens <= limit {
			break
		}
		if len(history) <= EmergencyHistoryMinMessages {
			break
		}
		removable := len(history) - EmergencyHistoryMinMessages
		dropNow := removable
		if dropNow > EmergencyHistoryDropBatch {
			dropNow = EmergencyHistoryDropBatch
		}
		if dropNow <= 0 {
			break
		}
		actualDropped, _ := dropHistoryHead(&history, dropNow)
		if actualDropped == 0 {
			break
		}
		dropped += actualDropped
		cs["history"] = history
	}

	if dropped > 0 {
		validated := ValidateHistoryStructure(history)
		if len(validated) != len(history) {
			cs["history"] = validated
		}
		logger.Infof("紧急裁剪: 丢弃 %d 条 history, 剩余 %d 条", dropped, len(cs["history"].([]interface{})))
	}

	return dropped
}