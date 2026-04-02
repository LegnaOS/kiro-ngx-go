// Package logger 提供异步日志系统，支持按日期滚动文件写入，同时输出到 stderr。
// 所有写入操作通过 channel 异步处理，不阻塞调用方。
package logger

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	channelSize = 4096
	filePerm    = 0o644
	dirPerm     = 0o755
)

// Level 日志级别
type Level int

const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
	LevelFatal
)

func (l Level) String() string {
	switch l {
	case LevelDebug:
		return "DEBUG"
	case LevelInfo:
		return "INFO"
	case LevelWarn:
		return "WARN"
	case LevelError:
		return "ERROR"
	case LevelFatal:
		return "FATAL"
	default:
		return "INFO"
	}
}

type entry struct {
	level Level
	msg   string
}

// Subscriber 日志订阅者接口，用于 runtimelog 等内存缓冲区接收日志
type Subscriber interface {
	Append(level, message string)
}

// Logger 异步日志器
type Logger struct {
	logsDir     string
	ch          chan entry
	wg          sync.WaitGroup
	once        sync.Once
	mu          sync.Mutex
	file        *os.File
	fileDate    string
	stdLog      *log.Logger
	subMu       sync.RWMutex
	subscribers []Subscriber
}

// New 创建并启动日志器。logsDir 为日志目录路径（不存在时自动创建）。
func New(logsDir string) *Logger {
	l := &Logger{
		logsDir: logsDir,
		ch:      make(chan entry, channelSize),
		stdLog:  log.New(os.Stderr, "", log.LstdFlags),
	}
	l.wg.Add(1)
	go l.run()
	return l
}

func (l *Logger) run() {
	defer l.wg.Done()
	for e := range l.ch {
		l.write(e)
	}
	l.closeFile()
}

func (l *Logger) write(e entry) {
	now := time.Now()
	dateStr := now.Format("2006-01-02")
	timeStr := now.Format("2006/01/02 15:04:05")
	line := fmt.Sprintf("%s [%s] %s\n", timeStr, e.level.String(), e.msg)

	l.mu.Lock()
	if l.file == nil || l.fileDate != dateStr {
		l.rotateFile(dateStr)
	}
	if l.file != nil {
		_, _ = io.WriteString(l.file, line)
	}
	l.mu.Unlock()

	// 通知所有订阅者（runtimelog 等）
	l.subMu.RLock()
	subs := l.subscribers
	l.subMu.RUnlock()
	for _, s := range subs {
		s.Append(e.level.String(), e.msg)
	}
}

// AddSubscriber 注册一个日志订阅者（线程安全）
func (l *Logger) AddSubscriber(s Subscriber) {
	l.subMu.Lock()
	l.subscribers = append(l.subscribers, s)
	l.subMu.Unlock()
}

func (l *Logger) rotateFile(dateStr string) {
	if l.file != nil {
		_ = l.file.Close()
		l.file = nil
	}
	if err := os.MkdirAll(l.logsDir, dirPerm); err != nil {
		fmt.Fprintf(os.Stderr, "logger: 创建日志目录失败: %v\n", err)
		return
	}
	filename := filepath.Join(l.logsDir, fmt.Sprintf("kiro-proxy-%s.log", dateStr))
	f, err := os.OpenFile(filename, os.O_CREATE|os.O_APPEND|os.O_WRONLY, filePerm)
	if err != nil {
		fmt.Fprintf(os.Stderr, "logger: 打开日志文件失败: %v\n", err)
		return
	}
	l.file = f
	l.fileDate = dateStr
}

func (l *Logger) closeFile() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		_ = l.file.Close()
		l.file = nil
	}
}

// send 向 channel 投递日志，channel 满时降级到 stderr 直接输出
func (l *Logger) send(e entry) {
	select {
	case l.ch <- e:
	default:
		fmt.Fprintf(os.Stderr, "[logger overflow] [%s] %s\n", e.level.String(), e.msg)
	}
}

// Shutdown 优雅关闭，等待所有日志写完。应在程序退出前调用。
func (l *Logger) Shutdown() {
	l.once.Do(func() {
		close(l.ch)
		l.wg.Wait()
	})
}

// ---- 日志方法 ----

func (l *Logger) logf(level Level, format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	l.stdLog.Printf("[%s] %s", level.String(), msg)
	l.send(entry{level: level, msg: msg})
}

