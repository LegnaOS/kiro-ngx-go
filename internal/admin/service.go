// Package admin Admin API 业务逻辑服务 - 参考 clauldcode-proxy/admin/service.py
package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"kiro-proxy/internal/kiro/model"
	"kiro-proxy/internal/kiro/tokenmanager"
	"kiro-proxy/internal/logger"
	"kiro-proxy/internal/tokenusage"
)

const (
	// BalanceCacheTTLSecs 余额缓存过期时间（秒）
	BalanceCacheTTLSecs = 1800
	// AutoBalanceRefreshIntervalSecs 后台自动余额刷新间隔（秒）
	AutoBalanceRefreshIntervalSecs = 3600
)

// BalanceCacheEntry 余额缓存条目
type BalanceCacheEntry struct {
	CachedAt time.Time
	Data     *BalanceResponse
}

// Service Admin 服务，封装所有 Admin API 的业务逻辑
type Service struct {
	tokenManager *tokenmanager.MultiTokenManager
	balanceCache map[int]*BalanceCacheEntry
	cacheLock    sync.RWMutex
	cachePath    string
	groups       map[int]string // {credential_id: "free"|"pro"|"priority"}
	groupsPath   string
	routingPath  string
	customModels []string
	autoBalanceStop chan struct{}
	autoBalanceDone chan struct{}
}

// NewService 创建 Admin 服务实例
func NewService(tm *tokenmanager.MultiTokenManager) *Service {
	s := &Service{
		tokenManager:    tm,
		balanceCache:    make(map[int]*BalanceCacheEntry),
		groups:          make(map[int]string),
		autoBalanceStop: make(chan struct{}),
		autoBalanceDone: make(chan struct{}),
	}

	cacheDir := tm.CacheDir()
	if cacheDir != "" {
		s.cachePath = filepath.Join(cacheDir, "kiro_balance_cache.json")
		s.groupsPath = filepath.Join(cacheDir, "kiro_groups.json")
		s.routingPath = filepath.Join(cacheDir, "kiro_routing.json")
	}

	s.loadBalanceCache()
	s.loadGroups()
	s.loadRouting()
	s.SyncGroupsToManager()

	return s
}

// GetAllCredentials 获取所有凭据状态
func (s *Service) GetAllCredentials() (*CredentialsStatusResponse, error) {
	snapshot := s.tokenManager.Snapshot()
	
	s.cacheLock.RLock()
	defer s.cacheLock.RUnlock()
	
	credentials := make([]CredentialStatusItem, 0, len(snapshot.Entries))
	
	for _, entry := range snapshot.Entries {
		// 自动分组：FREE → free，其他 → pro（除非手动设为 priority）
		var cached *BalanceCacheEntry
		if e, ok := s.balanceCache[entry.ID]; ok {
			cached = e
		}
		
		subTitle := ""
		if entry.SubscriptionTitle != nil {
			subTitle = *entry.SubscriptionTitle
		} else if cached != nil && cached.Data.SubscriptionTitle != nil {
			subTitle = *cached.Data.SubscriptionTitle
		}
		
		savedGroup := s.groups[entry.ID]
		isFree := subTitle != "" && containsIgnoreCase(subTitle, "FREE")
		
		var group string
		if isFree {
			group = "free"
		} else if savedGroup == "pro" || savedGroup == "priority" {
			group = savedGroup
		} else {
			group = "pro"
		}
		
		subTitlePtr := func() *string {
			if subTitle == "" {
				return nil
			}
			s := subTitle
			return &s
		}()
		groupPtr := func() *string { s := group; return &s }()

		item := CredentialStatusItem{
			ID:                 entry.ID,
			Priority:           entry.Priority,
			Disabled:           entry.Disabled,
			FailureCount:       entry.FailureCount,
			IsCurrent:          entry.ID == snapshot.CurrentID,
			ExpiresAt:          entry.ExpiresAt,
			AuthMethod:         entry.AuthMethod,
			HasProfileArn:      entry.ProfileArn != nil && *entry.ProfileArn != "",
			RefreshTokenHash:   entry.RefreshTokenHash,
			Email:              entry.Email,
			SuccessCount:       entry.SuccessCount,
			SessionCount:       entry.SessionCount,
			LastUsedAt:         entry.LastUsedAt,
			HasProxy:           entry.ProxyURL != nil && *entry.ProxyURL != "",
			ProxyUrl:           entry.ProxyURL,
			SubscriptionTitle:  subTitlePtr,
			Group:              groupPtr,
			BalanceScore:       entry.BalanceScore,
			BalanceDecay:       entry.BalanceDecay,
			BalanceRpm:         entry.BalanceRPM,
			BalanceCurrentUsage: entry.BalanceCurrentUsage,
			BalanceUsageLimit:   entry.BalanceUsageLimit,
			BalanceRemaining:    entry.BalanceRemaining,
			BalanceUsagePercentage: entry.BalanceUsagePercentage,
			BalanceNextResetAt: entry.BalanceNextResetAt,
			BalanceUpdatedAt:   entry.BalanceUpdatedAt,
			DisabledReason:     entry.DisabledReason,
		}
		
		credentials = append(credentials, item)
	}
	
	stats := s.tokenManager.GetStats()
	rpm := 0
	if v, ok := stats["rpm"]; ok {
		switch r := v.(type) {
		case float64:
			rpm = int(r)
		case int:
			rpm = r
		}
	}

	return &CredentialsStatusResponse{
		Total:       snapshot.Total,
		Available:   snapshot.Available,
		CurrentID:   snapshot.CurrentID,
		RPM:         rpm,
		Credentials: credentials,
	}, nil
}

