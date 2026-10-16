package ingest

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf16"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// MQTT, as Traccar's MQTT protocols use it: Netty's MqttDecoder and MqttEncoder on a TCP
// connection, and BaseMqttProtocolDecoder answering CONNECT, SUBSCRIBE and PUBLISH.

// MQTT message types.
const (
	MQTTConnect     = 1
	MQTTConnAck     = 2
	MQTTPublish     = 3
	MQTTPubAck      = 4
	MQTTPubRec      = 5
	MQTTPubRel      = 6
	MQTTPubComp     = 7
	MQTTSubscribe   = 8
	MQTTSubAck      = 9
	MQTTUnsubscribe = 10
	MQTTUnsubAck    = 11
	MQTTPingReq     = 12
	MQTTPingResp    = 13
	MQTTDisconnect  = 14
	MQTTAuth        = 15
)

// MQTTMessage is a message MqttDecoder produced. Invalid is set for a message it couldn't
// decode (Netty's failed decoder result); the connection then ignores everything after it.
type MQTTMessage struct {
	Type     int
	QoS      int
	ClientID string   // CONNECT
	Topic    string   // PUBLISH
	PacketID int      // PUBLISH (-1 at QoS 0), SUBSCRIBE and the like
	Payload  *kit.Buf // PUBLISH
	Invalid  error
}

// MQTT is one connection's MQTT codec: MqttDecoder as a Framer, and the encoder for what
// the server sends, which follows the protocol version of the client's CONNECT.
type MQTT struct {
	version int // protocol level: 3 (3.1), 4 (3.1.1), 5; 0 until CONNECT (3.1.1)
	bad     bool
}

// NewMQTT returns a codec for a new connection.
func NewMQTT() *MQTT { return &MQTT{} }

// MaxMQTTMessage is MqttDecoder's default maxBytesInMessage.
const MaxMQTTMessage = 8092

func (c *MQTT) v5() bool { return c.version == 5 }

// errReplay asks for more bytes (ReplayingDecoder's Signal).
var errReplay = errors.New("replay")

// mqttReader reads a message from the cumulation, asking for more bytes when they run out.
type mqttReader struct {
	b   []byte
	pos int
}

func (r *mqttReader) need(n int) {
	if n < 0 {
		panic(fmt.Errorf("IllegalArgumentException: minimumReadableBytes: %d", n))
	}
	if r.pos+n > len(r.b) {
		panic(errReplay)
	}
}

func (r *mqttReader) u8() int {
	r.need(1)
	v := r.b[r.pos]
	r.pos++
	return int(v)
}

func (r *mqttReader) bytes(n int) []byte {
	r.need(n)
	v := r.b[r.pos : r.pos+n]
	r.pos += n
	return v
}

func (r *mqttReader) msbLsb() int { return r.u8()<<8 | r.u8() }

func (r *mqttReader) int32() int { return int(int32(r.u8()<<24 | r.u8()<<16 | r.u8()<<8 | r.u8())) }

// str is decodeString: the value and the bytes consumed.
func (r *mqttReader) str() (string, int) {
	size := r.msbLsb()
	return kit.UTF8(r.bytes(size)), 2 + size
}

// varInt is decodeVariableByteInteger: the value and the bytes consumed.
func (r *mqttReader) varInt() (int, int) {
	value, multiplier := 0, 1
	for i := 0; i < 4; i++ {
		digit := r.u8()
		value += (digit & 127) * multiplier
		if digit&128 == 0 {
			return value, i + 1
		}
		multiplier *= 128
	}
	panic(errors.New("MQTT protocol limits Remaining Length to 4 bytes"))
}

