// Package tokenmanager Token 管理器 - 参考 clauldcode-proxy/kiro/token_manager.py
package tokenmanager

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"kiro-proxy/internal/config"
	"kiro-proxy/internal/httpclient"
	"kiro-proxy/internal/kiro/model"
)

// CredentialSnapshotEntry 凭据快照条目
type CredentialSnapshotEntry struct {
	ID                   int
	Priority             int
	Disabled             bool
	FailureCount         int
	SuccessCount         int
	SessionCount         int
	BalanceScore         *int
	BalanceDecay         *int
	BalanceRPM           *int
	BalanceCurrentUsage  *float64
	BalanceUsageLimit    *float64
	BalanceRemaining     *float64
	BalanceUsagePercentage *float64
	BalanceNextResetAt   *float64
	BalanceUpdatedAt     *string
	ExpiresAt            *string
	AuthMethod           *string
	ProfileArn           *string
	RefreshTokenHash     *string
	Email                *string
	LastUsedAt           *string
	ProxyURL             *string
	SubscriptionTitle    *string
	DisabledReason       *string
}

// CredentialSnapshot 凭据快照
type CredentialSnapshot struct {
	Total       int
	Available   int
	CurrentID   int
	Entries     []CredentialSnapshotEntry
}

// MultiTokenManager 多凭据 Token 管理器
type MultiTokenManager struct {
	config           *config.Config
	credentials      []model.KiroCredentials
	globalProxy      *httpclient.ProxyConfig
	credentialsPath  string
	isMultipleFormat bool
	
	mu              sync.RWMutex
	currentIndex    int
	failureCounts   map[int]int
	successCounts   map[int]int
	sessionCounts   map[int]int
	lastUsedAt      map[int]time.Time
	disabled        map[int]bool
	priorities      map[int]int
	
	balanceScores   map[int]int
	balanceDecays   map[int]int
	balanceRPM      map[int]int
	
	statsMu            sync.Mutex
	requestTimestamps  []float64         // RPM 滑动窗口（最近 60 秒）
	peakRPM            int               // 峰值 RPM
	modelCallCounts    map[string]int    // 每模型本次会话调用计数

	groups     map[int]string    // {credential_id: "free"|"pro"|"priority"}
	freeModels map[string]struct{} // 免费账号支持的模型 ID 集合
}

// NewMultiTokenManager 创建多凭据 Token 管理器
func NewMultiTokenManager(
	cfg *config.Config,
	credentials []model.KiroCredentials,
	proxy *httpclient.ProxyConfig,
	credentialsPath string,
	isMultipleFormat bool,
) (*MultiTokenManager, error) {
	tm := &MultiTokenManager{
		config:             cfg,
		credentials:        credentials,
		globalProxy:        proxy,
		credentialsPath:    credentialsPath,
		isMultipleFormat:   isMultipleFormat,
		currentIndex:       -1,
		failureCounts:      make(map[int]int),
		successCounts:      make(map[int]int),
		sessionCounts:      make(map[int]int),
		lastUsedAt:         make(map[int]time.Time),
		disabled:           make(map[int]bool),
		priorities:         make(map[int]int),
		balanceScores:      make(map[int]int),
		balanceDecays:      make(map[int]int),
		balanceRPM:         make(map[int]int),
		modelCallCounts:    make(map[string]int),
		groups:             make(map[int]string),
		freeModels:         make(map[string]struct{}),
	}
	
	// 初始化凭据索引
	for i, cred := range credentials {
		if cred.ID != nil {
			tm.priorities[*cred.ID] = cred.Priority
			tm.disabled[*cred.ID] = cred.Disabled
		} else {
			id := i + 1
			tm.priorities[id] = cred.Priority
			tm.disabled[id] = cred.Disabled
		}
	}
	
	// 选择第一个可用的凭据
	tm.selectCurrentCredential()
	
	return tm, nil
}

// selectCurrentCredential 选择当前凭据
func (tm *MultiTokenManager) selectCurrentCredential() {
	for i, cred := range tm.credentials {
		id := i + 1
		if cred.ID != nil {
			id = *cred.ID
		}
		
		if !tm.disabled[id] {
			tm.currentIndex = i
			break
		}
	}
}

