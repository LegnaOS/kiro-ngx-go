// Package tokenusage 提供按日 token 用量追踪，支持最多 31 天历史记录。
// 内存追踪 + 防抖落盘，支持今日/昨日/每模型维度 + 小时级分布 + 历史日聚合。
package tokenusage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"kiro-proxy/internal/logger"
)

const (
	// StatsSaveDebounce 防抖写盘间隔（秒）
	StatsSaveDebounce = 30.0
	// MaxHistoryDays 最大保留历史天数
	MaxHistoryDays = 31
)

// TokenUsageTracker 按日追踪 token 用量
type TokenUsageTracker struct {
	mu             sync.Mutex
	cachePath      string                                // kiro_token_usage.json 路径
	today          string                                // "2006-01-02" 格式的今日日期
	todayInput     int                                   // 今日输入 token 总量
	todayOutput    int                                   // 今日输出 token 总量
	todayCacheCreation int                               // 今日 cache creation tokens
	todayCacheRead     int                               // 今日 cache read tokens
	yesterdayInput int                                   // 昨日输入 token 总量
	yesterdayOutput int                                  // 昨日输出 token 总量
	yesterdayCacheCreation int                            // 昨日 cache creation tokens
	yesterdayCacheRead     int                            // 昨日 cache read tokens
	modelToday     map[string]map[string]int             // 今日每模型用量 {model: {"input": n, "output": n, "cache_creation": n, "cache_read": n}}
	modelYesterday map[string]map[string]int             // 昨日每模型用量
	hourlyToday    map[string]map[string]int             // 今日小时级用量 {"00": {"input": n, "output": n, "cache_creation": n, "cache_read": n}}
	dailyHistory   map[string]map[string]interface{}     // 历史日聚合
	lastSaveAt     *float64                              // 上次保存时间（单调时钟秒数）
	dirty          bool                                  // 是否有未落盘的变更
}

// cacheFile JSON 持久化结构
type cacheFile struct {
	Date           string                            `json:"date"`
	TodayInput     int                               `json:"todayInput"`
	TodayOutput    int                               `json:"todayOutput"`
	TodayCacheCreation int                            `json:"todayCacheCreation"`
	TodayCacheRead     int                            `json:"todayCacheRead"`
	YesterdayInput int                               `json:"yesterdayInput"`
	YesterdayOutput int                              `json:"yesterdayOutput"`
	YesterdayCacheCreation int                        `json:"yesterdayCacheCreation"`
	YesterdayCacheRead     int                        `json:"yesterdayCacheRead"`
	ModelToday     map[string]map[string]int          `json:"modelToday"`
	ModelYesterday map[string]map[string]int          `json:"modelYesterday"`
	HourlyToday    map[string]map[string]int          `json:"hourlyToday"`
	HourlyTzLocal  bool                              `json:"hourlyTzLocal"`
	History        map[string]map[string]interface{}  `json:"history"`
}

// NewTokenUsageTracker 创建新的用量追踪器并从磁盘加载缓存
func NewTokenUsageTracker(cacheDir string) *TokenUsageTracker {
	t := &TokenUsageTracker{
		today:          todayStr(),
		modelToday:     make(map[string]map[string]int),
		modelYesterday: make(map[string]map[string]int),
		hourlyToday:    make(map[string]map[string]int),
		dailyHistory:   make(map[string]map[string]interface{}),
	}
	if cacheDir != "" {
		t.cachePath = filepath.Join(cacheDir, "kiro_token_usage.json")
	}
	t.load()
	return t
}

// Report 累加一次请求的 token 用量
func (t *TokenUsageTracker) Report(model string, inputTokens, outputTokens, cacheCreation, cacheRead int) {
	t.mu.Lock()
	t.maybeRotate()
	t.todayInput += inputTokens
	t.todayOutput += outputTokens
	t.todayCacheCreation += cacheCreation
	t.todayCacheRead += cacheRead

	// 模型维度
	if _, ok := t.modelToday[model]; !ok {
		t.modelToday[model] = map[string]int{"input": 0, "output": 0, "cache_creation": 0, "cache_read": 0}
	}
	t.modelToday[model]["input"] += inputTokens
	t.modelToday[model]["output"] += outputTokens
	t.modelToday[model]["cache_creation"] += cacheCreation
	t.modelToday[model]["cache_read"] += cacheRead

	// 小时级
	hour := time.Now().Format("15")
	if _, ok := t.hourlyToday[hour]; !ok {
		t.hourlyToday[hour] = map[string]int{"input": 0, "output": 0, "cache_creation": 0, "cache_read": 0}
	}
	t.hourlyToday[hour]["input"] += inputTokens
	t.hourlyToday[hour]["output"] += outputTokens
	t.hourlyToday[hour]["cache_creation"] += cacheCreation
	t.hourlyToday[hour]["cache_read"] += cacheRead

	t.dirty = true
	t.mu.Unlock()

	t.saveDebounced()
}

