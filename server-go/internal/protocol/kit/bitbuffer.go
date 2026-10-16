package kit

// BitBuffer is Traccar's BitBuffer: bit fields read from (or 6-bit values written to) a
// byte buffer.
type BitBuffer struct {
	buf        *Buf
	writeByte  int
	writeCount int
	readByte   int
	readCount  int
}

// NewBitBuffer reads from buf, or writes to a new buffer when buf is nil.
func NewBitBuffer(buf *Buf) *BitBuffer {
	if buf == nil {
		buf = Empty()
	}
	return &BitBuffer{buf: buf}
}

// Buf is the underlying buffer.
func (b *BitBuffer) Buf() *Buf { return b.buf }

// WriteEncoded writes AIS-style 6-bit characters (b - 48, minus 8 more above 40).
func (b *BitBuffer) WriteEncoded(data []byte) {
	for _, c := range data {
		v := int8(c - 48)
		if v > 40 {
			v -= 8
		}
		b.Write(int(v))
	}
}

// Write appends a 6-bit value.
func (b *BitBuffer) Write(v int) {
	if b.writeCount == 0 {
		b.writeByte |= v
		b.writeCount = 6
		return
	}
	remaining := 8 - b.writeCount
	b.writeByte <<= remaining
	b.writeByte |= v >> (6 - remaining)
	b.buf.WriteByte8(b.writeByte)
	b.writeByte = v & (1<<(6-remaining) - 1)
	b.writeCount = 6 - remaining
}

// ReadUnsigned reads length bits.
func (b *BitBuffer) ReadUnsigned(length int) int {
	result := int32(0)
	for length > 0 {
		if b.readCount == 0 {
			b.readByte = b.buf.ReadUnsignedByte()
			b.readCount = 8
		}
		if b.readCount >= length {
			result <<= length
			result |= int32(b.readByte >> (b.readCount - length))
			b.readByte &= 1<<(b.readCount-length) - 1
			b.readCount -= length
			length = 0
		} else {
			result <<= b.readCount
			result |= int32(b.readByte)
			length -= b.readCount
			b.readByte = 0
			b.readCount = 0
		}
	}
	return int(result)
}

// ReadSigned reads length bits as a two's complement value.
func (b *BitBuffer) ReadSigned(length int) int {
	result := int32(b.ReadUnsigned(length))
	signBit := int32(1) << (uint(length-1) & 31)
	if result&signBit == 0 {
		return int(result)
	}
	result &= signBit - 1
	result += -signBit
	return int(result)
}
