// Package kit is the toolkit device decoders share: a byte buffer with Netty's ByteBuf
// semantics, Traccar's PatternBuilder and Parser for text protocols, DateBuilder, and the
// bit, BCD and checksum helpers. Decoders are ported from Java line by line around these,
// so each behaves as its Java original, including where the original fails.
//
// Failures Java signals with an exception (reading past the end of a buffer, a number
// that doesn't parse) panic with a *Fault here. A decoder runs under Recover, which turns
// the panic into an error: the same outcome as the Java decoder throwing, without
// threading an error through every read.
package kit

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"math"
	"strings"
	"unicode/utf8"
)

// Fault is a failure where the Java code throws.
type Fault struct{ Msg string }

func (f *Fault) Error() string { return f.Msg }

// Throw panics with a Fault, like a Java exception.
func Throw(format string, args ...any) {
	panic(&Fault{Msg: fmt.Sprintf(format, args...)})
}

// Recover turns a panic with an error into that error; call it deferred around a decode.
// Besides Faults, that covers the model's range checks and Go runtime errors: an index out
// of range is where the Java decoder throws ArrayIndexOutOfBoundsException.
func Recover(err *error) {
	if r := recover(); r != nil {
		e, ok := r.(error)
		if !ok {
			panic(r)
		}
		*err = e
	}
}

// Buf is a byte buffer with Netty's ByteBuf semantics: a reader index, a writer index,
// and reads that fail past the writer index. Slices share the bytes.
type Buf struct {
	b    []byte
	r, w int
	max  int // maxCapacity; 0 is unlimited
}

// NewBufMax is Unpooled.buffer(0, maxCapacity): writing past maxCapacity fails.
func NewBufMax(maxCapacity int) *Buf { return &Buf{max: maxCapacity} }

// NewBuf wraps bytes (Unpooled.wrappedBuffer): everything is readable.
func NewBuf(b []byte) *Buf { return &Buf{b: b, w: len(b)} }

// Hex parses hex text into a buffer (DataConverter.parseHex); invalid hex fails.
func Hex(s string) *Buf { return NewBuf(ParseHex(s)) }

// ParseHex is DataConverter.parseHex (commons-codec Hex.decodeHex): pairs of digits as
// Character.digit(c, 16) reads them; an odd count or another character fails.
func ParseHex(s string) []byte {
	if b, err := hex.DecodeString(s); err == nil {
		return b
	}
	chars := []rune(s)
	if len(chars)%2 != 0 {
		Throw("DecoderException: Odd number of characters.")
	}
	out := make([]byte, len(chars)/2)
	for i := range out {
		out[i] = byte(hexDigit(chars[2*i], 2*i)<<4 | hexDigit(chars[2*i+1], 2*i+1))
	}
	return out
}

func hexDigit(r rune, index int) int {
	if r > 0xFFFF { // two UTF-16 chars, neither a digit
		Throw("DecoderException: Illegal hexadecimal character at index %d", index)
	}
	v, err := model.ParseIntRadix(string(r), 16)
	if err != nil || v < 0 {
		Throw("DecoderException: Illegal hexadecimal character %c at index %d", r, index)
	}
	return int(v)
}

// Empty is Unpooled.buffer(): an empty buffer to write into.
func Empty() *Buf { return &Buf{} }

