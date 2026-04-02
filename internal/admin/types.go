// Package admin Admin API 类型定义 - 参考 admin/types.py
package admin

import "fmt"

// CredentialStatusItem 单个凭据的状态信息
type CredentialStatusItem struct {
	ID                     int      `json:"id"`
	Priority               int      `json:"priority"`
	Disabled               bool     `json:"disabled"`
	FailureCount           int      `json:"failureCount"`
	IsCurrent              bool     `json:"isCurrent"`
	ExpiresAt              *string  `json:"expiresAt,omitempty"`
	AuthMethod             *string  `json:"authMethod,omitempty"`
	HasProfileArn          bool     `json:"hasProfileArn"`
	RefreshTokenHash       *string  `json:"refreshTokenHash,omitempty"`
	Email                  *string  `json:"email,omitempty"`
	SuccessCount           int      `json:"successCount"`
	SessionCount           int      `json:"sessionCount"`
	LastUsedAt             *string  `json:"lastUsedAt,omitempty"`
	HasProxy               bool     `json:"hasProxy"`
	ProxyUrl               *string  `json:"proxyUrl,omitempty"`
	SubscriptionTitle      *string  `json:"subscriptionTitle,omitempty"`
	Group                  *string  `json:"group,omitempty"`
	BalanceScore           *int     `json:"balanceScore,omitempty"`
	BalanceDecay           *int     `json:"balanceDecay,omitempty"`
	BalanceRpm             *int     `json:"balanceRpm,omitempty"`
	BalanceCurrentUsage    *float64 `json:"balanceCurrentUsage,omitempty"`
	BalanceUsageLimit      *float64 `json:"balanceUsageLimit,omitempty"`
	BalanceRemaining       *float64 `json:"balanceRemaining,omitempty"`
	BalanceUsagePercentage *float64 `json:"balanceUsagePercentage,omitempty"`
	BalanceNextResetAt     *float64 `json:"balanceNextResetAt,omitempty"`
	BalanceUpdatedAt       *string  `json:"balanceUpdatedAt,omitempty"`
	DisabledReason         *string  `json:"disabledReason,omitempty"`
}

// ToDict 转换为 map，包含 cachedBalance 子对象
func (c *CredentialStatusItem) ToDict() map[string]interface{} {
	d := map[string]interface{}{
		"id":               c.ID,
		"priority":         c.Priority,
		"disabled":         c.Disabled,
		"failureCount":     c.FailureCount,
		"isCurrent":        c.IsCurrent,
		"expiresAt":        c.ExpiresAt,
		"authMethod":       c.AuthMethod,
		"hasProfileArn":    c.HasProfileArn,
		"refreshTokenHash": c.RefreshTokenHash,
		"email":            c.Email,
		"successCount":     c.SuccessCount,
		"sessionCount":     c.SessionCount,
		"lastUsedAt":       c.LastUsedAt,
		"hasProxy":         c.HasProxy,
		"subscriptionTitle": c.SubscriptionTitle,
		"group":            c.Group,
		"balanceScore":     c.BalanceScore,
		"balanceDecay":     c.BalanceDecay,
		"balanceRpm":       c.BalanceRpm,
		"disabledReason":   c.DisabledReason,
	}

	// 如果有任何余额字段非 nil，构建 cachedBalance 子对象
	if c.BalanceCurrentUsage != nil || c.BalanceUsageLimit != nil ||
		c.BalanceRemaining != nil || c.BalanceUsagePercentage != nil ||
		c.BalanceNextResetAt != nil {

		currentUsage := 0.0
		if c.BalanceCurrentUsage != nil {
			currentUsage = *c.BalanceCurrentUsage
		}
		usageLimit := 0.0
		if c.BalanceUsageLimit != nil {
			usageLimit = *c.BalanceUsageLimit
		}
		remaining := 0.0
		if c.BalanceRemaining != nil {
			remaining = *c.BalanceRemaining
		}
		usagePercentage := 0.0
		if c.BalanceUsagePercentage != nil {
			usagePercentage = *c.BalanceUsagePercentage
		}

		cachedBalance := map[string]interface{}{
			"id":                c.ID,
			"subscriptionTitle": c.SubscriptionTitle,
			"currentUsage":     currentUsage,
			"usageLimit":       usageLimit,
			"remaining":        remaining,
			"usagePercentage":  usagePercentage,
			"nextResetAt":      c.BalanceNextResetAt,
		}
		d["cachedBalance"] = cachedBalance
	}

	if c.BalanceUpdatedAt != nil {
		d["balanceUpdatedAt"] = *c.BalanceUpdatedAt
	}
	if c.ProxyUrl != nil {
		d["proxyUrl"] = *c.ProxyUrl
	}

	return d
}

// CredentialsStatusResponse 所有凭据状态响应
type CredentialsStatusResponse struct {
	Total       int                    `json:"total"`
	Available   int                    `json:"available"`
	CurrentID   int                    `json:"currentId"`
	RPM         int                    `json:"rpm"`
	Credentials []CredentialStatusItem `json:"credentials"`
}

// ToDict 转换为 map
func (r *CredentialsStatusResponse) ToDict() map[string]interface{} {
	creds := make([]map[string]interface{}, 0, len(r.Credentials))
	for i := range r.Credentials {
		creds = append(creds, r.Credentials[i].ToDict())
	}
	return map[string]interface{}{
		"total":       r.Total,
		"available":   r.Available,
		"currentId":   r.CurrentID,
		"rpm":         r.RPM,
		"credentials": creds,
	}
}

