package model

// 使用额度类型 - 参考 src/kiro/model/usage_limits.rs

// Bonus 额外额度
type Bonus struct {
	CurrentUsage float64 `json:"currentUsage"`
	UsageLimit   float64 `json:"usageLimit"`
	Status       *string `json:"status,omitempty"`
}

// IsActive 判断额度是否激活
func (b *Bonus) IsActive() bool {
	return b.Status != nil && *b.Status == "ACTIVE"
}

// BonusFromDict 从字典构造 Bonus
func BonusFromDict(data map[string]interface{}) Bonus {
	b := Bonus{}
	if v, ok := data["currentUsage"].(float64); ok {
		b.CurrentUsage = v
	}
	if v, ok := data["usageLimit"].(float64); ok {
		b.UsageLimit = v
	}
	if v, ok := data["status"].(string); ok {
		b.Status = &v
	}
	return b
}

// FreeTrialInfo 免费试用信息
type FreeTrialInfo struct {
	CurrentUsage              int      `json:"currentUsage"`
	CurrentUsageWithPrecision float64  `json:"currentUsageWithPrecision"`
	FreeTrialExpiry           *float64 `json:"freeTrialExpiry,omitempty"`
	FreeTrialStatus           *string  `json:"freeTrialStatus,omitempty"`
	UsageLimit                int      `json:"usageLimit"`
	UsageLimitWithPrecision   float64  `json:"usageLimitWithPrecision"`
}

// IsActive 判断免费试用是否激活
func (f *FreeTrialInfo) IsActive() bool {
	return f.FreeTrialStatus != nil && *f.FreeTrialStatus == "ACTIVE"
}

// FreeTrialInfoFromDict 从字典构造 FreeTrialInfo
func FreeTrialInfoFromDict(data map[string]interface{}) FreeTrialInfo {
	f := FreeTrialInfo{}
	if v, ok := data["currentUsage"].(float64); ok {
		f.CurrentUsage = int(v)
	}
	if v, ok := data["currentUsageWithPrecision"].(float64); ok {
		f.CurrentUsageWithPrecision = v
	}
	if v, ok := data["freeTrialExpiry"].(float64); ok {
		f.FreeTrialExpiry = &v
	}
	if v, ok := data["freeTrialStatus"].(string); ok {
		f.FreeTrialStatus = &v
	}
	if v, ok := data["usageLimit"].(float64); ok {
		f.UsageLimit = int(v)
	}
	if v, ok := data["usageLimitWithPrecision"].(float64); ok {
		f.UsageLimitWithPrecision = v
	}
	return f
}

// UsageBreakdown 使用量明细
type UsageBreakdown struct {
	CurrentUsage              int            `json:"currentUsage"`
	CurrentUsageWithPrecision float64        `json:"currentUsageWithPrecision"`
	Bonuses                   []Bonus        `json:"bonuses"`
	FreeTrialInfo             *FreeTrialInfo `json:"freeTrialInfo,omitempty"`
	NextDateReset             *float64       `json:"nextDateReset,omitempty"`
	UsageLimit                int            `json:"usageLimit"`
	UsageLimitWithPrecision   float64        `json:"usageLimitWithPrecision"`
}

// UsageBreakdownFromDict 从字典构造 UsageBreakdown
func UsageBreakdownFromDict(data map[string]interface{}) UsageBreakdown {
	bd := UsageBreakdown{}
	if v, ok := data["currentUsage"].(float64); ok {
		bd.CurrentUsage = int(v)
	}
	if v, ok := data["currentUsageWithPrecision"].(float64); ok {
		bd.CurrentUsageWithPrecision = v
	}
	if v, ok := data["usageLimit"].(float64); ok {
		bd.UsageLimit = int(v)
	}
	if v, ok := data["usageLimitWithPrecision"].(float64); ok {
		bd.UsageLimitWithPrecision = v
	}
	if v, ok := data["nextDateReset"].(float64); ok {
		bd.NextDateReset = &v
	}

	// 解析 bonuses 数组
	if rawBonuses, ok := data["bonuses"].([]interface{}); ok {
		for _, rb := range rawBonuses {
			if bMap, ok := rb.(map[string]interface{}); ok {
				bd.Bonuses = append(bd.Bonuses, BonusFromDict(bMap))
			}
		}
	}
	if bd.Bonuses == nil {
		bd.Bonuses = []Bonus{}
	}

	// 解析 freeTrialInfo
	if rawTrial, ok := data["freeTrialInfo"].(map[string]interface{}); ok {
		trial := FreeTrialInfoFromDict(rawTrial)
		bd.FreeTrialInfo = &trial
	}

	return bd
}

