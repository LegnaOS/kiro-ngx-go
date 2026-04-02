//go:build !windows && !darwin

package sysstat

import (
	"bufio"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// getProcessMemoryMb 读取 /proc/<pid>/status 中的 VmRSS（kB → MB）。
func getProcessMemoryMb() float64 {
	pid := os.Getpid()
	f, err := os.Open(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return 0
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "VmRSS:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			break
		}
		kb, err := strconv.ParseFloat(fields[1], 64)
		if err != nil {
			break
		}
		return kb / 1024
	}
	return 0
}

// readProcessCPUTicks 读取 /proc/self/stat 中的 utime + stime（clock ticks）。
func readProcessCPUTicks() uint64 {
	data, err := os.ReadFile("/proc/self/stat")
	if err != nil {
		return 0
	}
	// 格式: pid (comm) state ... 第14列 utime 第15列 stime（1-indexed）
	// comm 可能含空格，需找最后一个 ')' 之后的字段
	str := string(data)
	idx := strings.LastIndex(str, ")")
	if idx < 0 || idx+2 >= len(str) {
		return 0
	}
	fields := strings.Fields(str[idx+2:])
	// ')' 之后: [0]state [1]ppid ... [11]utime [12]stime
	if len(fields) < 13 {
		return 0
	}
	utime, _ := strconv.ParseUint(fields[11], 10, 64)
	stime, _ := strconv.ParseUint(fields[12], 10, 64)
	return utime + stime
}

// getCPUPercent 两次采样 /proc/self/stat 计算当前进程 CPU 使用率。
func getCPUPercent() float64 {
	ticks1 := readProcessCPUTicks()
	start := time.Now()
	time.Sleep(100 * time.Millisecond)
	elapsed := time.Since(start)
	ticks2 := readProcessCPUTicks()

	delta := ticks2 - ticks1
	if delta == 0 || elapsed == 0 {
		return 0
	}

	// clock tick 通常为 1/100 秒（SC_CLK_TCK = 100）
	const clkTck = 100
	cpuSeconds := float64(delta) / clkTck
	wallSeconds := elapsed.Seconds()
	numCPU := float64(runtime.NumCPU())

	return (cpuSeconds / wallSeconds / numCPU) * 100
}
