// Package tokencount 本地 token 计算模块。
//
// 目标：
// - 递归覆盖 text / thinking / tool_result / tool_use.input / tools / schema
// - 提供比纯字符长度更稳定的本地近似 tokenizer
// - 为发送前预检提供 tokens/chars/bytes 统计
package tokencount

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"sync"
	"unicode/utf8"

	"kiro-proxy/internal/httpclient"
	"kiro-proxy/internal/logger"
)

// CountTokensConfig 远程 token 计数 API 配置
type CountTokensConfig struct {
	ApiURL   string
	ApiKey   string
	AuthType string // "x-api-key" 或 "bearer"
	Proxy    *httpclient.ProxyConfig
}

// PayloadMetrics token/字符/字节 统计指标
type PayloadMetrics struct {
	Tokens int
	Chars  int
	Bytes  int
}

// 全局配置与远程客户端（复用）
var (
	globalConfig *CountTokensConfig

	remoteClientOnce sync.Once
	remoteClient     *http.Client
)

// InitConfig 初始化全局配置
func InitConfig(cfg *CountTokensConfig) {
	globalConfig = cfg
}

// ---------- 字符分类辅助函数 ----------

// CJK 码点区间
var cjkRanges = [][2]rune{
	{0x3400, 0x4DBF},
	{0x4E00, 0x9FFF},
	{0xF900, 0xFAFF},
	{0x3040, 0x309F}, // 平假名
	{0x30A0, 0x30FF}, // 片假名
	{0xAC00, 0xD7AF}, // 韩文
}

// 西文字符码点区间
var westernRanges = [][2]rune{
	{0x0000, 0x024F},
	{0x1E00, 0x1EFF},
	{0x2C60, 0x2C7F},
	{0xA720, 0xA7FF},
	{0xAB30, 0xAB6F},
}

// isNonWesternChar 判断字符是否为非西文字符
func isNonWesternChar(c rune) bool {
	if c <= 0x7F {
		return false
	}
	for _, r := range westernRanges {
		if c <= r[1] {
			return c < r[0]
		}
	}
	return true
}

// isCJKChar 判断字符是否为 CJK 字符
func isCJKChar(c rune) bool {
	for _, r := range cjkRanges {
		if c >= r[0] && c <= r[1] {
			return true
		}
	}
	return false
}

// ---------- 核心 token 计算 ----------

// CountTokens 本地近似 tokenizer。
//
// 不是 Claude 精确 tokenizer，但比简单 length/4 更稳：
// - CJK 按更高权重计入
// - 额外参考 UTF-8 字节长度，避免低估大量非 ASCII 内容
// - 对短文本保守上调，减少明显漏算
func CountTokens(text string) int {
	if len(text) == 0 {
		return 0
	}

	byteLen := len(text)

	var asciiChars, cjkChars, westernChars, otherChars int

	for _, c := range text {
		if c <= 0x7F {
			asciiChars++
		} else if (0x4E00 <= c && c <= 0x9FFF) || (0x3400 <= c && c <= 0x4DBF) ||
			(0xF900 <= c && c <= 0xFAFF) || (0x3040 <= c && c <= 0x30FF) ||
			(0xAC00 <= c && c <= 0xD7AF) {
			cjkChars++
		} else if c <= 0x024F || (0x1E00 <= c && c <= 0x1EFF) ||
			(0x2C60 <= c && c <= 0x2C7F) || (0xA720 <= c && c <= 0xA7FF) ||
			(0xAB30 <= c && c <= 0xAB6F) {
			westernChars++
		} else {
			otherChars++
		}
	}

	charBased := float64(asciiChars)/4.0 + float64(westernChars)/2.8 + float64(cjkChars)*0.95 + float64(otherChars)/1.8
	byteBased := float64(byteLen) / 3.4
	denseCJK := float64(asciiChars+westernChars)/4.5 + float64(cjkChars)*0.85 + float64(otherChars)/2.0

	estimate := int(math.Ceil(math.Max(charBased, math.Max(byteBased, denseCJK))))

	if estimate < 32 {
		estimate = int(math.Ceil(float64(estimate) * 1.15))
	} else if estimate < 256 {
		estimate = int(math.Ceil(float64(estimate) * 1.08))
	}

	if estimate < 1 {
		return 1
	}
	return estimate
}