func (b *Buf) ReaderIndex() int          { return b.r }
func (b *Buf) WriterIndex() int          { return b.w }
func (b *Buf) ReadableBytes() int        { return b.w - b.r }
func (b *Buf) IsReadable() bool          { return b.w > b.r }
func (b *Buf) IsReadableN(n int) bool    { return b.w-b.r >= n }
func (b *Buf) Capacity() int             { return len(b.b) }
func (b *Buf) SetReaderIndex(i int)      { b.checkIndex(i, b.w); b.r = i }
func (b *Buf) SetWriterIndex(i int)      { b.checkIndex(b.r, i); b.w = i }
func (b *Buf) Skip(n int)                { b.check(n); b.r += n }
func (b *Buf) Bytes() []byte             { return b.b[b.r:b.w] } // the readable bytes
func (b *Buf) Array() []byte             { return b.b }
func (b *Buf) String() string            { return hex.EncodeToString(b.Bytes()) }
func (b *Buf) HexDump() string           { return hex.EncodeToString(b.Bytes()) }
func (b *Buf) MarkReaderIndex() int      { return b.r }
func (b *Buf) ResetReaderIndex(m int)    { b.r = m }
func (b *Buf) SetIndex(r, w int)         { b.checkIndex(r, w); b.r, b.w = r, w }
func (b *Buf) Copy() *Buf                { return NewBuf(append([]byte(nil), b.Bytes()...)) }
func (b *Buf) Duplicate() *Buf           { c := *b; return &c }
func (b *Buf) ReadableString() string    { return string(b.Bytes()) }
func (b *Buf) EqualsBytes(o []byte) bool { return string(b.Bytes()) == string(o) }

func (b *Buf) check(n int) {
	if n < 0 || b.r+n > b.w {
		Throw("readerIndex(%d) + length(%d) exceeds writerIndex(%d)", b.r, n, b.w)
	}
}

func (b *Buf) checkIndex(r, w int) {
	if r < 0 || r > w || w > len(b.b) {
		Throw("readerIndex: %d, writerIndex: %d (expected: 0 <= readerIndex <= writerIndex <= capacity(%d))", r, w, len(b.b))
	}
}

func (b *Buf) checkGet(i, n int) {
	if i < 0 || n < 0 || i+n > len(b.b) {
		Throw("index: %d, length: %d (expected: range(0, %d))", i, n, len(b.b))
	}
}

// Reads at the reader index.

func (b *Buf) ReadSignedByte() int8 { b.check(1); v := int8(b.b[b.r]); b.r++; return v }

func (b *Buf) ReadUnsignedByte() int { b.check(1); v := int(b.b[b.r]); b.r++; return v }

func (b *Buf) ReadBoolean() bool { return b.ReadUnsignedByte() != 0 }

func (b *Buf) next(n int) []byte { b.check(n); s := b.b[b.r : b.r+n]; b.r += n; return s }

func (b *Buf) ReadShort() int           { return int(int16(binary.BigEndian.Uint16(b.next(2)))) }
func (b *Buf) ReadShortLE() int         { return int(int16(binary.LittleEndian.Uint16(b.next(2)))) }
func (b *Buf) ReadUnsignedShort() int   { return int(binary.BigEndian.Uint16(b.next(2))) }
func (b *Buf) ReadUnsignedShortLE() int { return int(binary.LittleEndian.Uint16(b.next(2))) }

func (b *Buf) ReadMedium() int {
	v := b.ReadUnsignedMedium()
	if v&0x800000 != 0 {
		v |= ^0xFFFFFF
	}
	return v
}

func (b *Buf) ReadMediumLE() int {
	v := b.ReadUnsignedMediumLE()
	if v&0x800000 != 0 {
		v |= ^0xFFFFFF
	}
	return v
}

func (b *Buf) ReadUnsignedMedium() int {
	s := b.next(3)
	return int(s[0])<<16 | int(s[1])<<8 | int(s[2])
}

func (b *Buf) ReadUnsignedMediumLE() int {
	s := b.next(3)
	return int(s[2])<<16 | int(s[1])<<8 | int(s[0])
}

