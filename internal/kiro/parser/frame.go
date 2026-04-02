package parser

import (
	"encoding/binary"
	"encoding/json"
)

const (
	// PreludeSize Prelude 固定大小 (12 字节)
	PreludeSize = 12
	// MinMessageSize 最小消息大小 (Prelude + Message CRC)
	MinMessageSize = PreludeSize + 4
	// MaxMessageSize 最大消息大小限制 (16 MB)
	MaxMessageSize = 16 * 1024 * 1024
)

// Frame 解析后的消息帧
type Frame struct {
	Headers *Headers
	Payload []byte
}

// MessageType 获取消息类型
func (f *Frame) MessageType() *string {
	return f.Headers.MessageType()
}

// EventType 获取事件类型
func (f *Frame) EventType() *string {
	return f.Headers.EventType()
}

// PayloadAsJSON 将 payload 解析为 JSON
func (f *Frame) PayloadAsJSON() (interface{}, error) {
	var result interface{}
	if err := json.Unmarshal(f.Payload, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// PayloadAsStr 将 payload 转换为字符串
func (f *Frame) PayloadAsStr() string {
	return string(f.Payload)
}

// ParseFrame 尝试从缓冲区解析一个完整的帧
// 返回值:
//   - (nil, 0, nil) - 数据不足，等待更多数据
//   - (frame, consumed, nil) - 成功解析
//   - (nil, 0, error) - 解析错误
func ParseFrame(buffer []byte) (*Frame, int, error) {
	if len(buffer) < PreludeSize {
		return nil, 0, nil
	}

	// 读取 prelude
	totalLength := int(binary.BigEndian.Uint32(buffer[0:4]))
	headerLength := int(binary.BigEndian.Uint32(buffer[4:8]))
	preludeCRC := binary.BigEndian.Uint32(buffer[8:12])

	// 验证消息长度范围
	if totalLength < MinMessageSize {
		return nil, 0, &MessageTooSmall{Length: totalLength, MinSize: MinMessageSize}
	}
	if totalLength > MaxMessageSize {
		return nil, 0, &MessageTooLarge{Length: totalLength, MaxSize: MaxMessageSize}
	}

	// 检查是否有完整的消息
	if len(buffer) < totalLength {
		return nil, 0, nil
	}

	// 验证 Prelude CRC
	actualPreludeCRC := CRC32(buffer[:8])
	if actualPreludeCRC != preludeCRC {
		return nil, 0, &PreludeCrcMismatch{Expected: preludeCRC, Actual: actualPreludeCRC}
	}

	// 验证 Message CRC
	messageCRC := binary.BigEndian.Uint32(buffer[totalLength-4 : totalLength])
	actualMessageCRC := CRC32(buffer[:totalLength-4])
	if actualMessageCRC != messageCRC {
		return nil, 0, &MessageCrcMismatch{Expected: messageCRC, Actual: actualMessageCRC}
	}

	// 解析头部
	headersStart := PreludeSize
	headersEnd := headersStart + headerLength
	if headersEnd > totalLength-4 {
		return nil, 0, &HeaderParseFailed{Msg: "头部长度超出消息边界"}
	}

	headers, err := ParseHeaders(buffer[headersStart:headersEnd], headerLength)
	if err != nil {
		return nil, 0, err
	}

	// 提取 payload
	payload := make([]byte, totalLength-4-headersEnd)
	copy(payload, buffer[headersEnd:totalLength-4])

	return &Frame{Headers: headers, Payload: payload}, totalLength, nil
}