// SetDisabled 设置凭据禁用状态
func (s *Service) SetDisabled(id int, disabled bool) error {
	snapshot := s.tokenManager.Snapshot()
	currentID := snapshot.CurrentID
	
	if err := s.tokenManager.SetDisabled(id, disabled); err != nil {
		return classifyError(err, id)
	}
	
	// 禁用当前凭据时切换到下一个
	if disabled && id == currentID {
		_ = s.tokenManager.SwitchToNext()
	}
	
	return nil
}

// SetPriority 设置凭据优先级
func (s *Service) SetPriority(id int, priority int) error {
	if err := s.tokenManager.SetPriority(id, priority); err != nil {
		return classifyError(err, id)
	}
	return nil
}

// ResetAndEnable 重置失败计数并重新启用
func (s *Service) ResetAndEnable(id int) error {
	if err := s.tokenManager.ResetAndEnable(id); err != nil {
		return classifyError(err, id)
	}
	return nil
}

// GetBalance 获取凭据余额（带缓存）
func (s *Service) GetBalance(ctx context.Context, id int, forceRefresh bool) (*BalanceResponse, error) {
	if !forceRefresh {
		s.cacheLock.RLock()
		cached, ok := s.balanceCache[id]
		s.cacheLock.RUnlock()
		
		if ok && time.Since(cached.CachedAt).Seconds() < BalanceCacheTTLSecs {
			return cached.Data, nil
		}
	}
	
	balance, err := s.fetchBalance(ctx, id)
	if err != nil {
		return nil, err
	}
	
	s.cacheLock.Lock()
	s.balanceCache[id] = &BalanceCacheEntry{
		CachedAt: time.Now(),
		Data:     balance,
	}
	s.cacheLock.Unlock()

	// 将余额数据写回 credentials 对象，使 Snapshot() 能直接读到最新值
	s.tokenManager.UpdateCredentialBalance(id, balance.CurrentUsage, balance.UsageLimit,
		balance.Remaining, balance.UsagePercentage, balance.NextResetAt,
		balance.SubscriptionTitle)

	s.saveBalanceCache()

	return balance, nil
}

// fetchBalance 从上游获取余额
func (s *Service) fetchBalance(ctx context.Context, id int) (*BalanceResponse, error) {
	usage, err := s.tokenManager.GetUsageLimitsFor(ctx, id)
	if err != nil {
		return nil, classifyBalanceError(err, id)
	}
	
	currentUsage := usage.CurrentUsageTotal()
	usageLimit := usage.UsageLimitTotal()
	remaining := max(0.0, usageLimit-currentUsage)
	usagePercentage := 0.0
	if usageLimit > 0 {
		usagePercentage = min(100.0, currentUsage/usageLimit*100.0)
	}
	
	return &BalanceResponse{
		ID:                id,
		SubscriptionTitle: usage.SubscriptionTitle(),
		CurrentUsage:      currentUsage,
		UsageLimit:        usageLimit,
		Remaining:         remaining,
		UsagePercentage:   usagePercentage,
		NextResetAt:       usage.NextDateReset,
	}, nil
}

