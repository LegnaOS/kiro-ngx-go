// Package admin Admin API 错误类型 - 参考 admin/error.py
package admin

import "fmt"

// AdminServiceError Admin 服务错误，包含 HTTP 状态码和结构化响应
type AdminServiceError struct {
	StatusCode int
	Response   AdminErrorResponse
	Err        error
}

// Error 实现 error 接口
func (e *AdminServiceError) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}
	return e.Response.Error.Message
}

// Unwrap 支持 errors.Unwrap
func (e *AdminServiceError) Unwrap() error {
	return e.Err
}

// NewNotFoundError 凭据不存在错误
func NewNotFoundError(id int) *AdminServiceError {
	msg := fmt.Sprintf("凭据不存在: %d", id)
	return &AdminServiceError{
		StatusCode: 404,
		Response:   NotFoundErrorResponse(msg),
		Err:        fmt.Errorf("%s", msg),
	}
}

// NewUpstreamError 上游服务错误
func NewUpstreamError(message string) *AdminServiceError {
	msg := fmt.Sprintf("上游服务错误: %s", message)
	return &AdminServiceError{
		StatusCode: 502,
		Response:   ApiError(msg),
		Err:        fmt.Errorf("%s", msg),
	}
}

// NewInternalError 内部错误
func NewInternalError(message string) *AdminServiceError {
	msg := fmt.Sprintf("内部错误: %s", message)
	return &AdminServiceError{
		StatusCode: 500,
		Response:   NewAdminError("internal_error", msg),
		Err:        fmt.Errorf("%s", msg),
	}
}

// NewInvalidCredentialError 凭据无效错误
func NewInvalidCredentialError(message string) *AdminServiceError {
	msg := fmt.Sprintf("凭据无效: %s", message)
	return &AdminServiceError{
		StatusCode: 400,
		Response:   InvalidRequestError(msg),
		Err:        fmt.Errorf("%s", msg),
	}
}
