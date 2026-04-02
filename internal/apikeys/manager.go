// Package apikeys 多 API Key 管理 — 分组、额度、计费倍率、月度重置
package apikeys

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"kiro-proxy/internal/logger"
)

// Group 分组配置
type Group struct {
	Rate         float64 `json:"rate"`
	MonthlyQuota int     `json:"monthlyQuota"`
}

// KeyEntry 单个 API Key 条目
type KeyEntry struct {
	Key            string                       `json:"key"`
	Name           string                       `json:"name"`
	Group          string                       `json:"group"`
	Rate           *float64                     `json:"rate,omitempty"`
	MonthlyQuota   *int                         `json:"monthlyQuota,omitempty"`
	BilledTokens   float64                      `json:"billedTokens"`
	BilledMonth    string                       `json:"billedMonth"`
	TotalRawTokens float64                      `json:"totalRawTokens"`
	RequestCount   int                          `json:"requestCount"`
	Enabled        bool                         `json:"enabled"`
	CreatedAt      string                       `json:"createdAt"`
	ModelCounts    map[string]int               `json:"modelCounts,omitempty"`
	ModelTokens    map[string]map[string]int    `json:"modelTokens,omitempty"`
	DailyUsage     map[string]map[string]int    `json:"dailyUsage,omitempty"`
	HourlyUsage    map[string]map[string]int    `json:"hourlyUsage,omitempty"`
	HourlyDate     string                       `json:"hourlyDate,omitempty"`
	HourlyTzLocal  bool                         `json:"hourlyTzLocal,omitempty"`
}

// persistData 持久化文件格式
type persistData struct {
	Groups        map[string]*Group    `json:"groups"`
	Keys          []*KeyEntry          `json:"keys"`
	ExtraTracking map[string]*KeyEntry `json:"extraTracking"`
}

// ApiKeyManager 多 API Key 管理器
type ApiKeyManager struct {
	mu            sync.Mutex
	path          string // api_keys.json 路径，空字符串表示不持久化
	groups        map[string]*Group
	keys          []*KeyEntry
	keyIndex      map[string]*KeyEntry
	extraTracking map[string]*KeyEntry // 非托管 key 的用量追踪
	dirty         bool
	lastSave      float64
}

// NewApiKeyManager 创建管理器，dataDir 非空时持久化到 dataDir/api_keys.json
func NewApiKeyManager(dataDir string) *ApiKeyManager {
	m := &ApiKeyManager{
		groups:        make(map[string]*Group),
		keys:          make([]*KeyEntry, 0),
		keyIndex:      make(map[string]*KeyEntry),
		extraTracking: make(map[string]*KeyEntry),
	}
	if dataDir != "" {
		m.path = filepath.Join(dataDir, "api_keys.json")
	}
	m.load()
	return m
}

// ---- 查询 ----

// Lookup 根据 key 字符串查找，自动月度重置
func (m *ApiKeyManager) Lookup(keyStr string) *KeyEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry := m.keyIndex[keyStr]
	if entry == nil {
		return nil
	}
	m.maybeResetMonth(entry)
	cp := *entry
	return &cp
}

// CheckQuota 检查额度，返回 (allowed, reason)
// reason: "key_not_found", "key_disabled", "quota_exceeded", ""
func (m *ApiKeyManager) CheckQuota(keyStr string) (bool, string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry := m.keyIndex[keyStr]
	if entry == nil {
		return false, "key_not_found"
	}
	if !entry.Enabled {
		return false, "key_disabled"
	}
	m.maybeResetMonth(entry)
	quota := m.effectiveQuota(entry)
	if quota < 0 { // -1 = 无限制
		return true, ""
	}
	if entry.BilledTokens >= float64(quota) {
		return false, "quota_exceeded"
	}
	return true, ""
}