// StartAutoBalanceRefresh 启动后台余额自动刷新
func (s *Service) StartAutoBalanceRefresh() {
	go s.autoBalanceRefreshLoop()
}

// StopAutoBalanceRefresh 停止后台余额自动刷新
func (s *Service) StopAutoBalanceRefresh() {
	close(s.autoBalanceStop)
	<-s.autoBalanceDone
}

// autoBalanceRefreshLoop 后台余额自动刷新循环
func (s *Service) autoBalanceRefreshLoop() {
	ticker := time.NewTicker(AutoBalanceRefreshIntervalSecs * time.Second)
	defer ticker.Stop()
	defer close(s.autoBalanceDone)
	
	for {
		select {
		case <-ticker.C:
			snapshot := s.tokenManager.Snapshot()
			successCount := 0
			failCount := 0
			
			for _, entry := range snapshot.Entries {
				ctx := context.Background()
				_, err := s.GetBalance(ctx, entry.ID, true)
				if err != nil {
					failCount++
				} else {
					successCount++
				}
			}
			logger.Infof("自动余额刷新完成：成功 %d/%d，失败 %d",
				successCount, len(snapshot.Entries), failCount)
		case <-s.autoBalanceStop:
			return
		}
	}
}

// GetAvailableCredentialCounts 获取总凭据数和可用凭据数
func (s *Service) GetAvailableCredentialCounts() map[string]int {
	snapshot := s.tokenManager.Snapshot()
	return map[string]int{
		"total":     snapshot.Total,
		"available": snapshot.Available,
	}
}

// GetStats 获取统计信息
func (s *Service) GetStats() map[string]interface{} {
	stats := s.tokenManager.GetStats()
	// 补充前端必需的字段，避免 Object.entries(null) 崩溃
	if _, ok := stats["modelCounts"]; !ok {
		stats["modelCounts"] = map[string]interface{}{}
	}
	if _, ok := stats["sessionRequests"]; !ok {
		stats["sessionRequests"] = 0
	}
	if _, ok := stats["peakRpm"]; !ok {
		stats["peakRpm"] = 0
	}
	// 合并 tokenusage 数据
	if tracker := tokenusage.GetTokenUsageTracker(); tracker != nil {
		stats["tokenUsage"] = tracker.GetStats()
	} else {
		stats["tokenUsage"] = map[string]interface{}{
			"today":     map[string]interface{}{"input": 0, "output": 0},
			"yesterday": map[string]interface{}{"input": 0, "output": 0},
			"models":    map[string]interface{}{},
		}
	}
	return stats
}

// ResetAllCounters 重置所有计数器
func (s *Service) ResetAllCounters() {
	s.tokenManager.ResetAllCounters()
}

// DeleteCredential 删除凭据
func (s *Service) DeleteCredential(id int) error {
	if err := s.tokenManager.DeleteCredential(id); err != nil {
		return classifyError(err, id)
	}
	
	s.cacheLock.Lock()
	delete(s.balanceCache, id)
	s.cacheLock.Unlock()

	s.saveBalanceCache()

	return nil
}

// AddCredential 添加新凭据（验证 token 后加入）
func (s *Service) AddCredential(ctx context.Context, req AddCredentialRequest) (*AddCredentialResponse, error) {
	if req.RefreshToken == "" {
		return nil, &InvalidCredentialError{Message: "refreshToken 不能为空"}
	}

	authMethod := req.AuthMethod
	cred := model.KiroCredentials{
		RefreshToken: &req.RefreshToken,
		AuthMethod:   &authMethod,
		Priority:     req.Priority,
		Region:       req.Region,
		AuthRegion:   req.AuthRegion,
		ApiRegion:    req.ApiRegion,
		MachineID:    req.MachineID,
		Email:        req.Email,
		ClientID:     req.ClientID,
		ClientSecret: req.ClientSecret,
		ProxyUrl:     req.ProxyUrl,
		ProxyUsername: req.ProxyUsername,
		ProxyPassword: req.ProxyPassword,
	}

	id, err := s.tokenManager.AddCredential(ctx, cred)
	if err != nil {
		return nil, &InternalError{Message: err.Error()}
	}

	return &AddCredentialResponse{
		Success:      true,
		Message:      fmt.Sprintf("凭据 #%d 已添加", id),
		CredentialID: id,
		Email:        req.Email,
	}, nil
}

