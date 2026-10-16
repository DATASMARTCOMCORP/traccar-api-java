package ingest

import (
	"fmt"
	"math"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// A Framer splits a TCP stream into messages: Netty's ByteToMessageDecoder. Frame returns
// the next message from in, consuming its bytes, or nil when more bytes are needed. It
// may consume bytes without returning a message (Netty calls it again). A Fault is where
// the Java frame decoder throws; the connection is then closed.
type Framer interface {
	Frame(m *Message, in *kit.Buf) any
}

// FramerFunc adapts a function (a BaseFrameDecoder's decode) to Framer.
type FramerFunc func(m *Message, in *kit.Buf) any

func (f FramerFunc) Frame(m *Message, in *kit.Buf) any { return f(m, in) }

// BaseFramer is BaseFrameDecoder: a decode function plus the maximum length (1024 by
// default) of a pending frame.
type BaseFramer struct {
	MaxLength int
	Decode    func(m *Message, in *kit.Buf) any
}

func (f *BaseFramer) Frame(m *Message, in *kit.Buf) any {
	if v := f.Decode(m, in); v != nil {
		return v
	}
	max := f.MaxLength
	if max == 0 {
		max = MaxFrameLength
	}
	if in.ReadableBytes() > max {
		kit.Throw("TooLongFrameException: Frame exceeds %d bytes", max)
	}
	return nil
}

// Frame length limits of BaseProtocol.
const (
	MaxFrameLength      = 1024
	MaxFrameLengthLarge = 32 * 1024
)

// LineFramer is Netty's LineBasedFrameDecoder: frames end with \n or \r\n.
type LineFramer struct {
	MaxLength  int
	KeepEnding bool // stripDelimiter false
	FailFast   bool

	discarding     bool
	discardedBytes int
	offset         int
}

// NewLineFramer is new LineBasedFrameDecoder(maxLength).
func NewLineFramer(maxLength int) *LineFramer { return &LineFramer{MaxLength: maxLength} }

func (f *LineFramer) findEndOfLine(b *kit.Buf) int {
	total := b.ReadableBytes()
	i := b.IndexOf(b.ReaderIndex()+f.offset, b.ReaderIndex()+total, '\n')
	if i >= 0 {
		f.offset = 0
		if i > 0 && b.GetByte(i-1) == '\r' {
			i--
		}
	} else {
		f.offset = total
	}
	return i
}

func (f *LineFramer) fail(length string) {
	kit.Throw("TooLongFrameException: frame length (%s) exceeds the allowed maximum (%d)", length, f.MaxLength)
}

func (f *LineFramer) Frame(_ *Message, b *kit.Buf) any {
	eol := f.findEndOfLine(b)
	delim := func() int {
		if b.GetByte(eol) == '\r' {
			return 2
		}
		return 1
	}
	if !f.discarding {
		if eol >= 0 {
			length := eol - b.ReaderIndex()
			d := delim()
			if length > f.MaxLength {
				b.SetReaderIndex(eol + d)
				f.fail(fmt.Sprint(length))
			}
			if !f.KeepEnding {
				frame := b.ReadSlice(length)
				b.Skip(d)
				return frame
			}
			return b.ReadSlice(length + d)
		}
		if length := b.ReadableBytes(); length > f.MaxLength {
			f.discardedBytes = length
			b.SetReaderIndex(b.WriterIndex())
			f.discarding = true
			f.offset = 0
			if f.FailFast {
				f.fail(fmt.Sprint("over ", f.discardedBytes))
			}
		}
		return nil
	}
	if eol >= 0 {
		length := f.discardedBytes + eol - b.ReaderIndex()
		b.SetReaderIndex(eol + delim())
		f.discardedBytes = 0
		f.discarding = false
		if !f.FailFast {
			f.fail(fmt.Sprint(length))
		}
	} else {
		f.discardedBytes += b.ReadableBytes()
		b.SetReaderIndex(b.WriterIndex())
		f.offset = 0
	}
	return nil
}

// DelimiterFramer is Traccar's CharacterDelimiterFrameDecoder (Netty's
// DelimiterBasedFrameDecoder as a subclass, so never line-based): the frame ends at the
// delimiter that gives the shortest frame.
type DelimiterFramer struct {
	MaxLength    int
	KeepEnding   bool // stripDelimiter false
	Delimiters   [][]byte
	discarding   bool
	tooLongBytes int
}

// NewDelimiterFramer is new CharacterDelimiterFrameDecoder(maxLength, delimiters...).
func NewDelimiterFramer(maxLength int, delimiters ...string) *DelimiterFramer {
	f := &DelimiterFramer{MaxLength: maxLength}
	for _, d := range delimiters {
		f.Delimiters = append(f.Delimiters, kit.Latin1Bytes(d))
	}
	return f
}

func (f *DelimiterFramer) fail(length int) {
	if length > 0 {
		kit.Throw("TooLongFrameException: frame length exceeds %d: %d - discarded", f.MaxLength, length)
	}
	kit.Throw("TooLongFrameException: frame length exceeds %d - discarding", f.MaxLength)
}

func (f *DelimiterFramer) Frame(_ *Message, b *kit.Buf) any {
	minLength, minDelim := math.MaxInt, -1
	for i, d := range f.Delimiters {
		idx := b.IndexOfBytes(d)
		if idx >= 0 && idx-b.ReaderIndex() < minLength {
			minLength, minDelim = idx-b.ReaderIndex(), i
		}
	}
	if minDelim >= 0 {
		delimLength := len(f.Delimiters[minDelim])
		if f.discarding {
			f.discarding = false
			b.Skip(minLength + delimLength)
			n := f.tooLongBytes
			f.tooLongBytes = 0
			_ = n // failFast is true for CharacterDelimiterFrameDecoder
			return nil
		}
		if minLength > f.MaxLength {
			b.Skip(minLength + delimLength)
			f.fail(minLength)
		}
		if !f.KeepEnding {
			frame := b.ReadSlice(minLength)
			b.Skip(delimLength)
			return frame
		}
		return b.ReadSlice(minLength + delimLength)
	}
	if !f.discarding {
		if b.ReadableBytes() > f.MaxLength {
			f.tooLongBytes = b.ReadableBytes()
			b.Skip(b.ReadableBytes())
			f.discarding = true
			f.fail(f.tooLongBytes)
		}
	} else {
		f.tooLongBytes += b.ReadableBytes()
		b.Skip(b.ReadableBytes())
	}
	return nil
}

// LengthFramer is Netty's LengthFieldBasedFrameDecoder.
type LengthFramer struct {
	MaxLength    int
	FieldOffset  int
	FieldLength  int // 1, 2, 3, 4 or 8
	Adjustment   int
	Strip        int  // initialBytesToStrip
	LittleEndian bool // ByteOrder.LITTLE_ENDIAN
	NoFailFast   bool // failFast false
	// FrameLength overrides getUnadjustedFrameLength.
	FrameLength func(b *kit.Buf, offset, length int) int64

	frameLength  int // -1: no frame in progress (0 is the initial value: set by Frame)
	started      bool
	discarding   bool
	toDiscard    int64
	tooLongBytes int64
}

// NewLengthFramer is new LengthFieldBasedFrameDecoder(max, offset, length, adjustment,
// strip).
func NewLengthFramer(maxLength, offset, length, adjustment, strip int) *LengthFramer {
	return &LengthFramer{MaxLength: maxLength, FieldOffset: offset, FieldLength: length, Adjustment: adjustment, Strip: strip}
}

func (f *LengthFramer) unadjusted(b *kit.Buf, offset int) int64 {
	if f.FrameLength != nil {
		return f.FrameLength(b, offset, f.FieldLength)
	}
	le := f.LittleEndian
	switch f.FieldLength {
	case 1:
		return int64(b.GetUnsignedByte(offset))
	case 2:
		if le {
			return int64(b.GetUnsignedShortLE(offset))
		}
		return int64(b.GetUnsignedShort(offset))
	case 3:
		if le {
			s := b.GetBytes(offset, 3)
			return int64(s[0]) | int64(s[1])<<8 | int64(s[2])<<16
		}
		return int64(b.GetUnsignedMedium(offset))
	case 4:
		if le {
			return b.GetUnsignedIntLE(offset)
		}
		return b.GetUnsignedInt(offset)
	case 8:
		if le {
			return kit.NewBuf(b.GetBytes(offset, 8)).ReadLongLE()
		}
		return b.GetLong(offset)
	}
	kit.Throw("DecoderException: unsupported lengthFieldLength: %d", f.FieldLength)
	return 0
}

func (f *LengthFramer) failTooLong(length int64) {
	if length > 0 {
		kit.Throw("TooLongFrameException: Adjusted frame length exceeds %d: %d - discarded", f.MaxLength, length)
	}
	kit.Throw("TooLongFrameException: Adjusted frame length exceeds %d - discarding", f.MaxLength)
}

func (f *LengthFramer) failIfNecessary(first bool) {
	if f.toDiscard == 0 {
		n := f.tooLongBytes
		f.tooLongBytes = 0
		f.discarding = false
		if f.NoFailFast || first {
			f.failTooLong(n)
		}
	} else if !f.NoFailFast && first {
		f.failTooLong(f.tooLongBytes)
	}
}

func (f *LengthFramer) Frame(_ *Message, in *kit.Buf) any {
	if !f.started {
		f.started, f.frameLength = true, -1
	}
	fieldEnd := f.FieldOffset + f.FieldLength
	if f.frameLength == -1 {
		if f.discarding {
			n := min(f.toDiscard, int64(in.ReadableBytes()))
			in.Skip(int(n))
			f.toDiscard -= n
			f.failIfNecessary(false)
		}
		if in.ReadableBytes() < fieldEnd {
			return nil
		}
		length := f.unadjusted(in, in.ReaderIndex()+f.FieldOffset)
		if length < 0 {
			in.Skip(fieldEnd)
			kit.Throw("CorruptedFrameException: negative pre-adjustment length field: %d", length)
		}
		length += int64(f.Adjustment + fieldEnd)
		if length < int64(fieldEnd) {
			in.Skip(fieldEnd)
			kit.Throw("CorruptedFrameException: Adjusted frame length (%d) is less than lengthFieldEndOffset: %d", length, fieldEnd)
		}
		if length > int64(f.MaxLength) {
			discard := length - int64(in.ReadableBytes())
			f.tooLongBytes = length
			if discard < 0 {
				in.Skip(int(length))
			} else {
				f.discarding = true
				f.toDiscard = discard
				in.Skip(in.ReadableBytes())
			}
			f.failIfNecessary(true)
			return nil
		}
		f.frameLength = int(length)
	}
	if in.ReadableBytes() < f.frameLength {
		return nil
	}
	if f.Strip > f.frameLength {
		in.Skip(f.frameLength)
		kit.Throw("CorruptedFrameException: Adjusted frame length (%d) is less than initialBytesToStrip: %d", f.frameLength, f.Strip)
	}
	in.Skip(f.Strip)
	n := f.frameLength - f.Strip
	frame := in.ReadSlice(n)
	f.frameLength = -1
	return frame
}

// FixedFramer is Netty's FixedLengthFrameDecoder.
type FixedFramer struct{ Length int }

func (f FixedFramer) Frame(_ *Message, in *kit.Buf) any {
	if in.ReadableBytes() < f.Length {
		return nil
	}
	return in.ReadSlice(f.Length)
}
