package starlink

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// tdx is the StarLinkMessage.mEventReport_TDx fields the decoder reads (proto2 defaults
// when absent).
type tdx struct {
	sensorNumber int64 // uint32, as Java's int: the bits of the low 32
	sensorID     string
	temperature  int32
	voltage      int64
}

func varint(data []byte, i int) (uint64, int) {
	var v uint64
	for shift := 0; ; shift += 7 {
		if i >= len(data) || shift >= 64 {
			kit.Throw("InvalidProtocolBufferException: truncated varint")
		}
		b := data[i]
		i++
		v |= uint64(b&0x7f) << shift
		if b < 0x80 {
			return v, i
		}
	}
}

// parseTDx is mEventReport_TDx.parseFrom: unknown fields are skipped; SensorNumber is
// required.
func parseTDx(data []byte) tdx {
	var t tdx
	hasNumber := false
	for i := 0; i < len(data); {
		key, next := varint(data, i)
		i = next
		num, wire := int(key>>3), int(key&7)
		switch wire {
		case 0:
			v, next := varint(data, i)
			i = next
			switch num {
			case 1:
				t.sensorNumber, hasNumber = int64(int32(uint32(v))), true
			case 4:
				u := uint32(v)
				t.temperature = int32(u>>1) ^ -int32(u&1)
			case 9:
				t.voltage = int64(int32(uint32(v)))
			}
		case 1:
			i += 8
		case 2:
			n, next := varint(data, i)
			i = next
			end := i + int(n)
			if n > uint64(len(data)) || end > len(data) {
				kit.Throw("InvalidProtocolBufferException: truncated message")
			}
			if num == 3 {
				t.sensorID = kit.UTF8(data[i:end])
			}
			i = end
		case 5:
			i += 4
		default:
			kit.Throw("InvalidProtocolBufferException: invalid wire type")
		}
		if i > len(data) {
			kit.Throw("InvalidProtocolBufferException: truncated message")
		}
	}
	if !hasNumber {
		kit.Throw("InvalidProtocolBufferException: missing SensorNumber")
	}
	return t
}