// ---------- 内容展平辅助函数 ----------

// flattenContent 从 content 中提取文本（支持 string / list / dict）
func flattenContent(content interface{}) string {
	if content == nil {
		return ""
	}
	switch v := content.(type) {
	case string:
		return v
	case []interface{}:
		var parts []string
		for _, item := range v {
			parts = append(parts, flattenContentBlock(item))
		}
		return strings.Join(parts, "")
	case map[string]interface{}:
		if text, ok := v["text"]; ok {
			if s, ok := text.(string); ok {
				return s
			}
		}
		if c, ok := v["content"]; ok {
			return flattenContent(c)
		}
		if thinking, ok := v["thinking"]; ok {
			if s, ok := thinking.(string); ok {
				return s
			}
		}
		b, _ := json.Marshal(v)
		return string(b)
	default:
		return fmt.Sprintf("%v", v)
	}
}

// flattenContentBlock 从单个内容块中提取文本
func flattenContentBlock(block interface{}) string {
	if block == nil {
		return ""
	}
	switch v := block.(type) {
	case string:
		return v
	case map[string]interface{}:
		blockType, _ := v["type"].(string)
		switch blockType {
		case "text":
			if s, ok := v["text"].(string); ok {
				return s
			}
			return ""
		case "thinking":
			if s, ok := v["thinking"].(string); ok && s != "" {
				return s
			}
			if s, ok := v["text"].(string); ok {
				return s
			}
			return ""
		case "tool_result":
			return flattenContent(v["content"])
		case "tool_use":
			payload, ok := v["input"]
			if !ok || payload == nil {
				return ""
			}
			b, _ := json.Marshal(payload)
			return string(b)
		case "image":
			return ""
		case "document":
			source, ok := v["source"].(map[string]interface{})
			if !ok {
				return ""
			}
			if data, ok := source["data"].(string); ok {
				return data
			}
			return ""
		default:
			// 无 type 或未知 type，尝试 text / content 字段
			if s, ok := v["text"].(string); ok {
				return s
			}
			if c, ok := v["content"]; ok {
				return flattenContent(c)
			}
			b, _ := json.Marshal(v)
			return string(b)
		}
	default:
		return fmt.Sprintf("%v", block)
	}
}

// collectTextSegments 递归收集任意值中的文本片段。
// 对 "bytes" 键（base64 图片数据）做特殊处理：按字节长度估算 token 数。
func collectTextSegments(obj interface{}) []string {
	var result []string
	collectTextSegmentsInto(obj, &result)
	return result
}

// collectTextSegmentsInto 递归收集文本片段到 result 切片
func collectTextSegmentsInto(obj interface{}, result *[]string) {
	if obj == nil {
		return
	}
	switch v := obj.(type) {
	case string:
		*result = append(*result, v)
	case bool:
		if v {
			*result = append(*result, "true")
		} else {
			*result = append(*result, "false")
		}
	case float64:
		// JSON 数字默认解码为 float64
		*result = append(*result, fmt.Sprintf("%v", v))
	case json.Number:
		*result = append(*result, v.String())
	case int:
		*result = append(*result, fmt.Sprintf("%d", v))
	case int64:
		*result = append(*result, fmt.Sprintf("%d", v))
	case []interface{}:
		for _, item := range v {
			collectTextSegmentsInto(item, result)
		}
	case map[string]interface{}:
		for key, value := range v {
			*result = append(*result, key)
			if key == "bytes" {
				// base64 图片数据：按体积给保守 token 估算，避免把原始内容当自然语言逐字分词
				if s, ok := value.(string); ok && len(s) > 0 {
					byteLen := len(s)
					pseudoTokens := int(math.Ceil(float64(byteLen) * 0.75 / 4.0))
					if pseudoTokens > 0 {
						*result = append(*result, fmt.Sprintf("<binary:%d>", pseudoTokens))
					}
				}
				continue
			}
			collectTextSegmentsInto(value, result)
		}
	default:
		*result = append(*result, fmt.Sprintf("%v", v))
	}
}