// GetRawCredentials 读取 credentials.json 原始内容
func (s *Service) GetRawCredentials() (string, error) {
	path := s.tokenManager.CredentialsPath()
	if path == "" {
		return "[]", nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "[]", nil
		}
		return "", err
	}
	return string(data), nil
}

// SaveRawCredentials 写入 credentials.json
func (s *Service) SaveRawCredentials(content string) error {
	var parsed []interface{}
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		return &BadRequestError{Message: fmt.Sprintf("JSON 格式错误: %v", err)}
	}
	path := s.tokenManager.CredentialsPath()
	if path == "" {
		return &InternalError{Message: "未配置凭据文件路径"}
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return &InternalError{Message: fmt.Sprintf("写入文件失败: %v", err)}
	}
	return nil
}
func (s *Service) SetCredentialGroup(id int, group string) error {
	if group != "free" && group != "pro" && group != "priority" {
		return &InvalidCredentialError{Message: fmt.Sprintf("无效的分组：%s", group)}
	}
	
	s.groups[id] = group
	s.saveGroups()
	s.SyncGroupsToManager()

	return nil
}

// SetCredentialGroupsBatch 批量设置凭据分组
func (s *Service) SetCredentialGroupsBatch(groups map[int]string) {
	for id, group := range groups {
		if group == "free" || group == "pro" || group == "priority" {
			s.groups[id] = group
		}
	}
	s.saveGroups()
	s.SyncGroupsToManager()
}

// SyncGroupsToManager 将分组信息同步到 token_manager
func (s *Service) SyncGroupsToManager() {
	snapshot := s.tokenManager.Snapshot()
	fullGroups := make(map[int]string)
	
	for _, entry := range snapshot.Entries {
		subTitle := ""
		if entry.SubscriptionTitle != nil {
			subTitle = *entry.SubscriptionTitle
		}
		
		savedGroup := s.groups[entry.ID]
		isFree := subTitle != "" && containsIgnoreCase(subTitle, "FREE")
		
		if isFree {
			fullGroups[entry.ID] = "free"
		} else if savedGroup == "pro" || savedGroup == "priority" {
			fullGroups[entry.ID] = savedGroup
		} else {
			fullGroups[entry.ID] = "pro"
		}
	}
	
	s.tokenManager.UpdateGroups(fullGroups)
}

// GetFreeModels 获取免费模型列表
func (s *Service) GetFreeModels() []string {
	return s.tokenManager.GetFreeModels()
}

// SetFreeModels 设置免费模型列表
func (s *Service) SetFreeModels(models []string) {
	set := make(map[string]struct{}, len(models))
	for _, m := range models {
		set[m] = struct{}{}
	}
	s.tokenManager.UpdateFreeModels(set)
	s.saveRouting()
}

// GetCustomModels 获取自定义模型列表
func (s *Service) GetCustomModels() []string {
	return s.customModels
}

// SetCustomModels 设置自定义模型列表
func (s *Service) SetCustomModels(models []string) {
	s.customModels = models
	s.saveRouting()
}

// ============ 余额缓存持久化 ============

type balanceCacheFile map[string]struct {
	CachedAt float64         `json:"cached_at"`
	Data     *BalanceResponse `json:"data"`
}

