// Package maxpb is the MaxPB protocol (MaxPbProtocol): binary frames carrying protobuf
// messages (MaxPbMessage.proto).
package maxpb

import (
	"strconv"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "maxpb",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{Framer: Framer(), Decoder: ingest.DecoderFunc(decode)}
		}}},
	}
}

// Framer is MaxPbFrameDecoder: a 6-byte header whose last two bytes are the payload
// length (little endian).
func Framer() ingest.Framer {
	return &ingest.BaseFramer{Decode: func(_ *ingest.Message, buf *kit.Buf) any {
		if buf.ReadableBytes() < 6 {
			return nil
		}
		length := 6 + buf.GetUnsignedShortLE(buf.ReaderIndex()+4)
		if buf.ReadableBytes() >= length {
			return buf.ReadSlice(length)
		}
		return nil
	}}
}

// Message types.
const (
	msgPosition = 0x0001
	msgAck      = 0x0002
	msgStatus   = 0x0011
)

// The embedded messages of MaxPbMessage.proto.
var (
	idPackSchema     = pbSchema{}
	reportDataSchema = pbSchema{
		4: {},                    // positionInfo
		5: {},                    // gps
		6: {2: {}, 3: {}},        // ios: inputInfo, outputInfo
		7: {1: {}, 2: {}, 3: {}}, // flags: deviceInfo, connectionInfo, operationalInfo
	}
	multipleReportDataSchema = pbSchema{3: idPackSchema, 4: reportDataSchema, 5: reportDataSchema}
	reportStatusSchema       = pbSchema{3: idPackSchema}
)

// session is getDeviceSession(channel, remoteAddress, deviceId, idPack).
func session(m *ingest.Message, deviceID int64, idPack pbMessage) *ingest.DeviceSession {
	if imei, ok := idPack.str(1); ok {
		return m.Session(imei, strconv.FormatInt(deviceID, 10))
	}
	return m.Session(strconv.FormatInt(deviceID, 10))
}

func decodeReport(m *ingest.Message, ds *ingest.DeviceSession, report pbMessage) *model.Position {
	info := report.message(4)
	if info == nil {
		return nil
	}
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID
	if report.has(3, 0) {
		pos.SetTime(time.UnixMilli(report.uint64(3) * 1000))
	} else {
		pos.SetTime(m.Now)
	}

	pos.SetLatitude(float64(info.sfixed32(3)) / 10000000.0)
	pos.SetLongitude(float64(info.sfixed32(4)) / 10000000.0)
	pos.Altitude = float64(info.sint32(5))
	pos.Course = float64(info.uint32(7))
	pos.Accuracy = float64(info.uint32(6))

	if gps := report.message(5); gps != nil {
		pos.Valid = gps.uint32(2) >= 2
		pos.Set(model.KeySatellites, gps.uint32(3))
		pos.Set(model.KeyHdop, float64(gps.uint32(5))/10.0)
	} else {
		pos.Valid = true
	}

	if ios := report.message(6); ios != nil {
		pos.Set(model.KeyIgnition, ios.uint32(1) != 0)
	}

	if flags := report.message(7); flags != nil {
		if deviceInfo := flags.message(1); deviceInfo != nil {
			pos.Set(model.KeyPower, float64(deviceInfo.uint32(2))/1000.0)
			pos.Set(model.KeyBattery, float64(deviceInfo.uint32(5))/1000.0)
			pos.Set(model.KeyBatteryLevel, deviceInfo.uint32(4))
		}
	}
	return pos
}

func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)
	buf.Skip(4)               // header
	buf.ReadUnsignedShortLE() // size
	buf.ReadUnsignedShortLE() // crc
	typ := buf.ReadUnsignedShortLE()
	if buf.ReadUnsignedShortLE() != 0 { // format
		return nil
	}
	data := buf.Bytes()

	switch typ {
	case msgPosition:
		message := pbParse(data)
		message.validate(multipleReportDataSchema)
		ds := session(m, message.uint64(1), message.message(3))
		if ds == nil {
			return nil
		}
		var positions []*model.Position
		for _, report := range message.messages(5) {
			if pos := decodeReport(m, ds, report); pos != nil {
				positions = append(positions, pos)
			}
		}
		if report := message.message(4); report != nil {
			if pos := decodeReport(m, ds, report); pos != nil {
				positions = append(positions, pos)
			}
		}
		if len(positions) == 0 {
			return nil
		}
		return positions
	case msgStatus:
		message := pbParse(data)
		message.validate(reportStatusSchema)
		session(m, message.uint64(1), message.message(3))
	}
	return nil
}
