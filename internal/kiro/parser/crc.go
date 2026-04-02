package parser

import "hash/crc32"

// CRC32 计算 CRC32 校验和 (IEEE 多项式)
func CRC32(data []byte) uint32 {
	return crc32.ChecksumIEEE(data)
}
