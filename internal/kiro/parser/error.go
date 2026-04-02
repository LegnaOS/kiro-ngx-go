// Package parser 实现 AWS Event Stream 二进制协议解析
package parser

import "fmt"

// ParseError 解析错误接口
type ParseError interface {
	error
	isParseError()
}

// IncompleteError 数据不足错误
type IncompleteError struct {
	Needed    int
	Available int
}

func (e *IncompleteError) Error() string {
	return fmt.Sprintf("数据不足: 需要 %d 字节, 当前 %d 字节", e.Needed, e.Available)
}

func (e *IncompleteError) isParseError() {}

// PreludeCrcMismatch Prelude CRC 校验失败
type PreludeCrcMismatch struct {
	Expected uint32
	Actual   uint32
}

func (e *PreludeCrcMismatch) Error() string {
	return fmt.Sprintf("Prelude CRC 校验失败: 期望 0x%08x, 实际 0x%08x", e.Expected, e.Actual)
}

func (e *PreludeCrcMismatch) isParseError() {}

// MessageCrcMismatch Message CRC 校验失败
type MessageCrcMismatch struct {
	Expected uint32
	Actual   uint32
}

func (e *MessageCrcMismatch) Error() string {
	return fmt.Sprintf("Message CRC 校验失败: 期望 0x%08x, 实际 0x%08x", e.Expected, e.Actual)
}

func (e *MessageCrcMismatch) isParseError() {}

// InvalidHeaderType 无效的头部值类型
type InvalidHeaderType struct {
	TypeID int
}

func (e *InvalidHeaderType) Error() string {
	return fmt.Sprintf("无效的头部值类型: %d", e.TypeID)
}

func (e *InvalidHeaderType) isParseError() {}

// HeaderParseFailed 头部解析错误
type HeaderParseFailed struct {
	Msg string
}

func (e *HeaderParseFailed) Error() string {
	return fmt.Sprintf("头部解析错误: %s", e.Msg)
}

func (e *HeaderParseFailed) isParseError() {}

// MessageTooLarge 消息长度超限
type MessageTooLarge struct {
	Length  int
	MaxSize int
}

func (e *MessageTooLarge) Error() string {
	return fmt.Sprintf("消息长度超限: %d 字节 (最大 %d)", e.Length, e.MaxSize)
}

func (e *MessageTooLarge) isParseError() {}

// MessageTooSmall 消息长度过小
type MessageTooSmall struct {
	Length  int
	MinSize int
}

func (e *MessageTooSmall) Error() string {
	return fmt.Sprintf("消息长度过小: %d 字节 (最小 %d)", e.Length, e.MinSize)
}

func (e *MessageTooSmall) isParseError() {}

// InvalidMessageType 无效的消息类型
type InvalidMessageType struct {
	MsgType string
}

func (e *InvalidMessageType) Error() string {
	return fmt.Sprintf("无效的消息类型: %s", e.MsgType)
}

func (e *InvalidMessageType) isParseError() {}

// TooManyErrors 连续错误过多，解码器已停止
type TooManyErrors struct {
	Count     int
	LastError string
}

func (e *TooManyErrors) Error() string {
	return fmt.Sprintf("连续错误过多 (%d 次)，解码器已停止: %s", e.Count, e.LastError)
}

func (e *TooManyErrors) isParseError() {}

// BufferOverflow 缓冲区溢出
type BufferOverflow struct {
	Size    int
	MaxSize int
}

func (e *BufferOverflow) Error() string {
	return fmt.Sprintf("缓冲区溢出: %d 字节 (最大 %d)", e.Size, e.MaxSize)
}

func (e *BufferOverflow) isParseError() {}
