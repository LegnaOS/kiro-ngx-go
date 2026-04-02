// Package runtimelog 提供内存环形缓冲区，供 Admin UI 按尾部/增量读取最近日志。
// 参考 clauldcode-proxy/admin/runtime_log.py
package runtimelog

import (
	"sync"
	"sync/atomic"
	"time"
)

const (
	DefaultMaxLines = 5000
	DefaultLimit    = 100
	MaxLimit        = 200
)

// Entry 单条运行时日志条目
type Entry struct {
	Seq       int    `json:"seq"`
	Timestamp string `json:"timestamp"`
	Level     string `json:"level"`
	Message   string `json:"message"`
}

// Buffer 线程安全的运行时日志环形缓冲区
type Buffer struct {
	mu       sync.RWMutex
	entries  []Entry
	head     int // 下一个写入位置（环形）
	size     int // 当前有效条目数
	capacity int
	nextSeq  atomic.Int64
}

// NewBuffer 创建新的环形缓冲区
func NewBuffer(maxLines int) *Buffer {
	if maxLines < 1 {
		maxLines = DefaultMaxLines
	}
	b := &Buffer{
		entries:  make([]Entry, maxLines),
		capacity: maxLines,
	}
	b.nextSeq.Store(1)
	return b
}

// Append 追加一条日志条目（线程安全）
func (b *Buffer) Append(level, message string) {
	seq := int(b.nextSeq.Add(1) - 1)
	e := Entry{
		Seq:       seq,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Level:     level,
		Message:   message,
	}
	b.mu.Lock()
	b.entries[b.head] = e
	b.head = (b.head + 1) % b.capacity
	if b.size < b.capacity {
		b.size++
	}
	b.mu.Unlock()
}

// snapshot 返回当前所有有效条目（按时间顺序）和下一个 cursor
func (b *Buffer) snapshot() ([]Entry, int) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	nextCursor := int(b.nextSeq.Load()) - 1

	if b.size == 0 {
		return nil, nextCursor
	}

	result := make([]Entry, b.size)
	if b.size < b.capacity {
		// 未满：从 0 到 size-1
		copy(result, b.entries[:b.size])
	} else {
		// 已满：从 head 开始绕一圈
		n := copy(result, b.entries[b.head:])
		copy(result[n:], b.entries[:b.head])
	}
	return result, nextCursor
}

// Tail 返回最近 limit 条日志（可按 level/keyword 过滤）
func (b *Buffer) Tail(limit int, level, keyword string) map[string]interface{} {
	limit = clampLimit(limit)
	entries, nextCursor := b.snapshot()
	entries = applyFilters(entries, level, keyword)
	if len(entries) > limit {
		entries = entries[len(entries)-limit:]
	}
	return map[string]interface{}{
		"entries":    entriesToDicts(entries),
		"nextCursor": nextCursor,
		"bufferSize": b.size,
	}
}

// Since 返回 seq > cursor 的条目（增量拉取）
func (b *Buffer) Since(cursor, limit int, level, keyword string) map[string]interface{} {
	limit = clampLimit(limit)
	entries, nextCursor := b.snapshot()
	var fresh []Entry
	for _, e := range entries {
		if e.Seq > cursor {
			fresh = append(fresh, e)
		}
	}
	fresh = applyFilters(fresh, level, keyword)
	if len(fresh) > limit {
		fresh = fresh[:limit]
	}
	return map[string]interface{}{
		"entries":    entriesToDicts(fresh),
		"nextCursor": nextCursor,
		"bufferSize": b.size,
	}
}

func clampLimit(limit int) int {
	if limit < 1 {
		return DefaultLimit
	}
	if limit > MaxLimit {
		return MaxLimit
	}
	return limit
}

func applyFilters(entries []Entry, level, keyword string) []Entry {
	if level == "" && keyword == "" {
		return entries
	}
	result := entries[:0:0]
	for _, e := range entries {
		if level != "" && e.Level != level {
			continue
		}
		if keyword != "" {
			if !containsCI(e.Message, keyword) {
				continue
			}
		}
		result = append(result, e)
	}
	return result
}

func containsCI(s, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	sl, subl := len(s), len(sub)
	if subl > sl {
		return false
	}
	for i := 0; i <= sl-subl; i++ {
		match := true
		for j := 0; j < subl; j++ {
			cs, csub := s[i+j], sub[j]
			if cs >= 'A' && cs <= 'Z' {
				cs += 32
			}
			if csub >= 'A' && csub <= 'Z' {
				csub += 32
			}
			if cs != csub {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func entriesToDicts(entries []Entry) []map[string]interface{} {
	if entries == nil {
		return []map[string]interface{}{}
	}
	result := make([]map[string]interface{}, len(entries))
	for i, e := range entries {
		result[i] = map[string]interface{}{
			"seq":       e.Seq,
			"timestamp": e.Timestamp,
			"level":     e.Level,
			"message":   e.Message,
		}
	}
	return result
}

// ---- 全局单例 ----

var (
	globalBuf  *Buffer
	globalOnce sync.Once
)

// Init 初始化全局缓冲区
func Init(maxLines int) *Buffer {
	globalOnce.Do(func() {
		globalBuf = NewBuffer(maxLines)
	})
	return globalBuf
}

// Get 获取全局缓冲区（未初始化返回 nil）
func Get() *Buffer {
	return globalBuf
}
