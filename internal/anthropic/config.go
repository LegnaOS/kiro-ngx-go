// Package anthropic Anthropic API 配置
package anthropic

// RequestLimits 请求限制配置
type RequestLimits struct {
	MaxBytes          int
	MaxChars          int
	ContextTokenLimit int
}

// StreamLimits 流限制配置
type StreamLimits struct {
	PingIntervalSecs  int
	MaxIdlePings      int
	WarnAfterPings    int
}

// ConverterLimits 转换器限制配置
type ConverterLimits struct {
	CurrentToolResultMaxChars int
	CurrentToolResultMaxLines int
	HistoryToolResultMaxChars int
	HistoryToolResultMaxLines int
}

// 全局配置变量
var (
	globalRequestLimits   = RequestLimits{}
	globalStreamLimits    = StreamLimits{}
	globalConverterLimits = ConverterLimits{}
)

// ConfigureRequestLimits 配置请求限制
func ConfigureRequestLimits(maxBytes, maxChars, contextTokenLimit int) {
	globalRequestLimits = RequestLimits{
		MaxBytes:          maxBytes,
		MaxChars:          maxChars,
		ContextTokenLimit: contextTokenLimit,
	}
}

// ConfigureStreamLimits 配置流限制
func ConfigureStreamLimits(pingIntervalSecs, maxIdlePings, warnAfterPings int) {
	globalStreamLimits = StreamLimits{
		PingIntervalSecs: pingIntervalSecs,
		MaxIdlePings:     maxIdlePings,
		WarnAfterPings:   warnAfterPings,
	}
}

// ConfigureConverterLimits 配置转换器限制
func ConfigureConverterLimits(currentMaxChars, currentMaxLines, historyMaxChars, historyMaxLines int) {
	globalConverterLimits = ConverterLimits{
		CurrentToolResultMaxChars: currentMaxChars,
		CurrentToolResultMaxLines: currentMaxLines,
		HistoryToolResultMaxChars: historyMaxChars,
		HistoryToolResultMaxLines: historyMaxLines,
	}
}

// GetRequestLimits 获取请求限制配置
func GetRequestLimits() RequestLimits {
	return globalRequestLimits
}

// GetStreamLimits 获取流限制配置
func GetStreamLimits() StreamLimits {
	return globalStreamLimits
}

// GetConverterLimits 获取转换器限制配置
func GetConverterLimits() ConverterLimits {
	return globalConverterLimits
}
