package parser

import (
	"encoding/binary"
)

// HeaderValueType 头部值类型枚举
type HeaderValueType int

const (
	BoolTrue  HeaderValueType = iota // 0 - 布尔真
	BoolFalse                        // 1 - 布尔假
	Byte                             // 2 - 单字节
	Short                            // 3 - 短整型
	Integer                          // 4 - 整型
	Long                             // 5 - 长整型
	ByteArray                        // 6 - 字节数组
	String                           // 7 - 字符串
	Timestamp                        // 8 - 时间戳
	UUID                             // 9 - UUID
)

// HeaderValue 头部值
type HeaderValue struct {
	Value interface{}
	Type  HeaderValueType
}

// AsStr 尝试将值转换为字符串指针，非字符串类型返回 nil
func (hv *HeaderValue) AsStr() *string {
	if hv.Type == String {
		s, ok := hv.Value.(string)
		if ok {
			return &s
		}
	}
	return nil
}

// Headers 消息头部集合
type Headers struct {
	inner map[string]HeaderValue
}

// NewHeaders 创建空的头部集合
func NewHeaders() *Headers {
	return &Headers{inner: make(map[string]HeaderValue)}
}

// Insert 插入头部键值对
func (h *Headers) Insert(name string, value HeaderValue) {
	h.inner[name] = value
}

// Get 获取头部值，不存在返回 nil
func (h *Headers) Get(name string) *HeaderValue {
	v, ok := h.inner[name]
	if !ok {
		return nil
	}
	return &v
}

// GetString 获取字符串类型的头部值
func (h *Headers) GetString(name string) *string {
	v := h.Get(name)
	if v == nil {
		return nil
	}
	return v.AsStr()
}

// MessageType 获取 :message-type 头部
func (h *Headers) MessageType() *string {
	return h.GetString(":message-type")
}

// EventType 获取 :event-type 头部
func (h *Headers) EventType() *string {
	return h.GetString(":event-type")
}

// ExceptionType 获取 :exception-type 头部
func (h *Headers) ExceptionType() *string {
	return h.GetString(":exception-type")
}

// ErrorCode 获取 :error-code 头部
func (h *Headers) ErrorCode() *string {
	return h.GetString(":error-code")
}

// ensureBytes 检查从 offset 开始是否有足够的字节
func ensureBytes(data []byte, offset, needed int) error {
	available := len(data) - offset
	if available < needed {
		return &IncompleteError{Needed: needed, Available: available}
	}
	return nil
}

// ParseHeaders 从字节流解析头部
func ParseHeaders(data []byte, headerLength int) (*Headers, error) {
	if len(data) < headerLength {
		return nil, &IncompleteError{Needed: headerLength, Available: len(data)}
	}

	headers := NewHeaders()
	offset := 0

	for offset < headerLength {
		// 名称长度 (1 byte)
		if offset >= len(data) {
			break
		}
		nameLen := int(data[offset])
		offset++

		if nameLen == 0 {
			return nil, &HeaderParseFailed{Msg: "头部名称长度不能为 0"}
		}

		// 名称
		if err := ensureBytes(data, offset, nameLen); err != nil {
			return nil, err
		}
		name := string(data[offset : offset+nameLen])
		offset += nameLen

		// 值类型 (1 byte)
		if err := ensureBytes(data, offset, 1); err != nil {
			return nil, err
		}
		typeByte := int(data[offset])
		offset++

		if typeByte > 9 {
			return nil, &InvalidHeaderType{TypeID: typeByte}
		}
		valueType := HeaderValueType(typeByte)

		// 根据类型解析值
		value, consumed, err := parseHeaderValue(data[offset:], valueType)
		if err != nil {
			return nil, err
		}
		offset += consumed
		headers.Insert(name, value)
	}

	return headers, nil
}

// parseHeaderValue 解析头部值，返回 (HeaderValue, 消耗字节数, 错误)
func parseHeaderValue(data []byte, valueType HeaderValueType) (HeaderValue, int, error) {
	switch valueType {
	case BoolTrue:
		return HeaderValue{Value: true, Type: valueType}, 0, nil

	case BoolFalse:
		return HeaderValue{Value: false, Type: valueType}, 0, nil

	case Byte:
		if err := ensureBytes(data, 0, 1); err != nil {
			return HeaderValue{}, 0, err
		}
		v := int8(data[0])
		return HeaderValue{Value: v, Type: valueType}, 1, nil

	case Short:
		if err := ensureBytes(data, 0, 2); err != nil {
			return HeaderValue{}, 0, err
		}
		v := int16(binary.BigEndian.Uint16(data[:2]))
		return HeaderValue{Value: v, Type: valueType}, 2, nil

	case Integer:
		if err := ensureBytes(data, 0, 4); err != nil {
			return HeaderValue{}, 0, err
		}
		v := int32(binary.BigEndian.Uint32(data[:4]))
		return HeaderValue{Value: v, Type: valueType}, 4, nil

	case Long, Timestamp:
		if err := ensureBytes(data, 0, 8); err != nil {
			return HeaderValue{}, 0, err
		}
		v := int64(binary.BigEndian.Uint64(data[:8]))
		return HeaderValue{Value: v, Type: valueType}, 8, nil

	case ByteArray:
		if err := ensureBytes(data, 0, 2); err != nil {
			return HeaderValue{}, 0, err
		}
		length := int(binary.BigEndian.Uint16(data[:2]))
		if err := ensureBytes(data, 0, 2+length); err != nil {
			return HeaderValue{}, 0, err
		}
		v := make([]byte, length)
		copy(v, data[2:2+length])
		return HeaderValue{Value: v, Type: valueType}, 2 + length, nil

	case String:
		if err := ensureBytes(data, 0, 2); err != nil {
			return HeaderValue{}, 0, err
		}
		length := int(binary.BigEndian.Uint16(data[:2]))
		if err := ensureBytes(data, 0, 2+length); err != nil {
			return HeaderValue{}, 0, err
		}
		v := string(data[2 : 2+length])
		return HeaderValue{Value: v, Type: valueType}, 2 + length, nil

	case UUID:
		if err := ensureBytes(data, 0, 16); err != nil {
			return HeaderValue{}, 0, err
		}
		v := make([]byte, 16)
		copy(v, data[:16])
		return HeaderValue{Value: v, Type: valueType}, 16, nil

	default:
		return HeaderValue{}, 0, &InvalidHeaderType{TypeID: int(valueType)}
	}
}
