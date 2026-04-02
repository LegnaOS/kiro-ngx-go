// Package sysstat 提供跨平台的进程内存和系统 CPU 使用率采集。
// 对应 clauldcode-proxy/admin/handlers.py 中的 _get_process_memory_mb / _get_cpu_percent。
// memoryBreakdown 使用 runtime.MemStats 替代 Python 的 tracemalloc。
package sysstat

import (
	"runtime"
	"sync"
	"time"
)

// MemoryBreakdownItem 对应前端 MemoryBreakdownItem 类型
type MemoryBreakdownItem struct {
	Module        string  `json:"module"`
	Path          string  `json:"path"`
	MemoryMb      float64 `json:"memoryMb"`
	SharePercent  float64 `json:"sharePercent"`
}

// SystemStats 对应 GET /system/stats 响应体
type SystemStats struct {
	CPUPercent     float64               `json:"cpuPercent"`
	MemoryMb       float64               `json:"memoryMb"`
	MemoryBreakdown []MemoryBreakdownItem `json:"memoryBreakdown"`
	TracedMemoryMb float64               `json:"tracedMemoryMb"`
}

// --- 内存明细缓存（15 秒 TTL，对应 Python _MEMORY_BREAKDOWN_CACHE_TTL_SEC = 15）---

const memoryCacheTTL = 15 * time.Second

var (
	memCacheMu    sync.Mutex
	memCacheAt    time.Time
	memCacheItems []MemoryBreakdownItem
	memCacheTotal float64
)

// GetStats 采集一次完整的系统状态，CPU 采样需要约 100ms。
func GetStats() SystemStats {
	// CPU 和内存可并行采集
	type cpuResult struct{ v float64 }
	ch := make(chan cpuResult, 1)
	go func() {
		ch <- cpuResult{getCPUPercent()}
	}()

	memMb := getProcessMemoryMb()
	breakdown, tracedMb := getMemoryBreakdown()

	cpu := <-ch

	return SystemStats{
		CPUPercent:      roundOne(cpu.v),
		MemoryMb:        roundOne(memMb),
		MemoryBreakdown: breakdown,
		TracedMemoryMb:  tracedMb,
	}
}

// getMemoryBreakdown 带 TTL 缓存的 Go 运行时内存明细。
func getMemoryBreakdown() ([]MemoryBreakdownItem, float64) {
	now := time.Now()
	memCacheMu.Lock()
	if !memCacheAt.IsZero() && now.Sub(memCacheAt) < memoryCacheTTL {
		items := make([]MemoryBreakdownItem, len(memCacheItems))
		copy(items, memCacheItems)
		total := memCacheTotal
		memCacheMu.Unlock()
		return items, total
	}
	memCacheMu.Unlock()

	items, total := collectMemoryBreakdown()

	memCacheMu.Lock()
	memCacheAt = now
	memCacheItems = items
	memCacheTotal = total
	memCacheMu.Unlock()

	return items, total
}

// collectMemoryBreakdown 用 runtime.MemStats 生成内存明细，替代 Python tracemalloc。
func collectMemoryBreakdown() ([]MemoryBreakdownItem, float64) {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)

	type entry struct {
		module string
		path   string
		bytes  uint64
	}

	// 将 MemStats 各字段映射为可读的"模块"条目
	fields := []entry{
		{"runtime.heap", "heap in-use objects", ms.HeapInuse},
		{"runtime.stack", "goroutine stacks", ms.StackInuse},
		{"runtime.mspan", "mspan structures", ms.MSpanInuse},
		{"runtime.mcache", "mcache structures", ms.MCacheInuse},
		{"runtime.other", "other system allocs", ms.OtherSys},
		{"runtime.gc", "GC metadata", ms.GCSys},
	}

	// 总追踪字节 = Sys（向 OS 申请的总量）
	totalBytes := ms.Sys
	if totalBytes == 0 {
		return nil, 0
	}
	totalMb := float64(totalBytes) / (1024 * 1024)

	items := make([]MemoryBreakdownItem, 0, len(fields))
	for _, f := range fields {
		if f.bytes == 0 {
			continue
		}
		mb := float64(f.bytes) / (1024 * 1024)
		items = append(items, MemoryBreakdownItem{
			Module:       f.module,
			Path:         f.path,
			MemoryMb:     roundTwo(mb),
			SharePercent: roundOne(float64(f.bytes) / float64(totalBytes) * 100),
		})
	}

	return items, roundTwo(totalMb)
}

func roundOne(v float64) float64 {
	return float64(int(v*10+0.5)) / 10
}

func roundTwo(v float64) float64 {
	return float64(int(v*100+0.5)) / 100
}