// ReportUsage 上报 token 用量，按倍率计入额度，同时追踪模型维度
func (m *ApiKeyManager) ReportUsage(keyStr string, inputTokens, outputTokens int, model string) {
	m.mu.Lock()
	entry := m.keyIndex[keyStr]
	if entry == nil {
		// 非托管 key（管理员），自动创建追踪条目
		entry = m.extraTracking[keyStr]
		if entry == nil {
			entry = &KeyEntry{
				Name:  "管理员",
				Group: "admin",
			}
			m.extraTracking[keyStr] = entry
		}
	}
	m.maybeResetMonth(entry)
	rate := m.effectiveRate(entry)
	raw := float64(inputTokens + outputTokens)
	billed := raw * rate
	entry.BilledTokens += billed
	entry.TotalRawTokens += raw
	entry.RequestCount++

	// 按模型追踪
	if model != "" {
		if entry.ModelCounts == nil {
			entry.ModelCounts = make(map[string]int)
		}
		entry.ModelCounts[model]++
		if entry.ModelTokens == nil {
			entry.ModelTokens = make(map[string]map[string]int)
		}
		if entry.ModelTokens[model] == nil {
			entry.ModelTokens[model] = map[string]int{"input": 0, "output": 0}
		}
		entry.ModelTokens[model]["input"] += inputTokens
		entry.ModelTokens[model]["output"] += outputTokens
	}

	// 按天追踪（保留最近 31 天）
	today := time.Now().Format("2006-01-02")
	if entry.DailyUsage == nil {
		entry.DailyUsage = make(map[string]map[string]int)
	}
	if entry.DailyUsage[today] == nil {
		entry.DailyUsage[today] = map[string]int{"input": 0, "output": 0}
	}
	entry.DailyUsage[today]["input"] += inputTokens
	entry.DailyUsage[today]["output"] += outputTokens

	// 按小时追踪（仅当天）
	hour := time.Now().Format("15")
	if entry.HourlyDate != today {
		entry.HourlyUsage = make(map[string]map[string]int)
		entry.HourlyDate = today
	}
	if entry.HourlyUsage == nil {
		entry.HourlyUsage = make(map[string]map[string]int)
	}
	if entry.HourlyUsage[hour] == nil {
		entry.HourlyUsage[hour] = map[string]int{"input": 0, "output": 0}
	}
	entry.HourlyUsage[hour]["input"] += inputTokens
	entry.HourlyUsage[hour]["output"] += outputTokens

	m.dirty = true
	m.mu.Unlock()
	m.saveDebounced()
}

// GetAllKeys 返回所有 key 信息，隐藏完整 key（前7后4），包含 effectiveRate, effectiveQuota
func (m *ApiKeyManager) GetAllKeys() []map[string]interface{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]map[string]interface{}, 0, len(m.keys))
	for _, e := range m.keys {
		m.maybeResetMonth(e)
		d := keyEntryToMap(e)
		d["effectiveRate"] = m.effectiveRate(e)
		d["effectiveQuota"] = m.effectiveQuota(e)
		k := e.Key
		if len(k) > 12 {
			d["maskedKey"] = k[:7] + "..." + k[len(k)-4:]
		} else {
			d["maskedKey"] = k
		}
		result = append(result, d)
	}
	return result
}

// GetGroups 返回所有分组
func (m *ApiKeyManager) GetGroups() map[string]*Group {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make(map[string]*Group, len(m.groups))
	for k, v := range m.groups {
		g := *v
		cp[k] = &g
	}
	return cp
}

// GetUsageStats 返回每个 key 的用量统计（不含 key 字符串）
func (m *ApiKeyManager) GetUsageStats() []map[string]interface{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	today := time.Now().Format("2006-01-02")
	all := make([]*KeyEntry, 0, len(m.keys)+len(m.extraTracking))
	all = append(all, m.keys...)
	for _, e := range m.extraTracking {
		all = append(all, e)
	}
	result := make([]map[string]interface{}, 0, len(all))
	for _, e := range all {
		m.maybeResetMonth(e)
		if e.HourlyDate != today {
			e.HourlyUsage = make(map[string]map[string]int)
			e.HourlyDate = today
		}
		modelTokensCopy := make(map[string]map[string]int)
		for model, v := range e.ModelTokens {
			inner := make(map[string]int)
			for tk, tv := range v {
				inner[tk] = tv
			}
			modelTokensCopy[model] = inner
		}
		modelCountsCopy := make(map[string]int)
		for k, v := range e.ModelCounts {
			modelCountsCopy[k] = v
		}
		dailyCopy := make(map[string]map[string]int)
		for d, v := range e.DailyUsage {
			inner := make(map[string]int)
			for tk, tv := range v {
				inner[tk] = tv
			}
			dailyCopy[d] = inner
		}
		hourlyCopy := make(map[string]map[string]int)
		for h, v := range e.HourlyUsage {
			inner := make(map[string]int)
			for tk, tv := range v {
				inner[tk] = tv
			}
			hourlyCopy[h] = inner
		}
		result = append(result, map[string]interface{}{
			"name":           e.Name,
			"group":          e.Group,
			"modelCounts":    modelCountsCopy,
			"modelTokens":    modelTokensCopy,
			"dailyUsage":     dailyCopy,
			"hourlyUsage":    hourlyCopy,
			"billedTokens":   e.BilledTokens,
			"totalRawTokens": e.TotalRawTokens,
			"requestCount":   e.RequestCount,
		})
	}
	return result
}