// properties is decodeProperties, which stops when the bytes read, counting the length
// prefix, reach the properties' length (so it can stop early).
func (r *mqttReader) properties() int {
	total, consumed := r.varInt()
	r.need(total)
	for consumed < total {
		id, n := r.varInt()
		consumed += n
		switch id {
		case 0x01, 0x17, 0x19, 0x24, 0x25, 0x28, 0x29, 0x2A:
			r.u8()
			consumed++
		case 0x13, 0x21, 0x22, 0x23:
			r.msbLsb()
			consumed += 2
		case 0x02, 0x11, 0x18, 0x27:
			r.int32()
			consumed += 4
		case 0x0B:
			_, n := r.varInt()
			consumed += n
		case 0x03, 0x08, 0x12, 0x15, 0x1A, 0x1C, 0x1F:
			_, n := r.str()
			consumed += n
		case 0x26:
			_, n1 := r.str()
			_, n2 := r.str()
			consumed += n1 + n2
		case 0x09, 0x16:
			size := r.msbLsb()
			r.bytes(size)
			consumed += size + 2
		default:
			panic(fmt.Errorf("Unknown property type: %d", id))
		}
	}
	return consumed
}

func (r *mqttReader) messageID() int {
	id := r.msbLsb()
	if id == 0 {
		panic(fmt.Errorf("invalid messageId: %d", id))
	}
	return id
}

// Frame is MqttDecoder.decode: the next message, nil when more bytes are needed.
func (c *MQTT) Frame(_ *Message, in *kit.Buf) any {
	if c.bad {
		in.Skip(in.ReadableBytes()) // keep discarding until disconnection
		return nil
	}
	r := &mqttReader{b: in.Bytes()}
	message, err := c.decode(r)
	switch {
	case errors.Is(err, errReplay):
		return nil
	case err != nil:
		c.bad = true
		in.Skip(in.ReadableBytes())
		return &MQTTMessage{Type: message.Type, Invalid: err}
	}
	in.Skip(r.pos)
	return message
}