func (b *Buf) ReadInt() int32           { return int32(binary.BigEndian.Uint32(b.next(4))) }
func (b *Buf) ReadIntLE() int32         { return int32(binary.LittleEndian.Uint32(b.next(4))) }
func (b *Buf) ReadUnsignedInt() int64   { return int64(binary.BigEndian.Uint32(b.next(4))) }
func (b *Buf) ReadUnsignedIntLE() int64 { return int64(binary.LittleEndian.Uint32(b.next(4))) }
func (b *Buf) ReadLong() int64          { return int64(binary.BigEndian.Uint64(b.next(8))) }
func (b *Buf) ReadLongLE() int64        { return int64(binary.LittleEndian.Uint64(b.next(8))) }
func (b *Buf) ReadFloat() float32       { return math.Float32frombits(binary.BigEndian.Uint32(b.next(4))) }
func (b *Buf) ReadFloatLE() float32 {
	return math.Float32frombits(binary.LittleEndian.Uint32(b.next(4)))
}
func (b *Buf) ReadDouble() float64 { return math.Float64frombits(binary.BigEndian.Uint64(b.next(8))) }
func (b *Buf) ReadDoubleLE() float64 {
	return math.Float64frombits(binary.LittleEndian.Uint64(b.next(8)))
}

// ReadBytes copies n bytes out.
func (b *Buf) ReadBytes(n int) []byte { return append([]byte(nil), b.next(n)...) }

// ReadSlice is readSlice/readRetainedSlice: a buffer over the next n bytes.
func (b *Buf) ReadSlice(n int) *Buf {
	s := b.next(n)
	return NewBuf(s[:n:n])
}

// ReadString is readCharSequence(n, US_ASCII) (BufferUtil.readString). Netty decodes
// US_ASCII one char per byte, so bytes above 0x7F are Latin-1 characters.
func (b *Buf) ReadString(n int) string { return Latin1(b.next(n)) }

// ReadLatin1 is readCharSequence(n, ISO_8859_1): one char per byte.
func (b *Buf) ReadLatin1(n int) string { return Latin1(b.next(n)) }

// ReadUTF8 is readCharSequence(n, UTF_8).
func (b *Buf) ReadUTF8(n int) string { return UTF8(b.next(n)) }

// Gets at an absolute index (no change to the reader index).

func (b *Buf) at(i, n int) []byte { b.checkGet(i, n); return b.b[i : i+n] }

func (b *Buf) GetByte(i int) int8           { return int8(b.at(i, 1)[0]) }
func (b *Buf) GetUnsignedByte(i int) int    { return int(b.at(i, 1)[0]) }
func (b *Buf) GetShort(i int) int           { return int(int16(binary.BigEndian.Uint16(b.at(i, 2)))) }
func (b *Buf) GetShortLE(i int) int         { return int(int16(binary.LittleEndian.Uint16(b.at(i, 2)))) }
func (b *Buf) GetUnsignedShort(i int) int   { return int(binary.BigEndian.Uint16(b.at(i, 2))) }
func (b *Buf) GetUnsignedShortLE(i int) int { return int(binary.LittleEndian.Uint16(b.at(i, 2))) }
func (b *Buf) GetUnsignedMedium(i int) int {
	s := b.at(i, 3)
	return int(s[0])<<16 | int(s[1])<<8 | int(s[2])
}
func (b *Buf) GetInt(i int) int32           { return int32(binary.BigEndian.Uint32(b.at(i, 4))) }
func (b *Buf) GetIntLE(i int) int32         { return int32(binary.LittleEndian.Uint32(b.at(i, 4))) }
func (b *Buf) GetUnsignedInt(i int) int64   { return int64(binary.BigEndian.Uint32(b.at(i, 4))) }
func (b *Buf) GetUnsignedIntLE(i int) int64 { return int64(binary.LittleEndian.Uint32(b.at(i, 4))) }
func (b *Buf) GetLong(i int) int64          { return int64(binary.BigEndian.Uint64(b.at(i, 8))) }
func (b *Buf) GetBytes(i, n int) []byte     { return append([]byte(nil), b.at(i, n)...) }
func (b *Buf) GetString(i, n int) string    { return ASCII(b.at(i, n)) }
func (b *Buf) GetLatin1(i, n int) string    { return Latin1(b.at(i, n)) }

// Slice is slice(index, length): a buffer over those bytes.
func (b *Buf) Slice(i, n int) *Buf {
	s := b.at(i, n)
	return NewBuf(s[:n:n])
}

