//go:build windows

package sysstat

import (
	"runtime"
	"syscall"
	"time"
	"unsafe"
)

var (
	kernel32              = syscall.NewLazyDLL("kernel32.dll")
	psapi                 = syscall.NewLazyDLL("psapi.dll")
	procGetCurrentProcess = kernel32.NewProc("GetCurrentProcess")
	procGetProcessMemInfo = psapi.NewProc("GetProcessMemoryInfo")
	procGetProcessTimes   = kernel32.NewProc("GetProcessTimes")
)

type fileTime struct {
	LowDateTime  uint32
	HighDateTime uint32
}

func ftToUint64(ft fileTime) uint64 {
	return uint64(ft.HighDateTime)<<32 | uint64(ft.LowDateTime)
}

// processMemoryCounters 对应 Windows PROCESS_MEMORY_COUNTERS 结构体
type processMemoryCounters struct {
	CB                         uint32
	PageFaultCount             uint32
	PeakWorkingSetSize         uintptr
	WorkingSetSize             uintptr
	QuotaPeakPagedPoolUsage    uintptr
	QuotaPagedPoolUsage        uintptr
	QuotaPeakNonPagedPoolUsage uintptr
	QuotaNonPagedPoolUsage     uintptr
	PagefileUsage              uintptr
	PeakPagefileUsage          uintptr
}

// getProcessMemoryMb 通过 GetCurrentProcess + GetProcessMemoryInfo 获取进程 WorkingSet（RSS）。
func getProcessMemoryMb() float64 {
	handle, _, _ := procGetCurrentProcess.Call()

	var counters processMemoryCounters
	counters.CB = uint32(unsafe.Sizeof(counters))

	ret, _, _ := procGetProcessMemInfo.Call(
		handle,
		uintptr(unsafe.Pointer(&counters)),
		uintptr(counters.CB),
	)
	if ret == 0 {
		return 0
	}
	return float64(counters.WorkingSetSize) / (1024 * 1024)
}

// getCPUPercent 通过 GetProcessTimes 两次采样计算当前进程 CPU 使用率。
func getCPUPercent() float64 {
	handle, _, _ := procGetCurrentProcess.Call()

	var creation, exit, kernel1, user1 fileTime
	ret, _, _ := procGetProcessTimes.Call(
		handle,
		uintptr(unsafe.Pointer(&creation)),
		uintptr(unsafe.Pointer(&exit)),
		uintptr(unsafe.Pointer(&kernel1)),
		uintptr(unsafe.Pointer(&user1)),
	)
	if ret == 0 {
		return 0
	}

	start := time.Now()
	time.Sleep(100 * time.Millisecond)
	elapsed := time.Since(start)

	var creation2, exit2, kernel2, user2 fileTime
	ret, _, _ = procGetProcessTimes.Call(
		handle,
		uintptr(unsafe.Pointer(&creation2)),
		uintptr(unsafe.Pointer(&exit2)),
		uintptr(unsafe.Pointer(&kernel2)),
		uintptr(unsafe.Pointer(&user2)),
	)
	if ret == 0 {
		return 0
	}

	// kernel + user 的增量即进程消耗的 CPU 时间（单位 100ns）
	kernelDelta := ftToUint64(kernel2) - ftToUint64(kernel1)
	userDelta := ftToUint64(user2) - ftToUint64(user1)
	cpuDelta := kernelDelta + userDelta

	// 墙钟时间转 100ns 单位，乘以逻辑核心数得到总可用 CPU 时间
	wall100ns := uint64(elapsed.Nanoseconds() / 100)
	totalAvail := wall100ns * uint64(runtime.NumCPU())
	if totalAvail == 0 {
		return 0
	}
	return float64(cpuDelta) / float64(totalAvail) * 100
}