// SubscriptionInfo 订阅信息
type SubscriptionInfo struct {
	SubscriptionTitle *string `json:"subscriptionTitle,omitempty"`
}

// SubscriptionInfoFromDict 从字典构造 SubscriptionInfo
func SubscriptionInfoFromDict(data map[string]interface{}) SubscriptionInfo {
	s := SubscriptionInfo{}
	if v, ok := data["subscriptionTitle"].(string); ok {
		s.SubscriptionTitle = &v
	}
	return s
}

// UsageLimitsResponse 使用额度响应
type UsageLimitsResponse struct {
	NextDateReset      *float64         `json:"nextDateReset,omitempty"`
	SubscriptionInfoV  *SubscriptionInfo `json:"subscriptionInfo,omitempty"`
	UsageBreakdownList []UsageBreakdown `json:"usageBreakdownList"`
}

// UsageLimitsResponseFromDict 从字典构造 UsageLimitsResponse
func UsageLimitsResponseFromDict(data map[string]interface{}) UsageLimitsResponse {
	r := UsageLimitsResponse{}
	if v, ok := data["nextDateReset"].(float64); ok {
		r.NextDateReset = &v
	}
	if rawSub, ok := data["subscriptionInfo"].(map[string]interface{}); ok {
		sub := SubscriptionInfoFromDict(rawSub)
		r.SubscriptionInfoV = &sub
	}
	if rawList, ok := data["usageBreakdownList"].([]interface{}); ok {
		for _, item := range rawList {
			if m, ok := item.(map[string]interface{}); ok {
				r.UsageBreakdownList = append(r.UsageBreakdownList, UsageBreakdownFromDict(m))
			}
		}
	}
	if r.UsageBreakdownList == nil {
		r.UsageBreakdownList = []UsageBreakdown{}
	}
	return r
}

// SubscriptionTitle 获取订阅标题
func (r *UsageLimitsResponse) SubscriptionTitle() *string {
	if r.SubscriptionInfoV != nil {
		return r.SubscriptionInfoV.SubscriptionTitle
	}
	return nil
}

// primaryBreakdown 获取主要使用量明细（第一个）
func (r *UsageLimitsResponse) primaryBreakdown() *UsageBreakdown {
	if len(r.UsageBreakdownList) > 0 {
		return &r.UsageBreakdownList[0]
	}
	return nil
}

// UsageLimitTotal 计算总使用额度上限
func (r *UsageLimitsResponse) UsageLimitTotal() float64 {
	bd := r.primaryBreakdown()
	if bd == nil {
		return 0.0
	}
	total := bd.UsageLimitWithPrecision
	if bd.FreeTrialInfo != nil && bd.FreeTrialInfo.IsActive() {
		total += bd.FreeTrialInfo.UsageLimitWithPrecision
	}
	for i := range bd.Bonuses {
		if bd.Bonuses[i].IsActive() {
			total += bd.Bonuses[i].UsageLimit
		}
	}
	return total
}

// CurrentUsageTotal 计算当前总使用量
func (r *UsageLimitsResponse) CurrentUsageTotal() float64 {
	bd := r.primaryBreakdown()
	if bd == nil {
		return 0.0
	}
	total := bd.CurrentUsageWithPrecision
	if bd.FreeTrialInfo != nil && bd.FreeTrialInfo.IsActive() {
		total += bd.FreeTrialInfo.CurrentUsageWithPrecision
	}
	for i := range bd.Bonuses {
		if bd.Bonuses[i].IsActive() {
			total += bd.Bonuses[i].CurrentUsage
		}
	}
	return total
}
