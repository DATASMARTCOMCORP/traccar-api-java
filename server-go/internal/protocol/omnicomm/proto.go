package omnicomm

import "github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"

// A minimal reader of the protobuf wire format for OmnicommMessage.proto (proto2, with
// groups). It parses as protobuf-java's generated parser does: every known field is read
// with its declared type (malformed content throws InvalidProtocolBufferException), a
// field with an unexpected wire type is kept as unknown, groups occurring twice merge, and
// a missing required field throws.

type fieldKind int

const (
	varint    fieldKind = iota + 1 // [u|s]int32/64, single
	varints                        // repeated varint: plain or packed
	bytesKind                      // bytes, string
	float32Kind
	floats // repeated float: plain or packed
	groupKind
)

type field struct {
	kind  fieldKind
	group *schema
}

type schema struct {
	fields   map[int]field
	required []int
}

func scalars(kind fieldKind, numbers ...int) map[int]field {
	m := map[int]field{}
	for _, n := range numbers {
		m[n] = field{kind: kind}
	}
	return m
}

func merge(maps ...map[int]field) map[int]field {
	out := map[int]field{}
	for _, m := range maps {
		for k, v := range m {
			out[k] = v
		}
	}
	return out
}

func rangeOf(from, to int) []int {
	var r []int
	for i := from; i <= to; i++ {
		r = append(r, i)
	}
	return r
}

var (
	generalSchema = &schema{fields: merge(
		scalars(varint, 1, 2, 4, 5, 6, 7, 8, 9, 10, 12, 13, 14, 16, 17, 18, 19),
		scalars(bytesKind, 3, 11, 15))}
	navSchema   = &schema{fields: scalars(varint, rangeOf(1, 7)...), required: rangeOf(1, 6)}
	llsdtSchema = &schema{fields: merge(scalars(varint, rangeOf(1, 24)...), scalars(varints, 25))}

	messageSchema = &schema{fields: map[int]field{
		1: {kind: varints},
		2: {kind: groupKind, group: generalSchema},
		4: {kind: groupKind, group: &schema{fields: merge(scalars(varint, 1, 2, 3, 5, 6), scalars(bytesKind, 4))}},
		5: {kind: groupKind, group: navSchema},
		6: {kind: groupKind, group: &schema{fields: scalars(varint, rangeOf(1, 6)...)}},
		7: {kind: groupKind, group: &schema{fields: merge(
			scalars(varint, 70, 91, 96, 100, 110, 174, 175, 182, 184, 190, 244, 245, 247, 250,
				521, 522, 527, 597, 598, 914, 916, 1624, 1821, 1856),
			scalars(varints, 582, 928))}},
		8: {kind: groupKind, group: llsdtSchema},
		9: {kind: groupKind, group: &schema{fields: map[int]field{
			1: {kind: groupKind, group: &schema{fields: scalars(varint, rangeOf(1, 8)...)}},
			2: {kind: groupKind, group: &schema{fields: merge(scalars(varint, 1, 2, 3, 4), scalars(bytesKind, 5, 6))}},
			3: {kind: groupKind, group: &schema{fields: merge(scalars(varints, 1, 2, 14), scalars(varint, 10, 11, 12, 13, 15))}},
			4: {kind: groupKind, group: &schema{fields: merge(scalars(varint, 1, 4), scalars(bytesKind, 2, 3))}},
			5: {kind: groupKind, group: &schema{fields: scalars(varint, rangeOf(1, 12)...)}},
			6: {kind: groupKind, group: &schema{fields: merge(scalars(varint, 1), scalars(bytesKind, 2, 3, 4))}},
			7: {kind: groupKind, group: &schema{fields: merge(scalars(varint, 1, 2, 3), scalars(floats, 4))}},
			8: {kind: groupKind, group: &schema{fields: merge(
				scalars(varint, 1, 2, 4, 11, 12, 14, 15, 17, 24, 25, 27, 28, 30, 37, 38, 41,
					46, 47, 48, 49, 50, 51, 52, 53, 54, 56, 57, 60, 61, 62, 63, 64),
				scalars(float32Kind, 3, 5, 6, 7, 8, 9, 10, 16, 18, 19, 20, 21, 22, 23, 29,
					31, 32, 33, 34, 35, 36, 40, 42, 43, 44, 45, 55),
				scalars(bytesKind, 13, 26, 39))}},
			9: {kind: groupKind, group: &schema{fields: merge(
				scalars(float32Kind, 1, 2, 4, 6, 7, 9, 10),
				scalars(varint, 3, 5, 8),
				scalars(varints, 11))}},
			10: {kind: groupKind, group: &schema{fields: merge(scalars(floats, 1, 2), scalars(float32Kind, 3, 4))}},
		}}},
		10: {kind: groupKind, group: &schema{fields: merge(scalars(bytesKind, 1, 7), scalars(varint, 2, 3, 4, 5, 6, 8))}},
		11: {kind: groupKind, group: &schema{fields: merge(scalars(varint, rangeOf(1, 10)...), scalars(bytesKind, 11), scalars(varint, 12))}},
		16: {kind: groupKind, group: &schema{fields: merge(scalars(bytesKind, 1), scalars(varint, 2), scalars(varints, 3, 4, 5, 6, 7))}},
	}}
)