// GetStats 返回今日/昨日/每模型汇总
func (t *TokenUsageTracker) GetStats() map[string]interface{} {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.maybeRotate()

	// 合并今日和昨日的模型列表
	allModels := make(map[string]struct{})
	for m := range t.modelToday {
		allModels[m] = struct{}{}
	}
	for m := range t.modelYesterday {
		allModels[m] = struct{}{}
	}

	models := make(map[string]interface{}, len(allModels))
	for m := range allModels {
		todayData := map[string]int{"input": 0, "output": 0}
		if v, ok := t.modelToday[m]; ok {
			todayData = copyIntMap(v)
		}
		yesterdayData := map[string]int{"input": 0, "output": 0}
		if v, ok := t.modelYesterday[m]; ok {
			yesterdayData = copyIntMap(v)
		}
		models[m] = map[string]interface{}{
			"today":     todayData,
			"yesterday": yesterdayData,
		}
	}

	return map[string]interface{}{
		"today":     map[string]int{"input": t.todayInput, "output": t.todayOutput, "cache_creation": t.todayCacheCreation, "cache_read": t.todayCacheRead},
		"yesterday": map[string]int{"input": t.yesterdayInput, "output": t.yesterdayOutput, "cache_creation": t.yesterdayCacheCreation, "cache_read": t.yesterdayCacheRead},
		"models":    models,
	}
}