// ---- 分组管理 ----

// SetGroup 创建或更新分组
func (m *ApiKeyManager) SetGroup(name string, rate float64, monthlyQuota int) {
	m.mu.Lock()
	m.groups[name] = &Group{Rate: rate, MonthlyQuota: monthlyQuota}
	m.dirty = true
	m.mu.Unlock()
	m.save()
}

// DeleteGroup 删除分组，如果有 key 在用返回 false
func (m *ApiKeyManager) DeleteGroup(name string) bool {
	m.mu.Lock()
	if _, ok := m.groups[name]; !ok {
		m.mu.Unlock()
		return false
	}
	for _, e := range m.keys {
		if e.Group == name {
			m.mu.Unlock()
			return false
		}
	}
	delete(m.groups, name)
	m.dirty = true
	m.mu.Unlock()
	m.save()
	return true
}

// ---- Key 管理 ----

// AddKey 新增 key，生成 "sk-" + 48位随机十六进制
func (m *ApiKeyManager) AddKey(name, group string, rate *float64, monthlyQuota *int) *KeyEntry {
	keyStr := genKey()
	entry := &KeyEntry{
		Key:          keyStr,
		Name:         name,
		Group:        group,
		Rate:         rate,
		MonthlyQuota: monthlyQuota,
		BilledTokens: 0,
		BilledMonth:  currentMonth(),
		Enabled:      true,
		CreatedAt:    time.Now().UTC().Format(time.RFC3339),
	}
	m.mu.Lock()
	m.keys = append(m.keys, entry)
	m.keyIndex[keyStr] = entry
	m.dirty = true
	m.mu.Unlock()
	m.save()
	cp := *entry
	return &cp
}

// UpdateKey 更新 key 字段，允许更新 name, group, rate, monthlyQuota, enabled
func (m *ApiKeyManager) UpdateKey(keyStr string, fields map[string]interface{}) *KeyEntry {
	m.mu.Lock()
	entry := m.keyIndex[keyStr]
	if entry == nil {
		m.mu.Unlock()
		return nil
	}
	allowed := map[string]bool{"name": true, "group": true, "rate": true, "monthlyQuota": true, "enabled": true}
	for k, v := range fields {
		if !allowed[k] {
			continue
		}
		switch k {
		case "name":
			if s, ok := v.(string); ok {
				entry.Name = s
			}
		case "group":
			if s, ok := v.(string); ok {
				entry.Group = s
			}
		case "rate":
			switch val := v.(type) {
			case float64:
				entry.Rate = &val
			case nil:
				entry.Rate = nil
			}
		case "monthlyQuota":
			switch val := v.(type) {
			case int:
				entry.MonthlyQuota = &val
			case float64:
				iv := int(val)
				entry.MonthlyQuota = &iv
			case nil:
				entry.MonthlyQuota = nil
			}
		case "enabled":
			if b, ok := v.(bool); ok {
				entry.Enabled = b
			}
		}
	}
	m.dirty = true
	m.mu.Unlock()
	m.save()
	cp := *entry
	return &cp
}