func (l *Logger) logln(level Level, args ...interface{}) {
	msg := fmt.Sprint(args...)
	l.stdLog.Printf("[%s] %s", level.String(), msg)
	l.send(entry{level: level, msg: msg})
}

// Debugf 调试日志
func (l *Logger) Debugf(format string, args ...interface{}) { l.logf(LevelDebug, format, args...) }

// Infof 信息日志
func (l *Logger) Infof(format string, args ...interface{}) { l.logf(LevelInfo, format, args...) }

// Warnf 警告日志
func (l *Logger) Warnf(format string, args ...interface{}) { l.logf(LevelWarn, format, args...) }

// Errorf 错误日志
func (l *Logger) Errorf(format string, args ...interface{}) { l.logf(LevelError, format, args...) }

// Printf 兼容 log.Printf（Info 级别）
func (l *Logger) Printf(format string, args ...interface{}) { l.logf(LevelInfo, format, args...) }

// Println 兼容 log.Println（Info 级别）
func (l *Logger) Println(args ...interface{}) { l.logln(LevelInfo, args...) }

// Fatalf 致命错误：同步写完后 os.Exit(1)
func (l *Logger) Fatalf(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	l.stdLog.Printf("[%s] %s", LevelFatal.String(), msg)
	// 同步写入文件，确保 fatal 日志不丢失
	l.write(entry{level: LevelFatal, msg: msg})
	l.closeFile()
	os.Exit(1)
}

// Fatal 致命错误：同步写完后 os.Exit(1)
func (l *Logger) Fatal(args ...interface{}) {
	l.Fatalf("%s", fmt.Sprint(args...))
}

// ---- 全局单例 ----

var (
	global     *Logger
	globalOnce sync.Once
	globalMu   sync.Mutex
)

// Init 初始化全局日志器。logsDir 通常为可执行文件同目录的 logs/ 路径。
func Init(logsDir string) {
	globalMu.Lock()
	defer globalMu.Unlock()
	globalOnce.Do(func() {
		global = New(logsDir)
	})
}

// Shutdown 关闭全局日志器，等待所有日志写完。
func Shutdown() {
	globalMu.Lock()
	g := global
	globalMu.Unlock()
	if g != nil {
		g.Shutdown()
	}
}

func get() *Logger {
	globalMu.Lock()
	g := global
	globalMu.Unlock()
	return g
}

// Debugf 全局调试日志
func Debugf(format string, args ...interface{}) {
	if g := get(); g != nil {
		g.Debugf(format, args...)
	} else {
		log.Printf("[DEBUG] "+format, args...)
	}
}

// Infof 全局信息日志
func Infof(format string, args ...interface{}) {
	if g := get(); g != nil {
		g.Infof(format, args...)
	} else {
		log.Printf("[INFO] "+format, args...)
	}
}

// Warnf 全局警告日志
func Warnf(format string, args ...interface{}) {
	if g := get(); g != nil {
		g.Warnf(format, args...)
	} else {
		log.Printf("[WARN] "+format, args...)
	}
}

// Errorf 全局错误日志
func Errorf(format string, args ...interface{}) {
	if g := get(); g != nil {
		g.Errorf(format, args...)
	} else {
		log.Printf("[ERROR] "+format, args...)
	}
}

// Printf 全局 Info 日志（兼容 log.Printf）
func Printf(format string, args ...interface{}) {
	if g := get(); g != nil {
		g.Printf(format, args...)
	} else {
		log.Printf(format, args...)
	}
}

// Println 全局 Info 日志（兼容 log.Println）
func Println(args ...interface{}) {
	if g := get(); g != nil {
		g.Println(args...)
	} else {
		log.Println(args...)
	}
}

// Fatalf 全局致命错误日志
func Fatalf(format string, args ...interface{}) {
	if g := get(); g != nil {
		g.Fatalf(format, args...)
	} else {
		log.Fatalf(format, args...)
	}
}

// Fatal 全局致命错误日志
func Fatal(args ...interface{}) {
	if g := get(); g != nil {
		g.Fatal(args...)
	} else {
		log.Fatal(args...)
	}
}

// AddSubscriber 向全局日志器注册订阅者（如 runtimelog.Buffer）
func AddSubscriber(s Subscriber) {
	if g := get(); g != nil {
		g.AddSubscriber(s)
	}
}