// ============ 操作请求 ============

// SetDisabledRequest 设置禁用状态请求
type SetDisabledRequest struct {
	Disabled bool `json:"disabled"`
}

// SetPriorityRequest 设置优先级请求
type SetPriorityRequest struct {
	Priority int `json:"priority"`
}

// AddCredentialRequest 添加凭据请求
type AddCredentialRequest struct {
	RefreshToken  string  `json:"refreshToken"`
	AuthMethod    string  `json:"authMethod"`
	ClientID      *string `json:"clientId,omitempty"`
	ClientSecret  *string `json:"clientSecret,omitempty"`
	Priority      int     `json:"priority"`
	Region        *string `json:"region,omitempty"`
	AuthRegion    *string `json:"authRegion,omitempty"`
	ApiRegion     *string `json:"apiRegion,omitempty"`
	MachineID     *string `json:"machineId,omitempty"`
	Email         *string `json:"email,omitempty"`
	ProxyUrl      *string `json:"proxyUrl,omitempty"`
	ProxyUsername *string `json:"proxyUsername,omitempty"`
	ProxyPassword *string `json:"proxyPassword,omitempty"`
}

// ============ 余额查询 ============

// BalanceResponse 余额响应
type BalanceResponse struct {
	ID                int      `json:"id"`
	SubscriptionTitle *string  `json:"subscriptionTitle,omitempty"`
	CurrentUsage      float64  `json:"currentUsage"`
	UsageLimit        float64  `json:"usageLimit"`
	Remaining         float64  `json:"remaining"`
	UsagePercentage   float64  `json:"usagePercentage"`
	NextResetAt       *float64 `json:"nextResetAt,omitempty"`
}

// ToDict 转换为 map
func (b *BalanceResponse) ToDict() map[string]interface{} {
	return map[string]interface{}{
		"id":                b.ID,
		"subscriptionTitle": b.SubscriptionTitle,
		"currentUsage":     b.CurrentUsage,
		"usageLimit":       b.UsageLimit,
		"remaining":        b.Remaining,
		"usagePercentage":  b.UsagePercentage,
		"nextResetAt":      b.NextResetAt,
	}
}

// ============ 通用响应 ============

// SuccessResponse 成功响应
type SuccessResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// ToDict 转换为 map
func (s *SuccessResponse) ToDict() map[string]interface{} {
	return map[string]interface{}{
		"success": s.Success,
		"message": s.Message,
	}
}

// NewSuccessResponse 创建成功响应
func NewSuccessResponse(message string) *SuccessResponse {
	return &SuccessResponse{Success: true, Message: message}
}

// AddCredentialResponse 添加凭据响应
type AddCredentialResponse struct {
	Success      bool    `json:"success"`
	Message      string  `json:"message"`
	CredentialID int     `json:"credentialId"`
	Email        *string `json:"email,omitempty"`
}

// ToDict 转换为 map
func (r *AddCredentialResponse) ToDict() map[string]interface{} {
	d := map[string]interface{}{
		"success":      r.Success,
		"message":      r.Message,
		"credentialId": r.CredentialID,
	}
	if r.Email != nil {
		d["email"] = *r.Email
	}
	return d
}

// ============ 错误类型 ============

// NotFoundError 凭据不存在错误
type NotFoundError struct {
	ID int
}

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("凭据不存在：%d", e.ID)
}

// UpstreamError 上游服务错误
type UpstreamError struct {
	Message string
}

func (e *UpstreamError) Error() string {
	return fmt.Sprintf("上游服务错误：%s", e.Message)
}

// InternalError 内部错误
type InternalError struct {
	Message string
}

func (e *InternalError) Error() string {
	return e.Message
}

// InvalidCredentialError 无效凭据错误
type InvalidCredentialError struct {
	Message string
}

func (e *InvalidCredentialError) Error() string {
	return fmt.Sprintf("无效凭据：%s", e.Message)
}

// BadRequestError 错误请求
type BadRequestError struct {
	Message string
}

func (e *BadRequestError) Error() string {
	return e.Message
}

// NotImplementedError 未实现错误
type NotImplementedError struct {
	Message string
}

func (e *NotImplementedError) Error() string {
	return e.Message
}

// ============ 错误响应 ============

// AdminErrorResponse 错误响应
type AdminErrorResponse struct {
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// NewAdminError 创建错误响应
func NewAdminError(errorType, message string) AdminErrorResponse {
	resp := AdminErrorResponse{}
	resp.Error.Type = errorType
	resp.Error.Message = message
	return resp
}

// AuthenticationError 认证错误
func AuthenticationError() AdminErrorResponse {
	return NewAdminError("authentication_error", "Invalid or missing admin API key")
}

// InvalidRequestError 无效请求错误
func InvalidRequestError(message string) AdminErrorResponse {
	return NewAdminError("invalid_request", message)
}

// NotFoundErrorResponse 资源未找到错误响应
func NotFoundErrorResponse(message string) AdminErrorResponse {
	return NewAdminError("not_found", message)
}

// ApiError API 错误
func ApiError(message string) AdminErrorResponse {
	return NewAdminError("api_error", message)
}