// GetCurrentCredential 获取当前凭据
func (tm *MultiTokenManager) GetCurrentCredential() *model.KiroCredentials {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	
	if tm.currentIndex < 0 || tm.currentIndex >= len(tm.credentials) {
		return nil
	}
	return &tm.credentials[tm.currentIndex]
}

// GetCredentialByID 根据 ID 获取凭据
func (tm *MultiTokenManager) GetCredentialByID(id int) *model.KiroCredentials {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	
	for _, cred := range tm.credentials {
		credID := 0
		if cred.ID != nil {
			credID = *cred.ID
		}
		if credID == id {
			return &cred
		}
	}
	return nil
}

// Snapshot 获取凭据快照
func (tm *MultiTokenManager) Snapshot() *CredentialSnapshot {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	
	entries := make([]CredentialSnapshotEntry, 0, len(tm.credentials))
	available := 0
	
	for i, cred := range tm.credentials {
		id := i + 1
		if cred.ID != nil {
			id = *cred.ID
		}
		
		if !tm.disabled[id] {
			available++
		}
		
		entry := CredentialSnapshotEntry{
			ID:                 id,
			Priority:           cred.Priority,
			Disabled:           tm.disabled[id],
			FailureCount:       tm.failureCounts[id],
			SuccessCount:       tm.successCounts[id],
			SessionCount:       tm.sessionCounts[id],
			BalanceScore:       tm.getBalanceScore(id),
			BalanceDecay:       tm.getBalanceDecay(id),
			BalanceRPM:         tm.getBalanceRPM(id),
			BalanceCurrentUsage: cred.BalanceCurrentUsage,
			BalanceUsageLimit:   cred.BalanceUsageLimit,
			BalanceRemaining:    cred.BalanceRemaining,
			BalanceUsagePercentage: cred.BalanceUsagePercentage,
			BalanceNextResetAt: cred.BalanceNextResetAt,
			BalanceUpdatedAt:   cred.BalanceUpdatedAt,
			ExpiresAt:          cred.ExpiresAt,
			AuthMethod:         cred.AuthMethod,
			ProfileArn:         cred.ProfileArn,
			RefreshTokenHash:   tm.getRefreshTokenHash(&cred),
			Email:              cred.Email,
			LastUsedAt:         tm.getLastUsedAt(id),
			ProxyURL:           cred.ProxyUrl,
			SubscriptionTitle:  cred.SubscriptionTitle,
		}
		
		entries = append(entries, entry)
	}
	
	currentID := 0
	if tm.currentIndex >= 0 && tm.currentIndex < len(tm.credentials) {
		cred := tm.credentials[tm.currentIndex]
		if cred.ID != nil {
			currentID = *cred.ID
		} else {
			currentID = tm.currentIndex + 1
		}
	}
	
	return &CredentialSnapshot{
		Total:       len(tm.credentials),
		Available:   available,
		CurrentID:   currentID,
		Entries:     entries,
	}
}

// getRefreshTokenHash 获取 refreshToken 的哈希值
func (tm *MultiTokenManager) getRefreshTokenHash(cred *model.KiroCredentials) *string {
	if cred.RefreshToken == nil || *cred.RefreshToken == "" {
		return nil
	}
	// TODO: 实现 SHA256 哈希
	hash := "placeholder"
	return &hash
}

// getLastUsedAt 获取最后使用时间（调用方必须已持有 mu 读锁或写锁）
func (tm *MultiTokenManager) getLastUsedAt(id int) *string {
	if t, ok := tm.lastUsedAt[id]; ok {
		s := t.Format(time.RFC3339)
		return &s
	}
	return nil
}

// getBalanceScore 获取平衡分数（调用方必须已持有 mu 读锁或写锁）
func (tm *MultiTokenManager) getBalanceScore(id int) *int {
	if score, ok := tm.balanceScores[id]; ok {
		return &score
	}
	return nil
}

// getBalanceDecay 获取平衡衰减（调用方必须已持有 mu 读锁或写锁）
func (tm *MultiTokenManager) getBalanceDecay(id int) *int {
	if decay, ok := tm.balanceDecays[id]; ok {
		return &decay
	}
	return nil
}

// getBalanceRPM 获取 RPM（调用方必须已持有 mu 读锁或写锁）
func (tm *MultiTokenManager) getBalanceRPM(id int) *int {
	if rpm, ok := tm.balanceRPM[id]; ok {
		return &rpm
	}
	return nil
}