// RegenerateKey 重新生成 key 字符串
func (m *ApiKeyManager) RegenerateKey(oldKey string) *KeyEntry {
	newKey := genKey()
	m.mu.Lock()
	entry := m.keyIndex[oldKey]
	if entry == nil {
		m.mu.Unlock()
		return nil
	}
	delete(m.keyIndex, oldKey)
	entry.Key = newKey
	m.keyIndex[newKey] = entry
	m.dirty = true
	m.mu.Unlock()
	m.save()
	cp := *entry
	return &cp
}

// DeleteKey 删除 key
func (m *ApiKeyManager) DeleteKey(keyStr string) bool {
	m.mu.Lock()
	entry := m.keyIndex[keyStr]
	if entry == nil {
		m.mu.Unlock()
		return false
	}
	delete(m.keyIndex, keyStr)
	for i, e := range m.keys {
		if e == entry {
			m.keys = append(m.keys[:i], m.keys[i+1:]...)
			break
		}
	}
	m.dirty = true
	m.mu.Unlock()
	m.save()
	return true
}

// ResetUsage 重置 key 的用量统计
func (m *ApiKeyManager) ResetUsage(keyStr string) bool {
	m.mu.Lock()
	entry := m.keyIndex[keyStr]
	if entry == nil {
		m.mu.Unlock()
		return false
	}
	entry.BilledTokens = 0
	entry.BilledMonth = currentMonth()
	m.dirty = true
	m.mu.Unlock()
	m.save()
	return true
}

// Flush 强制落盘
func (m *ApiKeyManager) Flush() {
	m.mu.Lock()
	dirty := m.dirty
	m.mu.Unlock()
	if dirty {
		m.save()
	}
}

// ---- 内部方法 ----

// effectiveRate entry.Rate 优先，否则 group.Rate，否则 1.0
func (m *ApiKeyManager) effectiveRate(entry *KeyEntry) float64 {
	if entry.Rate != nil {
		return *entry.Rate
	}
	if g, ok := m.groups[entry.Group]; ok {
		return g.Rate
	}
	return 1.0
}

// effectiveQuota entry.MonthlyQuota 优先，否则 group.MonthlyQuota，否则 -1
func (m *ApiKeyManager) effectiveQuota(entry *KeyEntry) int {
	if entry.MonthlyQuota != nil {
		return *entry.MonthlyQuota
	}
	if g, ok := m.groups[entry.Group]; ok {
		return g.MonthlyQuota
	}
	return -1
}

// maybeResetMonth 如果 billedMonth != currentMonth，重置 billedTokens（调用方持锁）
func (m *ApiKeyManager) maybeResetMonth(entry *KeyEntry) {
	month := currentMonth()
	if entry.BilledMonth != month {
		entry.BilledTokens = 0
		entry.BilledMonth = month
		m.dirty = true
	}
}

// currentMonth 返回 "2006-01" 格式的当前月份
func currentMonth() string {
	return time.Now().Format("2006-01")
}

// genKey 生成 "sk-" + 48位随机十六进制
func genKey() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("生成随机 key 失败: %v", err))
	}
	return "sk-" + hex.EncodeToString(b)
}

// ---- 持久化 ----

func (m *ApiKeyManager) load() {
	if m.path == "" {
		return
	}
	data, err := os.ReadFile(m.path)
	if err != nil {
		if !os.IsNotExist(err) {
			logger.Errorf("加载 api_keys.json 失败: %v", err)
		}
		return
	}
	var pd persistData
	if err := json.Unmarshal(data, &pd); err != nil {
		logger.Errorf("解析 api_keys.json 失败: %v", err)
		return
	}
	if pd.Groups != nil {
		m.groups = pd.Groups
	}
	if pd.Keys != nil {
		m.keys = pd.Keys
	}
	m.keyIndex = make(map[string]*KeyEntry, len(m.keys))
	for _, e := range m.keys {
		if e.Key != "" {
			m.keyIndex[e.Key] = e
		}
	}
	if pd.ExtraTracking != nil {
		m.extraTracking = pd.ExtraTracking
	}
	// 一次性迁移 UTC 小时键到本地时间
	all := make([]*KeyEntry, 0, len(m.keys)+len(m.extraTracking))
	all = append(all, m.keys...)
	for _, e := range m.extraTracking {
		all = append(all, e)
	}
	for _, e := range all {
		if e.HourlyUsage != nil && !e.HourlyTzLocal {
			e.HourlyUsage = migrateHourlyUTCToLocal(e.HourlyUsage)
			e.HourlyTzLocal = true
			m.dirty = true
		}
	}
	logger.Infof("已加载 %d 个 API key, %d 个分组", len(m.keys), len(m.groups))
}

