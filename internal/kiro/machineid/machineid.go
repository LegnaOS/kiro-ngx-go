// Package machineid Machine ID 生成 - 参考 src/kiro/machine_id.rs
package machineid

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// sha256Hex 返回输入字符串的 SHA-256 十六进制摘要
func sha256Hex(input string) string {
	h := sha256.Sum256([]byte(input))
	return hex.EncodeToString(h[:])
}

// isHexString 检查字符串是否全部由十六进制字符组成
func isHexString(s string) bool {
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

// normalizeMachineID 标准化 machineId 格式
//
// 支持：
//   - 64 字符十六进制字符串（直接返回）
//   - UUID 格式（移除连字符后补齐到 64 字符）
//
// 无法识别的格式返回空字符串
func normalizeMachineID(machineID string) string {
	trimmed := strings.TrimSpace(machineID)

	// 64 字符十六进制
	if len(trimmed) == 64 && isHexString(trimmed) {
		return trimmed
	}

	// UUID 格式：移除连字符后应为 32 字符十六进制
	withoutDashes := strings.ReplaceAll(trimmed, "-", "")
	if len(withoutDashes) == 32 && isHexString(withoutDashes) {
		return withoutDashes + withoutDashes
	}

	return ""
}

// ConfigProvider 配置提供者接口，用于获取 machineId
type ConfigProvider interface {
	GetMachineID() *string
}

// CredentialProvider 凭据提供者接口
type CredentialProvider interface {
	GetMachineID() *string
	GetRefreshToken() *string
}

// GenerateFromCredentials 根据凭证信息生成唯一的 Machine ID
//
// 优先级: 凭据级 machineId > config.machineId > refreshToken 生成
func GenerateFromCredentials(cred CredentialProvider, cfg ConfigProvider) string {
	// 凭据级 machineId
	if mid := cred.GetMachineID(); mid != nil && *mid != "" {
		if normalized := normalizeMachineID(*mid); normalized != "" {
			return normalized
		}
	}

	// 全局 machineId
	if mid := cfg.GetMachineID(); mid != nil && *mid != "" {
		if normalized := normalizeMachineID(*mid); normalized != "" {
			return normalized
		}
	}

	// 使用 refreshToken 生成
	if rt := cred.GetRefreshToken(); rt != nil && *rt != "" {
		return sha256Hex("KotlinNativeAPI/" + *rt)
	}

	return ""
}