// ---------- thinking 标签渲染 ----------

// renderThinkingSegments 根据 thinking 配置生成 thinking 标签文本片段
func renderThinkingSegments(thinking, outputConfig map[string]interface{}) []string {
	if thinking == nil {
		return nil
	}

	thinkingType := strings.TrimSpace(strings.ToLower(fmt.Sprintf("%v", thinking["type"])))

	switch thinkingType {
	case "enabled":
		budget := 20000
		if raw, ok := thinking["budget_tokens"]; ok {
			switch n := raw.(type) {
			case float64:
				budget = int(n)
			case int:
				budget = n
			case json.Number:
				if i, err := n.Int64(); err == nil {
					budget = int(i)
				}
			}
		}
		if budget < 1024 {
			budget = 1024
		} else if budget > 24576 {
			budget = 24576
		}
		return []string{
			"<thinking_mode>enabled</thinking_mode>",
			fmt.Sprintf("<max_thinking_length>%d</max_thinking_length>", budget),
		}

	case "adaptive":
		effort := "high"
		if outputConfig != nil {
			if raw, ok := outputConfig["effort"]; ok {
				e := strings.TrimSpace(strings.ToLower(fmt.Sprintf("%v", raw)))
				if e == "low" || e == "medium" || e == "high" {
					effort = e
				}
			}
		}
		return []string{
			"<thinking_mode>adaptive</thinking_mode>",
			fmt.Sprintf("<thinking_effort>%s</thinking_effort>", effort),
		}
	}

	return nil
}

// ---------- 远程 API 调用 ----------

// getRemoteClient 获取或创建复用的远程 HTTP 客户端
func getRemoteClient(cfg *CountTokensConfig) (*http.Client, error) {
	var initErr error
	remoteClientOnce.Do(func() {
		var err error
		remoteClient, err = httpclient.BuildHTTPClient(cfg.Proxy, 300)
		if err != nil {
			initErr = err
		}
	})
	if initErr != nil {
		return nil, initErr
	}
	return remoteClient, nil
}