// IndexOf is indexOf(from, to, value): the first index in [from, to) holding value, or -1.
// A from greater than to searches backwards, as Netty does.
func (b *Buf) IndexOf(from, to int, value byte) int {
	if from <= to {
		from, to = max(from, 0), min(to, len(b.b))
		for i := from; i < to; i++ {
			if b.b[i] == value {
				return i
			}
		}
		return -1
	}
	from, to = min(from, len(b.b)), max(to, 0)
	for i := from - 1; i >= to; i-- {
		if b.b[i] == value {
			return i
		}
	}
	return -1
}

// IndexOfBytes is ByteBufUtil.indexOf(needle, haystack): the first index of needle in
// the readable bytes, or -1.
func (b *Buf) IndexOfBytes(needle []byte) int {
	i := strings.Index(string(b.Bytes()), string(needle))
	if i < 0 {
		return -1
	}
	return b.r + i
}

// IndexOfIn is BufferUtil.indexOf(needle, buf, start, end).
func (b *Buf) IndexOfIn(needle string, start, end int) int {
	if start < 0 || end > len(b.b) || start > end {
		Throw("indexOf: bad range %d..%d", start, end)
	}
	i := strings.Index(string(b.b[start:end]), needle)
	if i < 0 {
		return -1
	}
	return start + i
}

// BytesBefore is bytesBefore(value): the distance from the reader index to value, or -1.
func (b *Buf) BytesBefore(value byte) int {
	i := b.IndexOf(b.r, b.w, value)
	if i < 0 {
		return -1
	}
	return i - b.r
}

// BytesBeforeN is bytesBefore(length, value).
func (b *Buf) BytesBeforeN(n int, value byte) int {
	b.check(n)
	i := b.IndexOf(b.r, b.r+n, value)
	if i < 0 {
		return -1
	}
	return i - b.r
}

// Writes at the writer index (the buffer grows as needed).

func (b *Buf) grow(n int) []byte {
	need := b.w + n
	if b.max > 0 && need > b.max {
		Throw("IndexOutOfBoundsException: writerIndex(%d) + minWritableBytes(%d) exceeds maxCapacity(%d)", b.w, n, b.max)
	}
	if need > cap(b.b) {
		nb := make([]byte, need, max(2*cap(b.b), need, 16))
		copy(nb, b.b)
		b.b = nb
	}
	if need > len(b.b) {
		b.b = b.b[:need]
	}
	s := b.b[b.w:need]
	b.w = need
	return s
}

func (b *Buf) WriteByte8(v int) *Buf { b.grow(1)[0] = byte(v); return b }
func (b *Buf) WriteBoolean(v bool) *Buf {
	if v {
		return b.WriteByte8(1)
	}
	return b.WriteByte8(0)
}
func (b *Buf) WriteShort(v int) *Buf   { binary.BigEndian.PutUint16(b.grow(2), uint16(v)); return b }
func (b *Buf) WriteShortLE(v int) *Buf { binary.LittleEndian.PutUint16(b.grow(2), uint16(v)); return b }
func (b *Buf) WriteMedium(v int) *Buf {
	s := b.grow(3)
	s[0], s[1], s[2] = byte(v>>16), byte(v>>8), byte(v)
	return b
}
func (b *Buf) WriteMediumLE(v int) *Buf {
	s := b.grow(3)
	s[0], s[1], s[2] = byte(v), byte(v>>8), byte(v>>16)
	return b
}
func (b *Buf) WriteInt(v int64) *Buf   { binary.BigEndian.PutUint32(b.grow(4), uint32(v)); return b }
func (b *Buf) WriteIntLE(v int64) *Buf { binary.LittleEndian.PutUint32(b.grow(4), uint32(v)); return b }
func (b *Buf) WriteLong(v int64) *Buf  { binary.BigEndian.PutUint64(b.grow(8), uint64(v)); return b }
func (b *Buf) WriteLongLE(v int64) *Buf {
	binary.LittleEndian.PutUint64(b.grow(8), uint64(v))
	return b
}
func (b *Buf) WriteFloat(v float32) *Buf {
	binary.BigEndian.PutUint32(b.grow(4), math.Float32bits(v))
	return b
}
func (b *Buf) WriteDouble(v float64) *Buf {
	binary.BigEndian.PutUint64(b.grow(8), math.Float64bits(v))
	return b
}
func (b *Buf) WriteBytes(p []byte) *Buf { copy(b.grow(len(p)), p); return b }
func (b *Buf) WriteBuf(o *Buf) *Buf     { return b.WriteBytes(o.Bytes()) }
func (b *Buf) WriteZero(n int) *Buf     { clear(b.grow(n)); return b }