func (m *ApiKeyManager) save() {
	if m.path == "" {
		return
	}
	m.mu.Lock()
	// 清理超过 31 天的 dailyUsage
	cutoff := time.Now().AddDate(0, 0, -31).Format("2006-01-02")
	all := make([]*KeyEntry, 0, len(m.keys)+len(m.extraTracking))
	all = append(all, m.keys...)
	for _, e := range m.extraTracking {
		all = append(all, e)
	}
	for _, e := range all {
		if e.DailyUsage != nil {
			for k := range e.DailyUsage {
				if k < cutoff {
					delete(e.DailyUsage, k)
				}
			}
		}
	}
	pd := persistData{
		Groups:        m.groups,
		Keys:          m.keys,
		ExtraTracking: m.extraTracking,
	}
	m.dirty = false
	// 在锁内序列化，防止并发 map 读写 panic
	b, err := json.MarshalIndent(pd, "", "  ")
	m.mu.Unlock()

	if err != nil {
		logger.Errorf("序列化 api_keys.json 失败: %v", err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(m.path), 0o755); err != nil {
		logger.Errorf("创建目录失败: %v", err)
		return
	}
	if err := os.WriteFile(m.path, b, 0o644); err != nil {
		logger.Errorf("保存 api_keys.json 失败: %v", err)
	}
}

func (m *ApiKeyManager) saveDebounced() {
	now := float64(time.Now().UnixNano()) / 1e9
	m.mu.Lock()
	last := m.lastSave
	m.mu.Unlock()
	if now-last >= 3 {
		m.save()
		m.mu.Lock()
		m.lastSave = now
		m.mu.Unlock()
	}
}

// ---- 辅助函数 ----

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
		newH := fmt.Sprintf("%02d", (hInt+offsetHours+24)%24)
		if migrated[newH] == nil {
			inner := make(map[string]int)
			for k, val := range v {
				inner[k] = val
			}
			migrated[newH] = inner
		} else {
			migrated[newH]["input"] += v["input"]
			migrated[newH]["output"] += v["output"]
		}
	}
	return migrated
}

// keyEntryToMap 将 KeyEntry 转为 map（用于 GetAllKeys 返回）
func keyEntryToMap(e *KeyEntry) map[string]interface{} {
	d := map[string]interface{}{
		"key":            e.Key,
		"name":           e.Name,
		"group":          e.Group,
		"billedTokens":   e.BilledTokens,
		"billedMonth":    e.BilledMonth,
		"totalRawTokens": e.TotalRawTokens,
		"requestCount":   e.RequestCount,
		"enabled":        e.Enabled,
		"createdAt":      e.CreatedAt,
	}
	if e.Rate != nil {
		d["rate"] = *e.Rate
	}
	if e.MonthlyQuota != nil {
		d["monthlyQuota"] = *e.MonthlyQuota
	}
	if e.ModelCounts != nil {
		d["modelCounts"] = e.ModelCounts
	}
	if e.ModelTokens != nil {
		d["modelTokens"] = e.ModelTokens
	}
	if e.DailyUsage != nil {
		d["dailyUsage"] = e.DailyUsage
	}
	if e.HourlyUsage != nil {
		d["hourlyUsage"] = e.HourlyUsage
	}
	return d
}

// sortedKeys 返回 map 的排序键（供调试/测试用）
func sortedKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// 消除 unused import 警告（strings、sort 在其他方法中可能用到）
var _ = strings.Contains
var _ = sortedKeys

// ---- 全局单例 ----

var (
	instance     *ApiKeyManager
	instanceOnce sync.Once
)

// InitApiKeyManager 初始化全局单例
func InitApiKeyManager(dataDir string) *ApiKeyManager {
	instanceOnce.Do(func() {
		instance = NewApiKeyManager(dataDir)
	})
	return instance
}

// GetApiKeyManager 获取全局单例
func GetApiKeyManager() *ApiKeyManager {
	return instance
}
