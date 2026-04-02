//go:build darwin

package sysstat

import (
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// getProcessMemoryMb 通过 ps 命令获取进程 RSS（KB → MB）。
func getProcessMemoryMb() float64 {
	pid := os.Getpid()
	out, err := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0
	}
	kb, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil {
		return 0
	}
	return kb / 1024
}

// getCPUPercent 通过 getrusage 两次采样计算当前进程 CPU 使用率。
func getCPUPercent() float64 {
	tvToNano := func(tv syscall.Timeval) int64 {
		return tv.Sec*1e9 + int64(tv.Usec)*1e3
	}

	var ru1 syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru1); err != nil {
		return 0
	}
	cpu1 := tvToNano(ru1.Utime) + tvToNano(ru1.Stime)

	start := time.Now()
	time.Sleep(100 * time.Millisecond)
	elapsed := time.Since(start)

	var ru2 syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru2); err != nil {
		return 0
	}
	cpu2 := tvToNano(ru2.Utime) + tvToNano(ru2.Stime)

	cpuDelta := float64(cpu2 - cpu1)
	wallDelta := float64(elapsed.Nanoseconds())
	numCPU := float64(runtime.NumCPU())

	if wallDelta == 0 || numCPU == 0 {
		return 0
	}
	return (cpuDelta / wallDelta / numCPU) * 100
}