// SetDisabled 设置凭据禁用状态
func (tm *MultiTokenManager) SetDisabled(id int, disabled bool) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	
	if _, ok := tm.disabled[id]; !ok {
		return fmt.Errorf("凭据不存在：%d", id)
	}
	
	tm.disabled[id] = disabled
	
	// 如果禁用了当前凭据，需要切换到下一个
	if disabled && id == tm.getCurrentID() {
		tm.selectNextCredential()
	}
	
	return nil
}

// getCurrentID 获取当前凭据 ID
func (tm *MultiTokenManager) getCurrentID() int {
	if tm.currentIndex < 0 || tm.currentIndex >= len(tm.credentials) {
		return 0
	}
	cred := tm.credentials[tm.currentIndex]
	if cred.ID != nil {
		return *cred.ID
	}
	return tm.currentIndex + 1
}

// selectNextCredential 选择下一个凭据
func (tm *MultiTokenManager) selectNextCredential() {
	for i := 0; i < len(tm.credentials); i++ {
		idx := (tm.currentIndex + 1 + i) % len(tm.credentials)
		id := idx + 1
		if tm.credentials[idx].ID != nil {
			id = *tm.credentials[idx].ID
		}
		
		if !tm.disabled[id] {
			tm.currentIndex = idx
			return
		}
	}
	tm.currentIndex = -1
}

// SwitchToNext 切换到下一个凭据
func (tm *MultiTokenManager) SwitchToNext() error {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	
	tm.selectNextCredential()
	if tm.currentIndex < 0 {
		return fmt.Errorf("没有可用的凭据")
	}
	return nil
}

// SetPriority 设置凭据优先级
func (tm *MultiTokenManager) SetPriority(id int, priority int) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	
	if _, ok := tm.priorities[id]; !ok {
		return fmt.Errorf("凭据不存在：%d", id)
	}
	
	tm.priorities[id] = priority
	
	// 更新凭据列表中的优先级
	for i := range tm.credentials {
		credID := i + 1
		if tm.credentials[i].ID != nil {
			credID = *tm.credentials[i].ID
		}
		if credID == id {
			tm.credentials[i].Priority = priority
			break
		}
	}
	
	return nil
}

// ResetAndEnable 重置失败计数并重新启用
func (tm *MultiTokenManager) ResetAndEnable(id int) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	
	if _, ok := tm.disabled[id]; !ok {
		return fmt.Errorf("凭据不存在：%d", id)
	}
	
	tm.failureCounts[id] = 0
	tm.disabled[id] = false
	
	return nil
}

// ResetAllCounters 重置所有计数器
func (tm *MultiTokenManager) ResetAllCounters() {
	tm.mu.Lock()
	for id := range tm.failureCounts {
		tm.failureCounts[id] = 0
	}
	for id := range tm.successCounts {
		tm.successCounts[id] = 0
	}
	for id := range tm.sessionCounts {
		tm.sessionCounts[id] = 0
	}
	for id := range tm.balanceScores {
		tm.balanceScores[id] = 0
	}
	for id := range tm.balanceDecays {
		tm.balanceDecays[id] = 0
	}
	for id := range tm.balanceRPM {
		tm.balanceRPM[id] = 0
	}
	tm.mu.Unlock()

	tm.statsMu.Lock()
	tm.requestTimestamps = tm.requestTimestamps[:0]
	tm.peakRPM = 0
	tm.modelCallCounts = make(map[string]int)
	tm.statsMu.Unlock()
}

// DeleteCredential 删除凭据
func (tm *MultiTokenManager) DeleteCredential(id int) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	
	// 查找凭据索引
	foundIdx := -1
	for i, cred := range tm.credentials {
		credID := i + 1
		if cred.ID != nil {
			credID = *cred.ID
		}
		if credID == id {
			foundIdx = i
			break
		}
	}
	
	if foundIdx < 0 {
		return fmt.Errorf("凭据不存在：%d", id)
	}
	
	// 只能删除已禁用的凭据
	if !tm.disabled[id] {
		return fmt.Errorf("只能删除已禁用的凭据，请先禁用凭据 #%d", id)
	}
	
	// 从列表中移除
	tm.credentials = append(tm.credentials[:foundIdx], tm.credentials[foundIdx+1:]...)
	
	// 清理相关数据
	delete(tm.failureCounts, id)
	delete(tm.successCounts, id)
	delete(tm.sessionCounts, id)
	delete(tm.lastUsedAt, id)
	delete(tm.disabled, id)
	delete(tm.priorities, id)
	delete(tm.balanceScores, id)
	delete(tm.balanceDecays, id)
	delete(tm.balanceRPM, id)
	
	// 如果删除的是当前凭据，切换到下一个
	if foundIdx == tm.currentIndex {
		tm.selectNextCredential()
	} else if foundIdx < tm.currentIndex {
		tm.currentIndex--
	}
	
	return nil
}

