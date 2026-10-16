package kit

import "fmt"

// CRC is a Checksum.Algorithm: a table-driven CRC with Java's arithmetic.
type CRC struct {
	bits          int
	poly, init    uint32
	refIn, refOut bool
	xorOut        uint32
	table         [256]uint32
}

func newCRC(bits int, poly, init uint32, refIn, refOut bool, xorOut uint32) *CRC {
	c := &CRC{bits: bits, poly: poly, init: init, refIn: refIn, refOut: refOut, xorOut: xorOut}
	for i := range c.table {
		switch bits {
		case 8:
			crc := uint32(i)
			for j := 0; j < 8; j++ {
				bit := crc&0x80 != 0
				crc <<= 1
				if bit {
					crc ^= poly
				}
			}
			c.table[i] = crc & 0xFF
		case 16:
			crc := uint32(i) << 8
			for j := 0; j < 8; j++ {
				bit := crc&0x8000 != 0
				crc <<= 1
				if bit {
					crc ^= poly
				}
			}
			c.table[i] = crc & 0xFFFF
		default:
			crc := uint32(i) << 24
			for j := 0; j < 8; j++ {
				if crc&0x80000000 != 0 {
					crc = crc<<1 ^ poly
				} else {
					crc <<= 1
				}
			}
			c.table[i] = crc
		}
	}
	return c
}

// The algorithms of Checksum.
var (
	CRC8_EGTS         = newCRC(8, 0x31, 0xFF, false, false, 0x00)
	CRC8_ROHC         = newCRC(8, 0x07, 0xFF, true, true, 0x00)
	CRC8_DALLAS       = newCRC(8, 0x31, 0x00, true, true, 0x00)
	CRC16_IBM         = newCRC(16, 0x8005, 0x0000, true, true, 0x0000)
	CRC16_X25         = newCRC(16, 0x1021, 0xFFFF, true, true, 0xFFFF)
	CRC16_MODBUS      = newCRC(16, 0x8005, 0xFFFF, true, true, 0x0000)
	CRC16_CCITT_FALSE = newCRC(16, 0x1021, 0xFFFF, false, false, 0x0000)
	CRC16_KERMIT      = newCRC(16, 0x1021, 0x0000, true, true, 0x0000)
	CRC16_XMODEM      = newCRC(16, 0x1021, 0x0000, false, false, 0x0000)
	CRC32_STANDARD    = newCRC(32, 0x04C11DB7, 0xFFFFFFFF, true, true, 0xFFFFFFFF)
	CRC32_MPEG2       = newCRC(32, 0x04C11DB7, 0xFFFFFFFF, false, false, 0x00000000)
)

func reverseBits(v uint32, bits int) uint32 {
	var r uint32
	for i := 0; i < bits; i++ {
		r = r<<1 | v&1
		v >>= 1
	}
	return r
}

// CRC8, CRC16 and CRC32 are Checksum.crc8/crc16/crc32 over the bytes.
func CRC8(a *CRC, data []byte) int {
	crc := a.init
	for _, v := range data {
		b := uint32(v)
		if a.refIn {
			b = reverseBits(b, 8)
		}
		crc = a.table[(crc&0xFF)^b]
	}
	if a.refOut {
		crc = reverseBits(crc, 8)
	}
	return int((crc ^ a.xorOut) & 0xFF)
}

func CRC16(a *CRC, data []byte) int {
	crc := a.init
	for _, v := range data {
		b := uint32(v)
		if a.refIn {
			b = reverseBits(b, 8)
		}
		crc = crc<<8 ^ a.table[((crc>>8)&0xFF)^b]
	}
	if a.refOut {
		crc = reverseBits(crc, 16)
	}
	return int((crc ^ a.xorOut) & 0xFFFF)
}

// CRC32 returns Java's int (signed).
func CRC32(a *CRC, data []byte) int32 {
	crc := a.init
	for _, v := range data {
		b := uint32(v)
		if a.refIn {
			b = reverseBits(b, 8)
		}
		crc = crc<<8 ^ a.table[((crc>>24)&0xFF)^b]
	}
	if a.refOut {
		crc = reverseBits(crc, 32)
	}
	return int32(crc ^ a.xorOut)
}

// XOR is Checksum.xor(ByteBuffer): over signed bytes, so the result can be negative.
func XOR(data []byte) int {
	c := 0
	for _, v := range data {
		c ^= int(int8(v))
	}
	return c
}

// XORString is Checksum.xor(String) over the ASCII bytes, as a signed byte.
func XORString(s string) int {
	var c int8
	for _, v := range ASCIIBytes(s) {
		c ^= int8(v)
	}
	return int(c)
}

// NMEA is Checksum.nmea: "*" and the XOR as two upper-case hex digits.
func NMEA(s string) string { return fmt.Sprintf("*%02X", uint8(XORString(s))) }

// Sum is Checksum.sum(ByteBuffer): the byte sum as a signed byte.
func Sum(data []byte) int {
	var c int8
	for _, v := range data {
		c += int8(v)
	}
	return int(c)
}

// Modulo256 is Checksum.modulo256.
func Modulo256(data []byte) int {
	c := 0
	for _, v := range data {
		c = (c + int(int8(v))) & 0xFF
	}
	return c
}

// SumString is Checksum.sum(String): the byte sum as two upper-case hex digits.
func SumString(s string) string {
	var c int8
	for _, v := range ASCIIBytes(s) {
		c += int8(v)
	}
	return fmt.Sprintf("%02X", uint8(c))
}

// Luhn is Checksum.luhn.
func Luhn(imei int64) int64 {
	var checksum int64
	remain := imei
	for i := 0; remain != 0; i++ {
		digit := remain % 10
		if i%2 == 0 {
			digit *= 2
			if digit >= 10 {
				digit = 1 + digit%10
			}
		}
		checksum += digit
		remain /= 10
	}
	return (10 - checksum%10) % 10
}

// IP is Checksum.ip, including Java's operator precedence in the fold.
func IP(data []byte) int {
	var sum int32
	for _, v := range data {
		sum += int32(v)
		if uint32(sum)&0x80000000 != 0 {
			sum = sum&0xffff + sum>>16
		}
	}
	for sum>>16 > 0 {
		sum = (sum&0xffff + sum) >> 16
	}
	if sum == 0xffff {
		return int(sum & 0xffff)
	}
	return int(^sum & 0xffff)
}