func (c *MQTT) decode(r *mqttReader) (message *MQTTMessage, err error) {
	message = &MQTTMessage{}
	defer func() {
		if x := recover(); x != nil {
			e, ok := x.(error)
			if !ok {
				panic(x)
			}
			err = e
		}
	}()

	// Fixed header.
	b1 := r.u8()
	typ := b1 >> 4
	dup, qos, retain := b1&0x08 != 0, (b1&0x06)>>1, b1&0x01 != 0
	if typ == 0 {
		return message, fmt.Errorf("unknown message type: %d", typ)
	}
	message.Type = typ
	switch typ {
	case MQTTPublish:
		if qos == 3 {
			return message, fmt.Errorf("Illegal QOS Level in fixed header of PUBLISH message (%d)", qos)
		}
	case MQTTPubRel, MQTTSubscribe, MQTTUnsubscribe:
		if dup || qos != 1 || retain {
			return message, errors.New("Illegal fixed header flags")
		}
	default:
		if dup || qos != 0 || retain {
			return message, errors.New("Illegal fixed header flags")
		}
	}
	remaining := 0
	for i, multiplier := 0, 1; ; i, multiplier = i+1, multiplier*128 {
		if i == 4 {
			return message, errors.New("remaining length exceeds 4 digits")
		}
		digit := r.u8()
		remaining += (digit & 127) * multiplier
		if digit&128 == 0 {
			break
		}
	}
	if typ == MQTTAuth && !c.v5() {
		return message, errors.New("AUTH message requires at least MQTT 5")
	}
	if typ == MQTTPublish {
		message.QoS = qos
	}
	if remaining > MaxMQTTMessage {
		return message, fmt.Errorf("TooLongFrameException: message length exceeds %d: %d", MaxMQTTMessage, remaining)
	}

	// Variable header.
	var connectFlags int
	switch typ {
	case MQTTConnect:
		name, n := r.str()
		level := int(int8(r.u8()))
		consumed := n + 1
		version := 0
		switch level {
		case 3:
			if name == "MQIsdp" {
				version = 3
			}
		case 4, 5:
			if name == "MQTT" {
				version = level
			}
		}
		if version == 0 {
			return message, fmt.Errorf("MqttUnacceptableProtocolVersionException: %s %d", name, level)
		}
		c.version = version
		connectFlags = r.u8()
		r.msbLsb() // keep alive
		consumed += 3
		if version >= 4 && connectFlags&0x01 != 0 {
			return message, errors.New("non-zero reserved flag")
		}
		if version == 5 {
			consumed += r.properties()
		}
		remaining -= consumed
	case MQTTConnAck:
		r.u8()
		code := int8(r.u8())
		if !validConnectReturnCode(code) {
			return message, fmt.Errorf("unknown connect return code: %d", code)
		}
		if c.v5() {
			remaining -= 2 + r.properties()
		} else {
			remaining -= 2
		}
	case MQTTSubscribe, MQTTUnsubscribe, MQTTSubAck, MQTTUnsubAck:
		message.PacketID = r.messageID()
		if c.v5() {
			remaining -= 2 + r.properties()
		} else {
			remaining -= 2
		}
	case MQTTPubAck, MQTTPubRec, MQTTPubComp, MQTTPubRel:
		message.PacketID = r.messageID()
		switch {
		case remaining > 3:
			r.u8()
			remaining -= 3 + r.properties()
		case remaining > 2:
			r.u8()
			remaining -= 3
		default:
			remaining -= 2
		}
	case MQTTPublish:
		topic, n := r.str()
		if strings.ContainsAny(topic, "#+") {
			return message, fmt.Errorf("invalid publish topic name: %s (contains wildcards)", topic)
		}
		message.Topic = topic
		message.PacketID = -1
		if qos > 0 {
			message.PacketID = r.messageID()
			n += 2
		}
		if c.v5() {
			n += r.properties()
		}
		remaining -= n
	case MQTTDisconnect, MQTTAuth:
		switch {
		case remaining > 1:
			r.u8()
			remaining -= 1 + r.properties()
		case remaining > 0:
			r.u8()
			remaining--
		}
	}

	// Payload.
	noneRemain := func(consumed int) {
		if remaining -= consumed; remaining != 0 {
			panic(fmt.Errorf("non-zero remaining payload bytes: %d", remaining))
		}
	}
	switch typ {
	case MQTTConnect:
		clientID, n := r.str()
		if c.version == 3 && (len(utf16.Encode([]rune(clientID))) < 1 || len(utf16.Encode([]rune(clientID))) > 23) {
			return message, fmt.Errorf("MqttIdentifierRejectedException: invalid clientIdentifier: %s", clientID)
		}
		message.ClientID = clientID
		consumed := n
		if connectFlags&0x04 != 0 { // will
			if c.v5() {
				consumed += r.properties()
			}
			_, n := r.str() // will topic
			size := r.msbLsb()
			r.bytes(size) // will message
			consumed += n + size + 2
		}
		if connectFlags&0x80 != 0 {
			_, n := r.str()
			consumed += n
		}
		if connectFlags&0x40 != 0 {
			size := r.msbLsb()
			r.bytes(size)
			consumed += size + 2
		}
		noneRemain(consumed)
	case MQTTSubscribe:
		consumed := 0
		for consumed < remaining {
			_, n := r.str()
			option := r.u8()
			if option&0x03 == 3 {
				return message, fmt.Errorf("invalid QoS: %d", option&0x03)
			}
			if (option&0x30)>>4 == 3 {
				return message, errors.New("invalid RetainedHandlingPolicy: 3")
			}
			consumed += n + 1
		}
		noneRemain(consumed)
	case MQTTSubAck, MQTTUnsubAck:
		consumed := 0
		for consumed < remaining {
			r.u8()
			consumed++
		}
		noneRemain(consumed)
	case MQTTUnsubscribe:
		consumed := 0
		for consumed < remaining {
			_, n := r.str()
			consumed += n
		}
		noneRemain(consumed)
	case MQTTPublish:
		message.Payload = kit.NewBuf(append([]byte(nil), r.bytes(remaining)...))
	}
	return message, nil
}