// AddCredential 添加新凭据
func (tm *MultiTokenManager) AddCredential(ctx context.Context, cred model.KiroCredentials) (int, error) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	
	// 生成新 ID
	newID := len(tm.credentials) + 1
	credWithID := cred.Clone()
	credWithID.ID = &newID
	
	tm.credentials = append(tm.credentials, credWithID)
	tm.disabled[newID] = cred.Disabled
	tm.priorities[newID] = cred.Priority
	tm.failureCounts[newID] = 0
	tm.successCounts[newID] = 0
	tm.sessionCounts[newID] = 0
	
	return newID, nil
}

// GetUsageLimitsFor 获取指定凭据的使用额度（自动刷新过期 token）
func (tm *MultiTokenManager) GetUsageLimitsFor(ctx context.Context, id int) (*model.UsageLimitsResponse, error) {
	cred := tm.GetCredentialByID(id)
	if cred == nil {
		return nil, fmt.Errorf("凭据不存在：%d", id)
	}

	// token 过期或即将过期时先刷新
	if IsTokenExpired(cred) || IsTokenExpiringSoon(cred) {
		effectiveProxy := cred.EffectiveProxy(tm.globalProxy)
		newCred, err := RefreshToken(ctx, cred, tm.config, effectiveProxy)
		if err != nil {
			return nil, fmt.Errorf("Token 刷新失败: %w", err)
		}
		// 写回 token manager
		tm.mu.Lock()
		for i := range tm.credentials {
			credID := i + 1
			if tm.credentials[i].ID != nil {
				credID = *tm.credentials[i].ID
			}
			if credID == id {
				tm.credentials[i].AccessToken = newCred.AccessToken
				tm.credentials[i].ExpiresAt = newCred.ExpiresAt
				if newCred.RefreshToken != nil {
					tm.credentials[i].RefreshToken = newCred.RefreshToken
				}
				break
			}
		}
		tm.mu.Unlock()
		cred = newCred
	}

	token := cred.AccessToken
	if token == nil || *token == "" {
		return nil, fmt.Errorf("凭据缺少 accessToken")
	}

	return GetUsageLimits(ctx, cred, tm.config, *token, tm.globalProxy)
}

// RecordRequest 记录一次请求（更新 RPM 滑动窗口、峰值、模型计数、会话计数）
// model 可为空字符串。credID 为发起请求的凭据 ID，0 表示未知。
func (tm *MultiTokenManager) RecordRequest(credID int, mdl string) {
	now := float64(time.Now().UnixNano()) / 1e9
	cutoff := now - 60.0

	tm.statsMu.Lock()
	// 滑动窗口
	filtered := tm.requestTimestamps[:0]
	for _, t := range tm.requestTimestamps {
		if t > cutoff {
			filtered = append(filtered, t)
		}
	}
	tm.requestTimestamps = append(filtered, now)
	currentRPM := len(tm.requestTimestamps)
	if currentRPM > tm.peakRPM {
		tm.peakRPM = currentRPM
	}
	// 模型计数
	if mdl != "" {
		tm.modelCallCounts[mdl]++
	}
	tm.statsMu.Unlock()

	// 凭据维度计数（需要写锁）
	if credID > 0 {
		tm.mu.Lock()
		tm.successCounts[credID]++
		tm.sessionCounts[credID]++
		tm.lastUsedAt[credID] = time.Now()
		tm.mu.Unlock()
	}
}

