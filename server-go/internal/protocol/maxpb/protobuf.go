package maxpb

import "github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"

// A minimal protobuf (proto2) wire reader, enough for MaxPbMessage.proto: what the Java
// generated parseFrom does with the fields read here. Malformed input panics, as
// InvalidProtocolBufferException; fields of an unexpected wire type are unknown fields.

// pbMessage is the fields of a message by number, in wire order.
type pbMessage map[int][]pbValue

type pbValue struct {
	wire   int
	varint uint64 // wire types 0, 1 and 5
	bytes  []byte // wire type 2
}

func pbVarint(data []byte, i int) (uint64, int) {
	var v uint64
	for shift := 0; shift < 70; shift += 7 {
		if i >= len(data) {
			kit.Throw("InvalidProtocolBufferException: While parsing a protocol message, the input ended unexpectedly")
		}
		b := data[i]
		i++
		if shift < 64 {
			v |= uint64(b&0x7f) << shift
		}
		if b < 0x80 {
			return v, i
		}
	}
	kit.Throw("InvalidProtocolBufferException: CodedInputStream encountered a malformed varint.")
	return 0, 0
}

func pbFixed(data []byte, i, n int) (uint64, int) {
	if i+n > len(data) {
		kit.Throw("InvalidProtocolBufferException: While parsing a protocol message, the input ended unexpectedly")
	}
	var v uint64
	for k := n - 1; k >= 0; k-- {
		v = v<<8 | uint64(data[i+k])
	}
	return v, i + n
}

// pbParse reads the fields of a message.
func pbParse(data []byte) pbMessage {
	msg := pbMessage{}
	if i := pbFields(data, 0, 0, msg); i != len(data) {
		kit.Throw("InvalidProtocolBufferException: Protocol message end-group tag did not match expected tag.")
	}
	return msg
}

func pbFields(data []byte, i, group int, msg pbMessage) int {
	for i < len(data) {
		tag, next := pbVarint(data, i)
		tag = uint64(uint32(tag))
		i = next
		num, wire := int(tag>>3), int(tag&7)
		if num == 0 {
			kit.Throw("InvalidProtocolBufferException: Protocol message contained an invalid tag (zero).")
		}
		var v pbValue
		v.wire = wire
		switch wire {
		case 0:
			v.varint, i = pbVarint(data, i)
		case 1:
			v.varint, i = pbFixed(data, i, 8)
		case 5:
			v.varint, i = pbFixed(data, i, 4)
		case 2:
			var n uint64
			n, i = pbVarint(data, i)
			if int32(n) < 0 {
				kit.Throw("InvalidProtocolBufferException: CodedInputStream encountered an embedded string or message which claimed to have negative size.")
			}
			if uint64(len(data)-i) < uint64(uint32(n)) {
				kit.Throw("InvalidProtocolBufferException: While parsing a protocol message, the input ended unexpectedly")
			}
			v.bytes = data[i : i+int(uint32(n))]
			i += int(uint32(n))
		case 3:
			i = pbFields(data, i, num, pbMessage{}) // an unknown group, skipped
			continue
		case 4:
			if group != num {
				kit.Throw("InvalidProtocolBufferException: Protocol message end-group tag did not match expected tag.")
			}
			return i
		default:
			kit.Throw("InvalidProtocolBufferException: Protocol message tag had invalid wire type.")
		}
		msg[num] = append(msg[num], v)
	}
	if group != 0 {
		kit.Throw("InvalidProtocolBufferException: While parsing a protocol message, the input ended unexpectedly")
	}
	return i
}

// pbSchema is the message-typed fields of a message, by number: Java parses embedded
// messages while parsing the outer one, so a malformed one fails the whole message.
type pbSchema map[int]pbSchema

// validate parses the embedded messages of m, panicking where Java's parseFrom throws.
func (m pbMessage) validate(s pbSchema) {
	for num, sub := range s {
		for _, v := range m[num] {
			if v.wire == 2 {
				pbParse(v.bytes).validate(sub)
			}
		}
	}
}

// last is the value of an optional scalar field (the last one wins), if present with the
// field's wire type.
func (m pbMessage) last(num, wire int) (pbValue, bool) {
	values := m[num]
	for k := len(values) - 1; k >= 0; k-- {
		if values[k].wire == wire {
			return values[k], true
		}
	}
	return pbValue{}, false
}

func (m pbMessage) has(num, wire int) bool { _, ok := m.last(num, wire); return ok }

// uint32 is an optional uint32 field as Java's int (0 when absent).
func (m pbMessage) uint32(num int) int {
	v, _ := m.last(num, 0)
	return int(int32(v.varint))
}

// uint64 is an optional uint64 field as Java's long.
func (m pbMessage) uint64(num int) int64 {
	v, _ := m.last(num, 0)
	return int64(v.varint)
}

// sint32 is an optional sint32 field (zigzag).
func (m pbMessage) sint32(num int) int {
	v, _ := m.last(num, 0)
	n := uint32(v.varint)
	return int(int32(n>>1) ^ -int32(n&1))
}

// sfixed32 is an optional sfixed32 field.
func (m pbMessage) sfixed32(num int) int {
	v, _ := m.last(num, 5)
	return int(int32(uint32(v.varint)))
}

// message is an optional message field: its occurrences merged, nil when absent.
func (m pbMessage) message(num int) pbMessage {
	var data []byte
	found := false
	for _, v := range m[num] {
		if v.wire == 2 {
			data = append(data, v.bytes...)
			found = true
		}
	}
	if !found {
		return nil
	}
	return pbParse(data)
}

// messages is a repeated message field.
func (m pbMessage) messages(num int) []pbMessage {
	var list []pbMessage
	for _, v := range m[num] {
		if v.wire == 2 {
			list = append(list, pbParse(v.bytes))
		}
	}
	return list
}

// str is an optional string field, if present.
func (m pbMessage) str(num int) (string, bool) {
	v, ok := m.last(num, 2)
	if !ok {
		return "", false
	}
	return kit.UTF8(v.bytes), true
}
