package kit

import (
	"fmt"
	"strings"
)

// BitUtil, BcdUtil, BufferUtil and StringUtil. Shift counts wrap as in Java (mod 32 for
// ints, mod 64 for longs).

// Check is BitUtil.check: whether bit index is set.
func Check(number int64, index int) bool { return number&(1<<(uint(index)&63)) != 0 }

// Between is BitUtil.between for longs: bits [from, to).
func Between(number int64, from, to int) int64 {
	return (number >> (uint(from) & 63)) & (1<<(uint(to-from)&63) - 1)
}

// BetweenInt is BitUtil.between for ints (32-bit arithmetic, as in Java).
func BetweenInt(number int, from, to int) int {
	return int((int32(number) >> (uint(from) & 31)) & (int32(1)<<(uint(to-from)&31) - 1))
}

// From is BitUtil.from: the bits from index up (an arithmetic shift).
func From(number int64, from int) int64 { return number >> (uint(from) & 63) }

// FromInt is BitUtil.from for ints.
func FromInt(number, from int) int { return int(int32(number) >> (uint(from) & 31)) }

// To is BitUtil.to: the bits below index.
func To(number int64, to int) int64 { return Between(number, 0, to) }

// ToInt is BitUtil.to for ints.
func ToInt(number, to int) int { return BetweenInt(number, 0, to) }

// ReadBCD is BcdUtil.readInteger: digits BCD digits (an odd count peeks at the high
// nibble of the next byte without consuming it).
func ReadBCD(b *Buf, digits int) int {
	result := 0
	for i := 0; i < digits/2; i++ {
		v := b.ReadUnsignedByte()
		result = result*10 + v>>4
		result = result*10 + v&0x0f
	}
	if digits%2 != 0 {
		v := b.GetUnsignedByte(b.ReaderIndex())
		result = result*10 + v>>4
	}
	return result
}

// ReadBCDCoordinate is BcdUtil.readCoordinate.
func ReadBCDCoordinate(b *Buf) float64 {
	b1, b2, b3, b4 := b.ReadUnsignedByte(), b.ReadUnsignedByte(), b.ReadUnsignedByte(), b.ReadUnsignedByte()
	value := float64((b2&0xf)*10 + (b3 >> 4))
	value += float64(((b3&0xf)*10+(b4>>4))*10+(b4&0xf)) / 1000.0
	value /= 60
	value += float64(((b1>>4&0x7)*10+(b1&0xf))*10 + (b2 >> 4))
	if b1&0x80 != 0 {
		value = -value
	}
	return value
}

// ReadSignedMagnitudeInt is BufferUtil.readSignedMagnitudeInt.
func ReadSignedMagnitudeInt(b *Buf) int {
	v := b.ReadUnsignedInt()
	result := int(int32(To(v, 31)))
	if Check(v, 31) {
		return -result
	}
	return result
}

// IndexOfCount is BufferUtil.indexOf(buf, from, to, value, count): the count-th
// occurrence of value.
func IndexOfCount(b *Buf, from, to int, value byte, count int) int {
	start := from
	for i := 0; i < count; i++ {
		result := b.IndexOf(start, to, value)
		if result < 0 || i == count-1 {
			return result
		}
		start = result + 1
	}
	return -1
}

// IsPrintable is BufferUtil.isPrintable.
func IsPrintable(b *Buf, length int) bool {
	for i := 0; i < length; i++ {
		v := b.GetByte(b.ReaderIndex() + i)
		if v < 32 && v != '\r' && v != '\n' {
			return false
		}
	}
	return true
}

// ReadDecimalDigits is BufferUtil.readDecimalDigits.
func ReadDecimalDigits(b *Buf, count int) string {
	var sb strings.Builder
	for i := 0; i < count; i++ {
		v := b.ReadUnsignedByte()
		fmt.Fprintf(&sb, "%d%d", v/10, v%10)
	}
	return sb.String()
}

// ContainsHex is StringUtil.containsHex.
func ContainsHex(s string) bool { return strings.ContainsAny(s, "abcdefABCDEF") }

// StripLeading is StringUtil.stripLeading (keeps at least one character).
func StripLeading(c byte, s string) string {
	start := 0
	for start < len(s)-1 && s[start] == c {
		start++
	}
	return s[start:]
}

// StripTrailing is StringUtil.stripTrailing (keeps at least one character).
func StripTrailing(c byte, s string) string {
	end := len(s)
	for end > 1 && s[end-1] == c {
		end--
	}
	return s[:end]
}