// WriteASCII is writeCharSequence(s, US_ASCII) (and ISO_8859_1): see NettyBytes.
func (b *Buf) WriteASCII(s string) *Buf { return b.WriteBytes(NettyBytes(s)) }

// SetByte and the other setters write at an absolute index.
func (b *Buf) SetByte(i, v int)  { b.at(i, 1)[0] = byte(v) }
func (b *Buf) SetShort(i, v int) { binary.BigEndian.PutUint16(b.at(i, 2), uint16(v)) }
func (b *Buf) SetShortLE(i, v int) {
	binary.LittleEndian.PutUint16(b.at(i, 2), uint16(v))
}
func (b *Buf) SetInt(i int, v int64) { binary.BigEndian.PutUint32(b.at(i, 4), uint32(v)) }

// Character sets: Java decodes bytes into UTF-16 strings; Go holds UTF-8.

// ASCII is the JDK's new String(bytes, US_ASCII): bytes above 0x7F become U+FFFD. Netty's
// ByteBuf.toString(US_ASCII) and readCharSequence(US_ASCII) are Latin1 instead.
func ASCII(p []byte) string {
	for _, c := range p {
		if c >= 0x80 {
			var sb strings.Builder
			for _, c := range p {
				if c < 0x80 {
					sb.WriteByte(c)
				} else {
					sb.WriteRune(utf8.RuneError)
				}
			}
			return sb.String()
		}
	}
	return string(p)
}

// Latin1 is new String(bytes, ISO_8859_1): one char per byte.
func Latin1(p []byte) string {
	for _, c := range p {
		if c >= 0x80 {
			var sb strings.Builder
			for _, c := range p {
				sb.WriteRune(rune(c))
			}
			return sb.String()
		}
	}
	return string(p)
}

// ASCIIBytes is String.getBytes(US_ASCII): a character above 0x7F (a surrogate pair is one)
// becomes '?'.
func ASCIIBytes(s string) []byte {
	out := make([]byte, 0, len(s))
	for _, r := range s {
		if r < 0x80 {
			out = append(out, byte(r))
		} else {
			out = append(out, '?')
		}
	}
	return out
}

// Latin1Bytes is String.getBytes(ISO_8859_1) (and Unpooled.copiedBuffer(s, ISO_8859_1)): a
// character above 0xFF (a surrogate pair is one) becomes '?'.
func Latin1Bytes(s string) []byte {
	out := make([]byte, 0, len(s))
	for _, r := range s {
		if r < 0x100 {
			out = append(out, byte(r))
		} else {
			out = append(out, '?')
		}
	}
	return out
}

// NettyBytes is how Netty writes a string as US_ASCII or ISO_8859_1
// (writeCharSequence, Unpooled.copiedBuffer(s, US_ASCII)): chars up to 0xFF as bytes,
// any other UTF-16 char (each half of a surrogate pair) as '?'.
func NettyBytes(s string) []byte {
	out := make([]byte, 0, len(s))
	for _, r := range s {
		switch {
		case r < 0x100:
			out = append(out, byte(r))
		case r > 0xFFFF:
			out = append(out, '?', '?')
		default:
			out = append(out, '?')
		}
	}
	return out
}
