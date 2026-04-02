// Package messagelog 记录完整的 API 请求和响应。
// 长文本（>=200字符）或大块数据（序列化>1000字符）存入独立的 texts 文件，主日志中用引用替代。
// 所有写入操作通过 channel 异步处理，不阻塞调用方。
// 参考 clauldcode-proxy/anthropic_api/message_log.py
package messagelog

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

const (
	TextThreshold = 200  // 单个字符串外置阈值（字符数）
	BulkThreshold = 1000 // 整体序列化后超此长度则整块外置
	channelSize   = 2048
)

type writeJob struct {
	logFile  string
	textFile string
	line     string
}

// MessageLogger 消息日志记录器，异步写入
type MessageLogger struct {
	mu         sync.RWMutex
	enabled    bool
	logDir     string
	logFile    string
	textFile   string
	textLine   atomic.Int64
	sessionTag string
	ch         chan writeJob
	wg         sync.WaitGroup
	once       sync.Once
}

// New 创建并启动消息日志记录器
func New(logDir string) *MessageLogger {
	m := &MessageLogger{
		logDir: logDir,
		ch:     make(chan writeJob, channelSize),
	}
	m.sessionTag = time.Now().Format("20060102_150405")
	m.initFiles()
	m.wg.Add(1)
	go m.run()
	return m
}

func (m *MessageLogger) initFiles() {
	if m.logDir == "" {
		return
	}
	m.logFile = filepath.Join(m.logDir, fmt.Sprintf("messages_%s.jsonl", m.sessionTag))
	m.textFile = filepath.Join(m.logDir, fmt.Sprintf("texts_%s.jsonl", m.sessionTag))
}

func (m *MessageLogger) run() {
	defer m.wg.Done()
	for job := range m.ch {
		appendLine(job.logFile, job.line)
	}
}

func appendLine(path, line string) {
	if path == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(line + "\n")
}

// Shutdown 等待所有写入完成
func (m *MessageLogger) Shutdown() {
	m.once.Do(func() {
		close(m.ch)
		m.wg.Wait()
	})
}

// Enabled 返回是否启用
func (m *MessageLogger) Enabled() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.enabled
}

// SetEnabled 设置启用状态
func (m *MessageLogger) SetEnabled(enabled bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.enabled = enabled
	if enabled && m.logDir != "" && m.logFile == "" {
		m.initFiles()
	}
}

// storeText 将长文本写入 texts 文件，返回引用标记（同步写，因为需要行号）
func (m *MessageLogger) storeText(text string) string {
	if m.textFile == "" {
		return text
	}
	lineNo := int(m.textLine.Add(1))
	row, _ := json.Marshal(map[string]interface{}{"line": lineNo, "text": text})
	appendLine(m.textFile, string(row))
	return fmt.Sprintf("...[texts:%d]...", lineNo)
}

// compactValue 递归压缩：字符串超阈值则外置
func (m *MessageLogger) compactValue(v interface{}) interface{} {
	switch val := v.(type) {
	case string:
		if len([]rune(val)) >= TextThreshold {
			return m.storeText(val)
		}
		return val
	case []interface{}:
		result := make([]interface{}, len(val))
		for i, item := range val {
			result[i] = m.compactValue(item)
		}
		return result
	case map[string]interface{}:
		result := make(map[string]interface{}, len(val))
		for k, item := range val {
			result[k] = m.compactValue(item)
		}
		return result
	default:
		return v
	}
}

// compactBulk 整块压缩：先递归压缩内部，若结果序列化仍超阈值则整块外置
func (m *MessageLogger) compactBulk(v interface{}) interface{} {
	if v == nil {
		return nil
	}
	compacted := m.compactValue(v)
	bs, err := json.Marshal(compacted)
	if err != nil {
		return compacted
	}
	if len(bs) > BulkThreshold {
		return m.storeText(string(bs))
	}
	return compacted
}

// send 异步投递写入任务，channel 满时同步写（降级）
func (m *MessageLogger) send(entry map[string]interface{}) {
	if m.logFile == "" {
		return
	}
	line, err := json.Marshal(entry)
	if err != nil {
		return
	}
	job := writeJob{logFile: m.logFile, textFile: m.textFile, line: string(line)}
	select {
	case m.ch <- job:
	default:
		// channel 满，同步写
		appendLine(job.logFile, job.line)
	}
}

// LogRequest 记录请求
func (m *MessageLogger) LogRequest(model string, messages, system, tools []map[string]interface{}, stream bool) {
	if !m.Enabled() {
		return
	}
	entry := map[string]interface{}{
		"type":      "request",
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"model":     model,
		"stream":    stream,
		"msgCount":  len(messages),
		"system":    m.compactBulk(toInterfaceSlice(system)),
		"messages":  m.compactBulk(toInterfaceSlice(messages)),
	}
	if tools != nil {
		entry["toolCount"] = len(tools)
		entry["tools"] = m.compactBulk(toInterfaceSlice(tools))
	}
	m.send(entry)
}

// LogResponse 记录非流式响应
func (m *MessageLogger) LogResponse(model string, content []interface{}, stopReason string, usage map[string]interface{}) {
	if !m.Enabled() {
		return
	}
	entry := map[string]interface{}{
		"type":       "response",
		"timestamp":  time.Now().UTC().Format(time.RFC3339),
		"model":      model,
		"content":    m.compactBulk(content),
		"stopReason": stopReason,
		"usage":      usage,
	}
	m.send(entry)
}

// LogStreamText 记录流式响应的最终文本
func (m *MessageLogger) LogStreamText(model, text, stopReason string, usage map[string]interface{}) {
	if !m.Enabled() {
		return
	}
	entry := map[string]interface{}{
		"type":       "stream_response",
		"timestamp":  time.Now().UTC().Format(time.RFC3339),
		"model":      model,
		"text":       m.compactValue(text),
		"stopReason": stopReason,
		"usage":      usage,
	}
	m.send(entry)
}

func toInterfaceSlice(s []map[string]interface{}) []interface{} {
	if s == nil {
		return nil
	}
	result := make([]interface{}, len(s))
	for i, v := range s {
		result[i] = v
	}
	return result
}

// ---- 全局单例 ----

var (
	globalML   *MessageLogger
	globalOnce sync.Once
	globalMu   sync.RWMutex
)

// Init 初始化全局消息日志器
func Init(logDir string) *MessageLogger {
	globalOnce.Do(func() {
		globalMu.Lock()
		globalML = New(logDir)
		globalMu.Unlock()
	})
	return globalML
}

// Get 获取全局消息日志器（未初始化返回 nil）
func Get() *MessageLogger {
	globalMu.RLock()
	defer globalMu.RUnlock()
	return globalML
}