// GetStats 获取统计信息
func (tm *MultiTokenManager) GetStats() map[string]interface{} {
	now := float64(time.Now().UnixNano()) / 1e9
	cutoff := now - 60.0

	tm.statsMu.Lock()
	// 清理过期时间戳，计算当前 RPM
	filtered := tm.requestTimestamps[:0]
	for _, t := range tm.requestTimestamps {
		if t > cutoff {
			filtered = append(filtered, t)
		}
	}
	tm.requestTimestamps = filtered
	rpm := len(tm.requestTimestamps)
	peakRPM := tm.peakRPM
	modelCounts := make(map[string]int, len(tm.modelCallCounts))
	for k, v := range tm.modelCallCounts {
		modelCounts[k] = v
	}
	tm.statsMu.Unlock()

	tm.mu.RLock()
	totalRequests := 0
	sessionRequests := 0
	for _, cred := range tm.credentials {
		id := 0
		if cred.ID != nil {
			id = *cred.ID
		}
		totalRequests += tm.successCounts[id]
		sessionRequests += tm.sessionCounts[id]
	}
	tm.mu.RUnlock()

	return map[string]interface{}{
		"rpm":             rpm,
		"peakRpm":         peakRPM,
		"totalRequests":   totalRequests,
		"sessionRequests": sessionRequests,
		"modelCounts":     modelCounts,
	}
}

// CacheDir 返回缓存目录
func (tm *MultiTokenManager) CacheDir() string {
	if tm.credentialsPath == "" {
		return ""
	}
	abs, err := filepath.Abs(tm.credentialsPath)
	if err != nil {
		return filepath.Dir(tm.credentialsPath)
	}
	dir := filepath.Dir(abs)
	if _, err := os.Stat(dir); err != nil {
		return ""
	}
	return dir
}

// CredentialsPath 返回凭据文件路径
func (tm *MultiTokenManager) CredentialsPath() string {
	return tm.credentialsPath
}

// Config 返回配置
func (tm *MultiTokenManager) Config() *config.Config {
	return tm.config
}

// UpdateCredentialBalance 将余额数据写回指定凭据（供 admin service 在查询后同步）
func (tm *MultiTokenManager) UpdateCredentialBalance(
	id int,
	currentUsage, usageLimit, remaining, usagePercentage float64,
	nextResetAt *float64,
	subscriptionTitle *string,
) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	now := time.Now().UTC().Format(time.RFC3339)
	for i := range tm.credentials {
		credID := i + 1
		if tm.credentials[i].ID != nil {
			credID = *tm.credentials[i].ID
		}
		if credID == id {
			tm.credentials[i].BalanceCurrentUsage = &currentUsage
			tm.credentials[i].BalanceUsageLimit = &usageLimit
			tm.credentials[i].BalanceRemaining = &remaining
			tm.credentials[i].BalanceUsagePercentage = &usagePercentage
			tm.credentials[i].BalanceNextResetAt = nextResetAt
			tm.credentials[i].BalanceUpdatedAt = &now
			if subscriptionTitle != nil {
				tm.credentials[i].SubscriptionTitle = subscriptionTitle
			}
			return
		}
	}
}

// UpdateCredentialToken 将刷新后的 token 写回指定凭据
func (tm *MultiTokenManager) UpdateCredentialToken(idx int, newCred *model.KiroCredentials) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	if idx < 0 || idx >= len(tm.credentials) {
		return
	}
	tm.credentials[idx].AccessToken = newCred.AccessToken
	tm.credentials[idx].ExpiresAt = newCred.ExpiresAt
	if newCred.RefreshToken != nil {
		tm.credentials[idx].RefreshToken = newCred.RefreshToken
	}
}

// GetCurrentIndex 返回当前凭据的索引（调用方需持有读锁外部不可修改）
func (tm *MultiTokenManager) GetCurrentIndex() int {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	return tm.currentIndex
}

// UpdateGroups 更新分组信息（用于路由决策）
func (tm *MultiTokenManager) UpdateGroups(groups map[int]string) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	tm.groups = make(map[int]string, len(groups))
	for k, v := range groups {
		tm.groups[k] = v
	}
}

// GetFreeModels 获取免费模型列表
func (tm *MultiTokenManager) GetFreeModels() []string {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	result := make([]string, 0, len(tm.freeModels))
	for m := range tm.freeModels {
		result = append(result, m)
	}
	return result
}

// UpdateFreeModels 更新免费模型列表
func (tm *MultiTokenManager) UpdateFreeModels(models map[string]struct{}) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	tm.freeModels = make(map[string]struct{}, len(models))
	for m := range models {
		tm.freeModels[m] = struct{}{}
	}
}