func (s *Service) loadBalanceCache() {
	if s.cachePath == "" {
		return
	}
	data, err := os.ReadFile(s.cachePath)
	if err != nil {
		return
	}
	var raw map[string]struct {
		CachedAt float64 `json:"cached_at"`
		Data     struct {
			ID                int      `json:"id"`
			SubscriptionTitle *string  `json:"subscriptionTitle"`
			CurrentUsage      float64  `json:"currentUsage"`
			UsageLimit        float64  `json:"usageLimit"`
			Remaining         float64  `json:"remaining"`
			UsagePercentage   float64  `json:"usagePercentage"`
			NextResetAt       *float64 `json:"nextResetAt"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return
	}
	s.cacheLock.Lock()
	defer s.cacheLock.Unlock()
	for k, v := range raw {
		var id int
		fmt.Sscanf(k, "%d", &id)
		bal := &BalanceResponse{
			ID:                v.Data.ID,
			SubscriptionTitle: v.Data.SubscriptionTitle,
			CurrentUsage:      v.Data.CurrentUsage,
			UsageLimit:        v.Data.UsageLimit,
			Remaining:         v.Data.Remaining,
			UsagePercentage:   v.Data.UsagePercentage,
			NextResetAt:       v.Data.NextResetAt,
		}
		s.balanceCache[id] = &BalanceCacheEntry{
			CachedAt: time.Unix(int64(v.CachedAt), 0),
			Data:     bal,
		}
		// 启动时将缓存余额写回 credentials，使 Snapshot() 能直接读到
		s.tokenManager.UpdateCredentialBalance(id, bal.CurrentUsage, bal.UsageLimit,
			bal.Remaining, bal.UsagePercentage, bal.NextResetAt, bal.SubscriptionTitle)
	}
}

func (s *Service) saveBalanceCache() {
	if s.cachePath == "" {
		return
	}
	s.cacheLock.RLock()
	out := make(map[string]interface{}, len(s.balanceCache))
	for id, entry := range s.balanceCache {
		out[fmt.Sprintf("%d", id)] = map[string]interface{}{
			"cached_at": float64(entry.CachedAt.Unix()),
			"data":      entry.Data,
		}
	}
	s.cacheLock.RUnlock()
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(s.cachePath), 0o755)
	_ = os.WriteFile(s.cachePath, data, 0o644)
}

// ============ 分组持久化 ============

func (s *Service) loadGroups() {
	if s.groupsPath == "" {
		return
	}
	data, err := os.ReadFile(s.groupsPath)
	if err != nil {
		return
	}
	var raw map[string]string
	if err := json.Unmarshal(data, &raw); err != nil {
		return
	}
	for k, v := range raw {
		var id int
		fmt.Sscanf(k, "%d", &id)
		s.groups[id] = v
	}
}

func (s *Service) saveGroups() {
	if s.groupsPath == "" {
		return
	}
	out := make(map[string]string, len(s.groups))
	for id, g := range s.groups {
		out[fmt.Sprintf("%d", id)] = g
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(s.groupsPath), 0o755)
	_ = os.WriteFile(s.groupsPath, data, 0o644)
}

// ============ 路由配置持久化 ============

func (s *Service) loadRouting() {
	if s.routingPath == "" {
		return
	}
	data, err := os.ReadFile(s.routingPath)
	if err != nil {
		return
	}
	var raw struct {
		FreeModels   []string `json:"freeModels"`
		CustomModels []string `json:"customModels"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return
	}
	set := make(map[string]struct{}, len(raw.FreeModels))
	for _, m := range raw.FreeModels {
		set[m] = struct{}{}
	}
	s.tokenManager.UpdateFreeModels(set)
	s.customModels = raw.CustomModels
}

func (s *Service) saveRouting() {
	if s.routingPath == "" {
		return
	}
	freeModels := s.tokenManager.GetFreeModels()
	out := map[string]interface{}{
		"freeModels":   freeModels,
		"customModels": s.customModels,
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(s.routingPath), 0o755)
	_ = os.WriteFile(s.routingPath, data, 0o644)
}

// containsIgnoreCase 检查字符串是否包含子串（忽略大小写）
func containsIgnoreCase(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

// max 返回两个 float64 中的较大值
func max(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

// min 返回两个 float64 中的较小值
func min(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

// classifyError 分类错误
func classifyError(err error, id int) error {
	msg := err.Error()
	if msg == "凭据不存在" {
		return &NotFoundError{ID: id}
	}
	return &InternalError{Message: msg}
}

// classifyBalanceError 分类余额查询错误
func classifyBalanceError(err error, id int) error {
	msg := err.Error()
	upstreamKeywords := []string{"凭证已过期", "权限不足", "已被限流", "服务器错误", "timeout"}
	
	for _, kw := range upstreamKeywords {
		if containsIgnoreCase(msg, kw) {
			return &UpstreamError{Message: msg}
		}
	}
	
	return &InternalError{Message: msg}
}
