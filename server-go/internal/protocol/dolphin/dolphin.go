// Package dolphin is the Dolphin protocol (DolphinProtocol): a binary header followed by
// a protobuf message (DolphinMessages.proto), over TCP.
package dolphin

import (
	"math"
	"strconv"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "dolphin",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{Framer: Framer(), Decoder: ingest.DecoderFunc(decode)}
		}}},
	}
}

// Framer is the LengthFieldBasedFrameDecoder of DolphinProtocol: a little-endian length
// at offset 20, not counting the 4 reserved bytes after it.
func Framer() ingest.Framer {
	f := ingest.NewLengthFramer(ingest.MaxFrameLengthLarge, 20, 4, 4, 0)
	f.LittleEndian = true
	return f
}

// MessageType values.
const (
	msgDataPackRequest  = 1
	msgDataPackResponse = 2
)

// dataPackOK is DataPackResponseCode.DataPack_OK.
const dataPackOK = 1

func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	buf.ReadUnsignedShort() // header
	index := buf.ReadUnsignedIntLE()
	buf.ReadUnsignedShort() // version
	buf.ReadUnsignedShort() // flags
	typ := buf.ReadUnsignedShortLE()

	ds := m.Session(strconv.FormatInt(buf.ReadLongLE(), 10))
	if ds == nil {
		return nil
	}

	length := int(int32(buf.ReadUnsignedIntLE()))
	buf.ReadUnsignedInt() // reserved

	if typ != msgDataPackRequest {
		return nil
	}

	points := parseDataPackRequest(buf.GetBytes(buf.ReaderIndex(), length))

	responseData := []byte{0x08, dataPackOK} // DataPackResponse{Response: DataPack_OK}
	response := kit.Empty()
	response.WriteShort(0xABAB) // header
	response.WriteIntLE(index)
	response.WriteShort(0) // flags
	response.WriteShortLE(msgDataPackResponse)
	response.WriteIntLE(int64(len(responseData)))
	response.WriteIntLE(0) // reserved
	response.WriteBytes(responseData)
	m.Send(response)

	positions := []*model.Position{}
	for _, point := range points {
		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID

		pos.Valid = true
		pos.SetTime(time.UnixMilli(int64(point.timestamp) * 1000))
		pos.SetLatitude(float64(point.latitude))
		pos.SetLongitude(float64(point.longitude))
		pos.Altitude = float64(point.altitude)
		pos.Speed = units.KnotsFromKph(float64(point.speed))
		pos.Course = float64(point.bearing)

		pos.Set(model.KeySatellites, int(point.satellites))
		pos.Set(model.KeyHdop, int(point.hdop))

		for j, id := range point.ioListID {
			pos.Set(model.PrefixIo+strconv.Itoa(int(id)), int64(point.ioListValue[j]))
		}
		positions = append(positions, pos)
	}
	return positions
}

// dataPoint is the DataPoint message.
type dataPoint struct {
	latitude, longitude, altitude float32
	satellites                    int32
	speed, bearing                float32
	hdop                          int32
	timestamp                     int32 // fixed32, read by Java as int
	ioListID                      []int32
	ioListValue                   []uint64
}

// pb reads protobuf wire format; malformed input panics, as parseFrom throws
// InvalidProtocolBufferException.
type pb struct {
	b []byte
	i int
}

func (p *pb) more() bool { return p.i < len(p.b) }

func (p *pb) varint() uint64 {
	var v uint64
	for shift := 0; shift < 64; shift += 7 {
		if p.i >= len(p.b) {
			kit.Throw("InvalidProtocolBufferException: truncated message")
		}
		c := p.b[p.i]
		p.i++
		v |= uint64(c&0x7f) << shift
		if c < 0x80 {
			return v
		}
	}
	kit.Throw("InvalidProtocolBufferException: malformed varint")
	return 0
}

func (p *pb) bytes(n int) []byte {
	if n < 0 || p.i+n > len(p.b) {
		kit.Throw("InvalidProtocolBufferException: truncated message")
	}
	s := p.b[p.i : p.i+n]
	p.i += n
	return s
}

func (p *pb) fixed32() uint32 {
	s := p.bytes(4)
	return uint32(s[0]) | uint32(s[1])<<8 | uint32(s[2])<<16 | uint32(s[3])<<24
}

func (p *pb) float() float32 { return math.Float32frombits(p.fixed32()) }

func (p *pb) skip(wire uint64) {
	switch wire {
	case 0:
		p.varint()
	case 1:
		p.bytes(8)
	case 2:
		p.bytes(int(p.varint()))
	case 5:
		p.bytes(4)
	default:
		kit.Throw("InvalidProtocolBufferException: invalid wire type %d", wire)
	}
}

// varints reads a repeated varint field, packed (wire type 2) or not.
func (p *pb) varints(wire uint64, add func(uint64)) {
	if wire == 2 {
		packed := &pb{b: p.bytes(int(p.varint()))}
		for packed.more() {
			add(packed.varint())
		}
		return
	}
	add(p.varint())
}

func parseDataPackRequest(b []byte) []dataPoint {
	p := &pb{b: b}
	var points []dataPoint
	for p.more() {
		tag := p.varint()
		if tag>>3 == 1 && tag&7 == 2 {
			points = append(points, parseDataPoint(p.bytes(int(p.varint()))))
		} else {
			p.skip(tag & 7)
		}
	}
	return points
}

func parseDataPoint(b []byte) dataPoint {
	p := &pb{b: b}
	var d dataPoint
	for p.more() {
		tag := p.varint()
		field, wire := tag>>3, tag&7
		switch {
		case field == 1 && wire == 5:
			d.latitude = p.float()
		case field == 2 && wire == 5:
			d.longitude = p.float()
		case field == 3 && wire == 5:
			d.altitude = p.float()
		case field == 4 && wire == 0:
			d.satellites = int32(p.varint())
		case field == 5 && wire == 5:
			d.speed = p.float()
		case field == 6 && wire == 5:
			d.bearing = p.float()
		case field == 7 && wire == 0:
			d.hdop = int32(p.varint())
		case field == 8 && wire == 5:
			d.timestamp = int32(p.fixed32())
		case field == 11 && (wire == 0 || wire == 2):
			p.varints(wire, func(v uint64) { d.ioListID = append(d.ioListID, int32(v)) })
		case field == 12 && (wire == 0 || wire == 2):
			p.varints(wire, func(v uint64) { d.ioListValue = append(d.ioListValue, v) })
		default:
			p.skip(wire)
		}
	}
	return d
}