func validConnectReturnCode(code int8) bool {
	switch uint8(code) {
	case 0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x80, 0x81, 0x82, 0x83, 0x84, 0x85, 0x86, 0x87, 0x88,
		0x89, 0x8A, 0x8C, 0x90, 0x95, 0x97, 0x99, 0x9A, 0x9B, 0x9C, 0x9D, 0x9F:
		return true
	}
	return false
}

// Encoding (MqttEncoder): MQTT 5 adds an empty property list.

func (c *MQTT) properties() []byte {
	if c.v5() {
		return []byte{0}
	}
	return nil
}

func mqttFrame(header byte, body []byte) *kit.Buf {
	out := kit.Empty().WriteByte8(int(header))
	n := len(body)
	for {
		digit := n % 128
		n /= 128
		if n > 0 {
			digit |= 0x80
		}
		out.WriteByte8(digit)
		if n == 0 {
			break
		}
	}
	return out.WriteBytes(body)
}

// ConnAck is a CONNACK with a return code (0 accepted, 2 identifier rejected).
func (c *MQTT) ConnAck(returnCode int) *kit.Buf {
	return mqttFrame(0x20, append([]byte{0, byte(returnCode)}, c.properties()...))
}

// SubAck is MqttMessageBuilders.subAck().packetId(id): no reason codes.
func (c *MQTT) SubAck(packetID int) *kit.Buf {
	return mqttFrame(0x90, append([]byte{byte(packetID >> 8), byte(packetID)}, c.properties()...))
}

// PubAck is MqttMessageBuilders.pubAck().packetId(id).
func (c *MQTT) PubAck(packetID int) *kit.Buf {
	return mqttFrame(0x40, []byte{byte(packetID >> 8), byte(packetID)})
}

// Publish is a PUBLISH message (MqttMessageBuilders.publish()).
func (c *MQTT) Publish(topic string, qos, packetID int, payload []byte) *kit.Buf {
	t := []byte(topic)
	body := append([]byte{byte(len(t) >> 8), byte(len(t))}, t...)
	if qos > 0 {
		body = append(body, byte(packetID>>8), byte(packetID))
	}
	body = append(body, c.properties()...)
	return mqttFrame(byte(MQTTPublish<<4|qos<<1), append(body, payload...))
}

// MQTTDecoder is BaseMqttProtocolDecoder: CONNECT identifies the device by its client id
// (CONNACK accepted or identifier rejected), SUBSCRIBE gets a SUBACK, and a PUBLISH from an
// identified device goes to decode, then gets a PUBACK. Anything else decodes to nothing.
func MQTTDecoder(codec *MQTT, decode func(m *Message, ds *DeviceSession, publish *MQTTMessage) any) Decoder {
	return DecoderFunc(func(m *Message, msg any) any {
		message, ok := msg.(*MQTTMessage)
		if !ok || message.Invalid != nil {
			return nil
		}
		switch message.Type {
		case MQTTConnect:
			code := 0x00
			if m.Session(message.ClientID) == nil {
				code = 0x02
			}
			m.Send(codec.ConnAck(code))
		case MQTTSubscribe:
			m.Send(codec.SubAck(message.PacketID))
		case MQTTPublish:
			ds := m.Session()
			if ds == nil {
				return nil
			}
			result := decode(m, ds, message)
			if message.PacketID < 1 {
				// A QoS 0 message has no packet id: building the PUBACK throws, so the
				// message's positions are dropped and the connection is closed.
				kit.Throw("IllegalArgumentException: messageId: %d (expected: 1 ~ 65535)", message.PacketID)
			}
			m.Send(codec.PubAck(message.PacketID))
			return result
		}
		return nil
	})
}