// message is a parsed (sub)message: the last value of each scalar varint field and the
// merged groups.
type message struct {
	values map[int]uint64
	groups map[int]*message
}

func newMessage() *message {
	return &message{values: map[int]uint64{}, groups: map[int]*message{}}
}

// int32 is a uint32 field as Java's int getter returns it.
func (m *message) int32(n int) int { return int(int32(uint32(m.values[n]))) }

// sint32 is a sint32 field (zigzag).
func (m *message) sint32(n int) int {
	v := uint32(m.values[n])
	return int(int32(v>>1) ^ -int32(v&1))
}

type protoReader struct {
	b   []byte
	pos int
	end int // the current limit
}

func invalid(msg string) { kit.Throw("InvalidProtocolBufferException: %s", msg) }

func (r *protoReader) varint() uint64 {
	var v uint64
	for i := 0; i < 10; i++ {
		if r.pos >= r.end {
			invalid("While parsing a protocol message, the input ended unexpectedly in the middle of a field.")
		}
		c := r.b[r.pos]
		r.pos++
		v |= uint64(c&0x7F) << (7 * uint(i))
		if c < 0x80 {
			return v
		}
	}
	invalid("CodedInputStream encountered a malformed varint.")
	return 0
}

func (r *protoReader) skip(n int) {
	if n < 0 {
		invalid("CodedInputStream encountered an embedded string or message which claimed to have negative size.")
	}
	if n > r.end-r.pos {
		invalid("While parsing a protocol message, the input ended unexpectedly in the middle of a field.")
	}
	r.pos += n
}

// length reads a length prefix (readRawVarint32).
func (r *protoReader) length() int { return int(int32(uint32(r.varint()))) }

// readTag is CodedInputStream.readTag: 0 at the end.
func (r *protoReader) readTag() uint32 {
	if r.pos >= r.end {
		return 0
	}
	tag := uint32(r.varint())
	if tag>>3 == 0 {
		invalid("Protocol message contained an invalid tag (zero).")
	}
	return tag
}

// skipField is CodedInputStream.skipField: false for an end-group tag.
func (r *protoReader) skipField(tag uint32) bool {
	switch tag & 7 {
	case 0:
		r.varint()
	case 1:
		r.skip(8)
	case 2:
		r.skip(r.length())
	case 3:
		for {
			t := r.readTag()
			if t == 0 || !r.skipField(t) {
				r.checkLastTag(t, tag>>3<<3|4)
				break
			}
		}
	case 4:
		return false
	case 5:
		r.skip(4)
	default:
		invalid("Protocol message tag had invalid wire type.")
	}
	return true
}

func (r *protoReader) checkLastTag(got, want uint32) {
	if got != want {
		invalid("Protocol message end-group tag did not match expected tag.")
	}
}

// packed reads a packed field's content with read for each element.
func (r *protoReader) packed(read func()) {
	n := r.length()
	if n < 0 {
		invalid("CodedInputStream encountered an embedded string or message which claimed to have negative size.")
	}
	if n > r.end-r.pos {
		invalid("While parsing a protocol message, the input ended unexpectedly in the middle of a field.")
	}
	outer := r.end
	r.end = r.pos + n
	for r.pos < r.end {
		read()
	}
	r.end = outer
}

// parse reads fields into m until the end or an end-group tag, which it returns.
func (r *protoReader) parse(s *schema, m *message) uint32 {
	for {
		tag := r.readTag()
		if tag == 0 {
			return 0
		}
		number, wire := int(tag>>3), tag&7
		f, known := s.fields[number]
		switch {
		case known && f.kind == varint && wire == 0:
			m.values[number] = r.varint()
		case known && f.kind == varints && wire == 0:
			r.varint()
		case known && f.kind == varints && wire == 2:
			r.packed(func() { r.varint() })
		case known && f.kind == bytesKind && wire == 2:
			r.skip(r.length())
		case known && f.kind == float32Kind && wire == 5, known && f.kind == floats && wire == 5:
			r.skip(4)
		case known && f.kind == floats && wire == 2:
			r.packed(func() { r.skip(4) })
		case known && f.kind == groupKind && wire == 3:
			g := m.groups[number]
			if g == nil {
				g = newMessage()
				m.groups[number] = g
			}
			r.checkLastTag(r.parse(f.group, g), uint32(number)<<3|4)
		default:
			if !r.skipField(tag) {
				return tag
			}
		}
	}
}

// checkRequired is isInitialized: every required field of the present groups is set.
func checkRequired(s *schema, m *message) {
	for _, n := range s.required {
		if _, ok := m.values[n]; !ok {
			kit.Throw("InvalidProtocolBufferException: Message missing required fields")
		}
	}
	for n, g := range m.groups {
		checkRequired(s.fields[n].group, g)
	}
}

// parseMessage is OmnicommMessage.parseFrom(data).
func parseMessage(data []byte) *message {
	r := &protoReader{b: data, end: len(data)}
	m := newMessage()
	r.checkLastTag(r.parse(messageSchema, m), 0)
	checkRequired(messageSchema, m)
	return m
}
