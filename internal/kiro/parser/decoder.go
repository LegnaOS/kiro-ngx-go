package parser

import (
	"encoding/binary"

	"kiro-proxy/internal/logger"
)

const (
	// DefaultMaxBufferSize 默认最大缓冲区大小 (16 MB)
	DefaultMaxBufferSize = 16 * 1024 * 1024
	// DefaultMaxErrors 默认最大连续错误数
	DefaultMaxErrors = 5
)

// DecoderState 解码器状态
type DecoderState int

const (
	Ready      DecoderState = iota // 就绪
	Parsing                        // 解析中
	Recovering                     // 恢复中
	Stopped                        // 已停止
)

// EventStreamDecoder 流式事件解码器
type EventStreamDecoder struct {
	buffer        []byte
	state         DecoderState
	framesDecoded int
	errorCount    int
	maxErrors     int
	maxBufferSize int
	bytesSkipped  int
}

// NewEventStreamDecoder 创建新的流式事件解码器
func NewEventStreamDecoder(maxErrors, maxBufferSize int) *EventStreamDecoder {
	if maxErrors <= 0 {
		maxErrors = DefaultMaxErrors
	}
	if maxBufferSize <= 0 {
		maxBufferSize = DefaultMaxBufferSize
	}
	return &EventStreamDecoder{
		buffer:        make([]byte, 0, 8192),
		state:         Ready,
		maxErrors:     maxErrors,
		maxBufferSize: maxBufferSize,
	}
}

// Feed 向解码器提供数据
func (d *EventStreamDecoder) Feed(data []byte) error {
	newSize := len(d.buffer) + len(data)
	if newSize > d.maxBufferSize {
		return &BufferOverflow{Size: newSize, MaxSize: d.maxBufferSize}
	}

	d.buffer = append(d.buffer, data...)
	if d.state == Recovering {
		d.state = Ready
	}
	return nil
}

// Decode 尝试解码下一个帧
// 返回 (nil, nil) 表示数据不足
// 返回 (frame, nil) 表示成功
// 返回 (nil, error) 表示解析错误
func (d *EventStreamDecoder) Decode() (*Frame, error) {
	if d.state == Stopped {
		return nil, &TooManyErrors{Count: d.errorCount, LastError: "解码器已停止"}
	}

	if len(d.buffer) == 0 {
		d.state = Ready
		return nil, nil
	}

	d.state = Parsing

	frame, consumed, err := ParseFrame(d.buffer)
	if err != nil {
		// 解析出错
		parseErr, ok := err.(ParseError)
		if !ok {
			// 非 ParseError 类型，包装一下
			parseErr = &HeaderParseFailed{Msg: err.Error()}
		}

		d.errorCount++
		errorMsg := err.Error()

		if d.errorCount >= d.maxErrors {
			d.state = Stopped
			preview := d.buffer
			if len(preview) > 256 {
				preview = preview[:256]
			}
			logger.Errorf("解码器停止: 连续 %d 次错误，最后错误: %s\n缓冲区前 %d 字节: %q", d.errorCount, errorMsg, len(preview), preview)
			return nil, &TooManyErrors{Count: d.errorCount, LastError: errorMsg}
		}

		d.tryRecover(parseErr)
		d.state = Recovering
		return nil, err
	}

	if frame == nil {
		// 数据不足
		d.state = Ready
		return nil, nil
	}

	// 成功解码
	d.buffer = d.buffer[consumed:]
	d.state = Ready
	d.framesDecoded++
	d.errorCount = 0
	return frame, nil
}

// DecodeAll 解码所有可用帧
func (d *EventStreamDecoder) DecodeAll() []*Frame {
	var frames []*Frame
	for {
		if d.state == Stopped || d.state == Recovering {
			break
		}
		frame, err := d.Decode()
		if err != nil {
			break
		}
		if frame == nil {
			break
		}
		frames = append(frames, frame)
	}
	return frames
}

// tryRecover 尝试容错恢复
func (d *EventStreamDecoder) tryRecover(err ParseError) {
	if len(d.buffer) == 0 {
		return
	}

	// Prelude 阶段错误：逐字节跳过
	switch err.(type) {
	case *PreludeCrcMismatch, *MessageTooSmall, *MessageTooLarge:
		skipped := d.buffer[0]
		d.buffer = d.buffer[1:]
		d.bytesSkipped++
		logger.Warnf("Prelude 错误恢复: 跳过字节 0x%02x (累计跳过 %d 字节)", skipped, d.bytesSkipped)
		return
	}

	// Data 阶段错误：尝试跳过整帧
	switch err.(type) {
	case *MessageCrcMismatch, *HeaderParseFailed:
		if len(d.buffer) >= PreludeSize {
			totalLength := int(binary.BigEndian.Uint32(d.buffer[0:4]))
			if totalLength >= MinMessageSize && totalLength <= len(d.buffer) {
				logger.Warnf("Data 错误恢复: 跳过损坏帧 (%d 字节)", totalLength)
				d.buffer = d.buffer[totalLength:]
				d.bytesSkipped += totalLength
				return
			}
		}
	}

	// 回退到逐字节跳过
	skipped := d.buffer[0]
	d.buffer = d.buffer[1:]
	d.bytesSkipped++
	logger.Warnf("通用错误恢复: 跳过字节 0x%02x (累计跳过 %d 字节)", skipped, d.bytesSkipped)
}

// Reset 重置解码器到初始状态
func (d *EventStreamDecoder) Reset() {
	d.buffer = d.buffer[:0]
	d.state = Ready
	d.framesDecoded = 0
	d.errorCount = 0
	d.bytesSkipped = 0
}

// State 获取当前状态
func (d *EventStreamDecoder) State() DecoderState {
	return d.state
}

// IsReady 是否就绪
func (d *EventStreamDecoder) IsReady() bool {
	return d.state == Ready
}

// IsStopped 是否已停止
func (d *EventStreamDecoder) IsStopped() bool {
	return d.state == Stopped
}

// IsRecovering 是否恢复中
func (d *EventStreamDecoder) IsRecovering() bool {
	return d.state == Recovering
}

// FramesDecoded 已解码帧数
func (d *EventStreamDecoder) FramesDecoded() int {
	return d.framesDecoded
}

// ErrorCount 连续错误计数
func (d *EventStreamDecoder) ErrorCount() int {
	return d.errorCount
}

// BytesSkipped 累计跳过字节数
func (d *EventStreamDecoder) BytesSkipped() int {
	return d.bytesSkipped
}

// BufferLen 当前缓冲区长度
func (d *EventStreamDecoder) BufferLen() int {
	return len(d.buffer)
}

// TryResume 尝试从 Stopped 状态恢复
func (d *EventStreamDecoder) TryResume() {
	if d.state == Stopped {
		d.errorCount = 0
		d.state = Ready
		logger.Infof("解码器从 Stopped 状态恢复")
	}
}
