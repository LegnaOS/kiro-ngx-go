package model

// Token 刷新类型 - 参考 src/kiro/model/token_refresh.rs

// RefreshRequest Social 认证刷新请求
type RefreshRequest struct {
	RefreshToken string `json:"refreshToken"`
}

// RefreshResponse Social 认证刷新响应
type RefreshResponse struct {
	AccessToken  *string `json:"accessToken"`
	RefreshToken *string `json:"refreshToken,omitempty"`
	ProfileArn   *string `json:"profileArn,omitempty"`
	ExpiresIn    *int    `json:"expiresIn,omitempty"`
}

// IdcRefreshRequest IdC Token 刷新请求 (AWS SSO OIDC)
type IdcRefreshRequest struct {
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
	RefreshToken string `json:"refreshToken"`
	GrantType    string `json:"grantType"`
}

// NewIdcRefreshRequest 创建 IdC 刷新请求，默认 grantType 为 "refresh_token"
func NewIdcRefreshRequest(clientID, clientSecret, refreshToken string) IdcRefreshRequest {
	return IdcRefreshRequest{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RefreshToken: refreshToken,
		GrantType:    "refresh_token",
	}
}

// IdcRefreshResponse IdC Token 刷新响应 (AWS SSO OIDC)
type IdcRefreshResponse struct {
	AccessToken  *string `json:"accessToken,omitempty"`
	RefreshToken *string `json:"refreshToken,omitempty"`
	ExpiresIn    *int    `json:"expiresIn,omitempty"`
}