// callRemoteCountTokens 调用远程 API 进行 token 计数
func callRemoteCountTokens(
	apiURL string,
	cfg *CountTokensConfig,
	model string,
	system, messages, tools []map[string]interface{},
) (int, error) {
	client, err := getRemoteClient(cfg)
	if err != nil {
		return 0, fmt.Errorf("创建 HTTP 客户端失败: %w", err)
	}

	// 构建请求体
	body := map[string]interface{}{
		"model":    model,
		"messages": messages,
	}
	if system != nil {
		body["system"] = system
	}
	if tools != nil {
		body["tools"] = tools
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return 0, fmt.Errorf("序列化请求体失败: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, apiURL, bytes.NewReader(jsonBody))
	if err != nil {
		return 0, fmt.Errorf("创建请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	// 设置认证头
	if cfg.ApiKey != "" {
		if cfg.AuthType == "bearer" {
			req.Header.Set("Authorization", "Bearer "+cfg.ApiKey)
		} else {
			req.Header.Set("x-api-key", cfg.ApiKey)
		}
	}

	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("请求远程 API 失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("远程 API 返回错误状态码 %d: %s", resp.StatusCode, string(respBody))
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, fmt.Errorf("解析响应失败: %w", err)
	}

	if tokens, ok := result["input_tokens"].(float64); ok {
		return int(tokens), nil
	}
	return 1, nil
}

// ---------- 公开接口 ----------

// CountAllTokens 估算 Anthropic 请求的输入 tokens，优先远程 API，回退本地。
func CountAllTokens(
	model string,
	system []map[string]interface{},
	messages []map[string]interface{},
	tools []map[string]interface{},
	thinking, outputConfig map[string]interface{},
) int {
	if globalConfig != nil && globalConfig.ApiURL != "" {
		tokens, err := callRemoteCountTokens(globalConfig.ApiURL, globalConfig, model, system, messages, tools)
		if err != nil {
			logger.Warnf("远程 count_tokens API 调用失败，回退到本地计算: %v", err)
		} else {
			return tokens
		}
	}

	return EstimateAnthropicRequestMetrics(system, messages, tools, thinking, outputConfig).Tokens
}

// EstimateAnthropicRequestMetrics 本地估算 Anthropic 风格请求的 tokens/chars/bytes
func EstimateAnthropicRequestMetrics(
	system []map[string]interface{},
	messages []map[string]interface{},
	tools []map[string]interface{},
	thinking, outputConfig map[string]interface{},
) PayloadMetrics {
	var segments []string
	extraTokens := 0

	// 处理 system
	if system != nil {
		for _, entry := range system {
			// 将 entry 转为 interface{} 传给 flattenContent
			segments = append(segments, flattenContent(mapToInterface(entry)))
		}
	}

	// 处理 thinking 标签
	if thinking != nil {
		segments = append(segments, renderThinkingSegments(thinking, outputConfig)...)
	}

	// 处理 messages
	for _, msg := range messages {
		segments = append(segments, flattenContent(msg["content"]))

		content, ok := msg["content"].([]interface{})
		if !ok {
			continue
		}
		for _, item := range content {
			block, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			blockType, _ := block["type"].(string)
			switch blockType {
			case "image":
				extraTokens += 1600
			case "document":
				source, ok := block["source"].(map[string]interface{})
				if !ok {
					continue
				}
				data, ok := source["data"].(string)
				if ok && data != "" {
					extraTokens += int(math.Ceil(float64(len(data)) * 0.75 / 4.0))
				}
			}
		}
	}

	// 处理 tools
	if tools != nil {
		for _, tool := range tools {
			segments = append(segments, collectTextSegments(mapToInterface(tool))...)
		}
	}

	baseMetrics := EstimateTextMetrics(segments)
	tokens := baseMetrics.Tokens + extraTokens
	if tokens < 1 {
		tokens = 1
	}
	return PayloadMetrics{
		Tokens: tokens,
		Chars:  baseMetrics.Chars,
		Bytes:  baseMetrics.Bytes,
	}
}

// EstimateKiroPayloadMetrics 估算 Kiro conversationState 请求体的 tokens/chars/bytes
func EstimateKiroPayloadMetrics(payload interface{}) PayloadMetrics {
	segments := collectTextSegments(payload)
	metrics := EstimateTextMetrics(segments)
	if metrics.Tokens < 1 {
		metrics.Tokens = 1
	}
	return metrics
}

// EstimateTextMetrics 计算一组文本片段的 tokens/chars/bytes 统计
func EstimateTextMetrics(texts []string) PayloadMetrics {
	tokenTotal := 0
	charTotal := 0
	byteTotal := 0

	for _, text := range texts {
		if text == "" {
			continue
		}
		tokenTotal += CountTokens(text)
		charTotal += utf8.RuneCountInString(text)
		byteTotal += len(text)
	}

	if tokenTotal < 1 {
		tokenTotal = 1
	}
	return PayloadMetrics{
		Tokens: tokenTotal,
		Chars:  charTotal,
		Bytes:  byteTotal,
	}
}

// EstimateOutputTokens 估算输出 tokens
func EstimateOutputTokens(content []map[string]interface{}) int {
	// 将 content 包装为一条 assistant 消息，复用 EstimateAnthropicRequestMetrics
	contentSlice := make([]interface{}, len(content))
	for i, c := range content {
		contentSlice[i] = mapToInterface(c)
	}
	messages := []map[string]interface{}{
		{
			"role":    "assistant",
			"content": contentSlice,
		},
	}
	metrics := EstimateAnthropicRequestMetrics(nil, messages, nil, nil, nil)
	if metrics.Tokens < 1 {
		return 1
	}
	return metrics.Tokens
}

// mapToInterface 将 map[string]interface{} 转为 interface{}（用于传递给接受 interface{} 的函数）
func mapToInterface(m map[string]interface{}) interface{} {
	return m
}