// GetHistory 返回最近 N 天的日聚合数据（含今日）
func (t *TokenUsageTracker) GetHistory(days int) map[string]interface{} {
	if days < 1 {
		days = 1
	}
	if days > MaxHistoryDays {
		days = MaxHistoryDays
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	t.maybeRotate()

	result := make(map[string]interface{})

	// 今日
	todayModels := make(map[string]interface{}, len(t.modelToday))
	for k, v := range t.modelToday {
		todayModels[k] = copyIntMap(v)
	}
	result[t.today] = map[string]interface{}{
		"input":  t.todayInput,
		"output": t.todayOutput,
		"models": todayModels,
	}

	// 历史
	todayDt, _ := time.Parse("2006-01-02", t.today)
	for i := 1; i < days; i++ {
		dateStr := todayDt.AddDate(0, 0, -i).Format("2006-01-02")
		if entry, ok := t.dailyHistory[dateStr]; ok {
			result[dateStr] = map[string]interface{}{
				"input":  getIntFromInterface(entry["input"]),
				"output": getIntFromInterface(entry["output"]),
				"models": entry["models"],
			}
		} else if i == 1 {
			// 昨日数据可能还没进 history（当天首次轮转前）
			yesterdayModels := make(map[string]interface{}, len(t.modelYesterday))
			for k, v := range t.modelYesterday {
				yesterdayModels[k] = copyIntMap(v)
			}
			result[dateStr] = map[string]interface{}{
				"input":  t.yesterdayInput,
				"output": t.yesterdayOutput,
				"models": yesterdayModels,
			}
		}
	}

	return result
}

// GetHourly 返回今日 24 小时的用量分布
func (t *TokenUsageTracker) GetHourly() map[string]interface{} {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.maybeRotate()

	result := make(map[string]interface{}, 24)
	for h := 0; h < 24; h++ {
		key := fmt.Sprintf("%02d", h)
		if entry, ok := t.hourlyToday[key]; ok {
			result[key] = copyIntMap(entry)
		} else {
			result[key] = map[string]int{"input": 0, "output": 0, "cache_creation": 0, "cache_read": 0}
		}
	}
	return result
}

// Flush 强制落盘（进程退出前调用）
func (t *TokenUsageTracker) Flush() {
	t.mu.Lock()
	dirty := t.dirty
	t.mu.Unlock()
	if dirty {
		t.save()
	}
}

// ---- 日期轮转 ----

// maybeRotate 检查日期是否变更，如变更则执行轮转。调用方须持有 mu 锁。
func (t *TokenUsageTracker) maybeRotate() {
	nowStr := todayStr()
	if nowStr == t.today {
		return
	}

	// 把今日数据归档到 dailyHistory
	todayModels := make(map[string]interface{}, len(t.modelToday))
	for k, v := range t.modelToday {
		todayModels[k] = copyIntMap(v)
	}
	t.dailyHistory[t.today] = map[string]interface{}{
		"input":  t.todayInput,
		"output": t.todayOutput,
		"models": todayModels,
	}

	// 清理超过 MaxHistoryDays 的旧数据
	t.trimHistory()

	// 今日 → 昨日
	t.yesterdayInput = t.todayInput
	t.yesterdayOutput = t.todayOutput
	t.yesterdayCacheCreation = t.todayCacheCreation
	t.yesterdayCacheRead = t.todayCacheRead
	t.modelYesterday = t.modelToday

	// 重置今日
	t.todayInput = 0
	t.todayOutput = 0
	t.todayCacheCreation = 0
	t.todayCacheRead = 0
	t.modelToday = make(map[string]map[string]int)
	t.hourlyToday = make(map[string]map[string]int)
	t.today = nowStr
	t.dirty = true
}

// trimHistory 清理超过 MaxHistoryDays 的历史数据
func (t *TokenUsageTracker) trimHistory() {
	if len(t.dailyHistory) <= MaxHistoryDays {
		return
	}
	dates := make([]string, 0, len(t.dailyHistory))
	for d := range t.dailyHistory {
		dates = append(dates, d)
	}
	sort.Strings(dates)
	excess := len(dates) - MaxHistoryDays
	for _, d := range dates[:excess] {
		delete(t.dailyHistory, d)
	}
}

// ---- 持久化 ----

// load 从 JSON 文件加载缓存数据
func (t *TokenUsageTracker) load() {
	if t.cachePath == "" {
		return
	}

	data, err := os.ReadFile(t.cachePath)
	if err != nil {
		if !os.IsNotExist(err) {
			logger.Errorf("解析 token 用量缓存失败: %v", err)
		}
		return
	}

	var cf cacheFile
	if err := json.Unmarshal(data, &cf); err != nil {
		logger.Errorf("解析 token 用量缓存失败: %v", err)
		return
	}

	savedDate := cf.Date
	today := todayStr()

	// 加载历史
	if cf.History != nil {
		t.dailyHistory = cf.History
	}

	if savedDate == today {
		// 存档日期是今天，直接恢复
		t.todayInput = cf.TodayInput
		t.todayOutput = cf.TodayOutput
		t.todayCacheCreation = cf.TodayCacheCreation
		t.todayCacheRead = cf.TodayCacheRead
		t.yesterdayInput = cf.YesterdayInput
		t.yesterdayOutput = cf.YesterdayOutput
		t.yesterdayCacheCreation = cf.YesterdayCacheCreation
		t.yesterdayCacheRead = cf.YesterdayCacheRead
		if cf.ModelToday != nil {
			t.modelToday = cf.ModelToday
		}
		if cf.ModelYesterday != nil {
			t.modelYesterday = cf.ModelYesterday
		}
		if cf.HourlyToday != nil {
			t.hourlyToday = cf.HourlyToday
		}
		// 如果小时数据不是本地时区，执行迁移
		if !cf.HourlyTzLocal {
			t.hourlyToday = migrateHourlyUTCToLocal(t.hourlyToday)
			t.dirty = true
		}
	} else {
		// 存档日期不是今天 → 存档数据归档到 history 并变成昨日
		modelToday := make(map[string]interface{})
		if cf.ModelToday != nil {
			for k, v := range cf.ModelToday {
				modelToday[k] = copyIntMap(v)
			}
		}
		t.dailyHistory[savedDate] = map[string]interface{}{
			"input":  cf.TodayInput,
			"output": cf.TodayOutput,
			"models": modelToday,
		}
		t.yesterdayInput = cf.TodayInput
		t.yesterdayOutput = cf.TodayOutput
		if cf.ModelToday != nil {
			t.modelYesterday = cf.ModelToday
		}
		t.today = today
		t.trimHistory()
	}

	logger.Infof("已加载 token 用量缓存 (date=%s, history_days=%d)", savedDate, len(t.dailyHistory))
}

// save 将当前状态写入 JSON 文件
func (t *TokenUsageTracker) save() {
	if t.cachePath == "" {
		return
	}

	t.mu.Lock()
	cf := cacheFile{
		Date:            t.today,
		TodayInput:      t.todayInput,
		TodayOutput:     t.todayOutput,
		TodayCacheCreation: t.todayCacheCreation,
		TodayCacheRead:     t.todayCacheRead,
		YesterdayInput:  t.yesterdayInput,
		YesterdayOutput: t.yesterdayOutput,
		YesterdayCacheCreation: t.yesterdayCacheCreation,
		YesterdayCacheRead:     t.yesterdayCacheRead,
		ModelToday:      t.modelToday,
		ModelYesterday:  t.modelYesterday,
		HourlyToday:     t.hourlyToday,
		HourlyTzLocal:   true,
		History:         t.dailyHistory,
	}
	t.dirty = false
	t.mu.Unlock()

	data, err := json.MarshalIndent(cf, "", "  ")
	if err != nil {
		logger.Errorf("保存 token 用量缓存失败: %v", err)
		return
	}

	// 确保目录存在
	dir := filepath.Dir(t.cachePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		logger.Errorf("保存 token 用量缓存失败: %v", err)
		return
	}

	if err := os.WriteFile(t.cachePath, data, 0o644); err != nil {
		logger.Errorf("保存 token 用量缓存失败: %v", err)
		return
	}

	t.mu.Lock()
	now := monotonicSeconds()
	t.lastSaveAt = &now
	t.mu.Unlock()
}

// saveDebounced 防抖写盘：距上次保存超过 StatsSaveDebounce 秒才执行
func (t *TokenUsageTracker) saveDebounced() {
	t.mu.Lock()
	shouldFlush := t.lastSaveAt == nil || (monotonicSeconds()-*t.lastSaveAt) >= StatsSaveDebounce
	t.mu.Unlock()
	if shouldFlush {
		t.save()
	}
}

// ---- 辅助函数 ----

// todayStr 返回当前本地日期字符串 "2006-01-02"
func todayStr() string {
	return time.Now().Format("2006-01-02")
}

// monotonicSeconds 返回单调递增的秒数（用于防抖计时）
func monotonicSeconds() float64 {
	return float64(time.Now().UnixNano()) / 1e9
}

// copyIntMap 深拷贝 map[string]int
func copyIntMap(src map[string]int) map[string]int {
	dst := make(map[string]int, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

// getIntFromInterface 从 interface{} 中提取 int 值（兼容 JSON 反序列化后的 float64）
func getIntFromInterface(v interface{}) int {
	switch val := v.(type) {
	case int:
		return val
	case float64:
		return int(val)
	case int64:
		return int(val)
	default:
		return 0
	}
}

// migrateHourlyUTCToLocal 一次性将 UTC 小时键偏移到本地时间
func migrateHourlyUTCToLocal(hourly map[string]map[string]int) map[string]map[string]int {
	_, offset := time.Now().Zone()
	offsetHours := offset / 3600
	if offsetHours == 0 {
		return hourly
	}

	migrated := make(map[string]map[string]int, len(hourly))
	for h, v := range hourly {
		var hInt int
		fmt.Sscanf(h, "%d", &hInt)
		newH := fmt.Sprintf("%02d", ((hInt + offsetHours) % 24 + 24) % 24)
		if existing, ok := migrated[newH]; ok {
			existing["input"] += v["input"]
			existing["output"] += v["output"]
		} else {
			migrated[newH] = copyIntMap(v)
		}
	}
	return migrated
}

// ---- 模块级单例 ----

var (
	instance     *TokenUsageTracker
	instanceOnce sync.Once
)

// InitTokenUsageTracker 初始化全局单例（应在程序启动时调用一次）
func InitTokenUsageTracker(cacheDir string) *TokenUsageTracker {
	instanceOnce.Do(func() {
		instance = NewTokenUsageTracker(cacheDir)
	})
	return instance
}

// GetTokenUsageTracker 获取全局单例（未初始化时返回 nil）
func GetTokenUsageTracker() *TokenUsageTracker {
	return instance
}
