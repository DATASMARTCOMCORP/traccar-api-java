// Package navigil is the Navigil protocol (NavigilProtocol): little-endian binary
// messages with a 20-byte header, optionally after a preamble.
package navigil

import (
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
		Name: "navigil",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  Framer(),
				Decoder: NewDecoder(),
			}
		}}},
	}
}

const (
	messageHeader = 20
	preamble      = 0x2477F5F6
)

// Framer is NavigilFrameDecoder: the length is in the header, after an optional
// preamble that is dropped.
func Framer() ingest.Framer {
	return &ingest.BaseFramer{Decode: func(_ *ingest.Message, buf *kit.Buf) any {
		if buf.ReadableBytes() < messageHeader {
			return nil
		}
		hasPreamble := buf.GetUnsignedIntLE(buf.ReaderIndex()) == preamble
		length := buf.GetUnsignedShortLE(buf.ReaderIndex() + 6)
		if buf.ReadableBytes() >= length {
			if hasPreamble {
				buf.ReadUnsignedIntLE()
				length -= 4
			}
			return buf.ReadSlice(length)
		}
		return nil
	}}
}

const leapSecondsDelta = 25

const (
	msgUnitReport      = 8
	msgTg2Report       = 12
	msgPositionReport  = 13
	msgPositionReport2 = 15
	msgSnapshot4       = 17
	msgTrackingData    = 18
	msgAcknowledgement = 255
)

func convertTimestamp(timestamp int64) time.Time {
	return time.UnixMilli((timestamp - leapSecondsDelta) * 1000)
}

// Decoder is NavigilProtocolDecoder: it numbers its acknowledgements.
type Decoder struct {
	senderSequenceNumber int
}

// NewDecoder returns a decoder whose first acknowledgement is number 1.
func NewDecoder() *Decoder { return &Decoder{senderSequenceNumber: 1} }

func (d *Decoder) sendAcknowledgment(m *ingest.Message, sequenceNumber int) {
	data := kit.Empty()
	data.WriteShortLE(sequenceNumber)
	data.WriteShortLE(0) // OK

	header := kit.Empty()
	header.WriteByte8(1)
	header.WriteByte8(0)
	header.WriteShortLE(d.senderSequenceNumber)
	d.senderSequenceNumber++
	header.WriteShortLE(msgAcknowledgement)
	header.WriteShortLE(20 + 4)
	header.WriteShortLE(0)
	header.WriteShortLE(kit.CRC16(kit.CRC16_CCITT_FALSE, data.Bytes()))
	header.WriteIntLE(0)
	header.WriteIntLE(int64(int32(m.Now.UnixMilli()/1000) + leapSecondsDelta))

	m.Send(header.WriteBuf(data))
}

func newPosition(m *ingest.Message, ds *ingest.DeviceSession, sequenceNumber int) *model.Position {
	pos := m.NewPosition()
	pos.Set(model.KeyIndex, sequenceNumber)
	pos.DeviceID = ds.DeviceID
	return pos
}

func parseUnitReport(m *ingest.Message, ds *ingest.DeviceSession, buf *kit.Buf, sequenceNumber int) *model.Position {
	pos := m.NewPosition()

	pos.Valid = true
	pos.Set(model.KeyIndex, sequenceNumber)
	pos.DeviceID = ds.DeviceID

	buf.ReadUnsignedShortLE() // report trigger
	pos.Set(model.KeyFlags, buf.ReadUnsignedShortLE())

	pos.SetLatitude(float64(buf.ReadIntLE()) / 10000000.0)
	pos.SetLongitude(float64(buf.ReadIntLE()) / 10000000.0)
	pos.Altitude = float64(buf.ReadUnsignedShortLE())

	pos.Set(model.KeySatellites, buf.ReadUnsignedShortLE())
	pos.Set(model.KeySatellitesVisible, buf.ReadUnsignedShortLE())
	pos.Set("gpsAntennaState", buf.ReadUnsignedShortLE())

	pos.Speed = float64(buf.ReadUnsignedShortLE()) * 0.194384
	pos.Course = float64(buf.ReadUnsignedShortLE())

	pos.Set(model.KeyOdometer, buf.ReadUnsignedIntLE())
	pos.Set(model.KeyDistance, buf.ReadUnsignedIntLE())

	pos.Set(model.KeyBattery, float64(buf.ReadUnsignedShortLE())/1000.0)

	pos.Set(model.KeyCharge, buf.ReadUnsignedShortLE())

	pos.SetTime(convertTimestamp(buf.ReadUnsignedIntLE()))

	return pos
}

func parseTg2Report(m *ingest.Message, ds *ingest.DeviceSession, buf *kit.Buf, sequenceNumber int) *model.Position {
	pos := m.NewPosition()

	pos.Valid = true
	pos.Set(model.KeyIndex, sequenceNumber)
	pos.DeviceID = ds.DeviceID

	buf.ReadUnsignedShortLE() // report trigger
	buf.ReadUnsignedByte()    // reserved
	buf.ReadUnsignedByte()    // assisted GPS age

	pos.SetTime(convertTimestamp(buf.ReadUnsignedIntLE()))

	pos.SetLatitude(float64(buf.ReadIntLE()) / 10000000.0)
	pos.SetLongitude(float64(buf.ReadIntLE()) / 10000000.0)
	pos.Altitude = float64(buf.ReadUnsignedShortLE())

	pos.Set(model.KeySatellites, buf.ReadUnsignedByte())
	pos.Set(model.KeySatellitesVisible, buf.ReadUnsignedByte())

	pos.Speed = float64(buf.ReadUnsignedShortLE()) * 0.194384
	pos.Course = float64(buf.ReadUnsignedShortLE())

	pos.Set(model.KeyOdometer, buf.ReadUnsignedIntLE())
	pos.Set("maximumSpeed", buf.ReadUnsignedShortLE())
	pos.Set("minimumSpeed", buf.ReadUnsignedShortLE())

	pos.Set(model.PrefixIo+"1", buf.ReadUnsignedShortLE()) // VSAUT1 voltage
	pos.Set(model.PrefixIo+"2", buf.ReadUnsignedShortLE()) // VSAUT2 voltage
	pos.Set(model.PrefixIo+"3", buf.ReadUnsignedShortLE()) // solar voltage

	pos.Set(model.KeyBattery, float64(buf.ReadUnsignedShortLE())/1000.0)

	return pos
}

func parsePositionReport(m *ingest.Message, ds *ingest.DeviceSession, buf *kit.Buf, sequenceNumber int, timestamp int64) *model.Position {
	pos := newPosition(m, ds, sequenceNumber)
	pos.SetTime(convertTimestamp(timestamp))

	pos.SetLatitude(float64(buf.ReadMediumLE()) / 50000.0)
	pos.SetLongitude(float64(buf.ReadMediumLE()) / 50000.0)

	pos.Speed = units.KnotsFromKph(float64(buf.ReadUnsignedByte()))
	pos.Course = float64(buf.ReadUnsignedByte() * 2)

	flags := buf.ReadUnsignedByte()
	pos.Valid = flags&0x80 == 0x80 && flags&0x40 == 0x40

	buf.ReadUnsignedByte() // reserved

	return pos
}

func parsePositionReport2(m *ingest.Message, ds *ingest.DeviceSession, buf *kit.Buf, sequenceNumber int, timestamp int64) *model.Position {
	pos := newPosition(m, ds, sequenceNumber)
	pos.SetTime(convertTimestamp(timestamp))

	pos.SetLatitude(float64(buf.ReadIntLE()) / 10000000.0)
	pos.SetLongitude(float64(buf.ReadIntLE()) / 10000000.0)

	buf.ReadUnsignedByte() // report trigger

	pos.Speed = units.KnotsFromKph(float64(buf.ReadUnsignedByte()))

	flags := buf.ReadUnsignedByte()
	pos.Valid = flags&0x80 == 0x80 && flags&0x40 == 0x40

	pos.Set(model.KeySatellites, buf.ReadUnsignedByte())
	pos.Set(model.KeyOdometer, buf.ReadUnsignedIntLE())

	return pos
}

func parseSnapshot4(m *ingest.Message, ds *ingest.DeviceSession, buf *kit.Buf, sequenceNumber int) *model.Position {
	pos := newPosition(m, ds, sequenceNumber)

	buf.ReadUnsignedByte() // report trigger
	buf.ReadUnsignedByte() // position fix source
	buf.ReadUnsignedByte() // GNSS fix quality
	buf.ReadUnsignedByte() // GNSS assistance age

	flags := buf.ReadUnsignedIntLE()
	pos.Valid = flags&0x0400 == 0x0400

	pos.SetTime(convertTimestamp(buf.ReadUnsignedIntLE()))

	pos.SetLatitude(float64(buf.ReadIntLE()) / 10000000.0)
	pos.SetLongitude(float64(buf.ReadIntLE()) / 10000000.0)
	pos.Altitude = float64(buf.ReadUnsignedShortLE())

	pos.Set(model.KeySatellites, buf.ReadUnsignedByte())
	pos.Set(model.KeySatellitesVisible, buf.ReadUnsignedByte())

	pos.Speed = float64(buf.ReadUnsignedShortLE()) * 0.194384
	pos.Course = float64(buf.ReadUnsignedShortLE()) / 10.0

	pos.Set("maximumSpeed", buf.ReadUnsignedByte())
	pos.Set("minimumSpeed", buf.ReadUnsignedByte())
	pos.Set(model.KeyOdometer, buf.ReadUnsignedIntLE())

	pos.Set(model.PrefixIo+"1", buf.ReadUnsignedByte()) // supply voltage 1
	pos.Set(model.PrefixIo+"2", buf.ReadUnsignedByte()) // supply voltage 2
	pos.Set(model.KeyBattery, float64(buf.ReadUnsignedShortLE())/1000.0)

	return pos
}

func parseTrackingData(m *ingest.Message, ds *ingest.DeviceSession, buf *kit.Buf, sequenceNumber int, timestamp int64) *model.Position {
	pos := newPosition(m, ds, sequenceNumber)
	pos.SetTime(convertTimestamp(timestamp))

	buf.ReadUnsignedByte() // tracking mode

	flags := buf.ReadUnsignedByte()
	pos.Valid = flags&0x01 == 0x01

	buf.ReadUnsignedShortLE() // duration

	pos.SetLatitude(float64(buf.ReadIntLE()) / 10000000.0)
	pos.SetLongitude(float64(buf.ReadIntLE()) / 10000000.0)

	pos.Speed = units.KnotsFromKph(float64(buf.ReadUnsignedByte()))
	pos.Course = float64(buf.ReadUnsignedByte()) * 2.0

	pos.Set(model.KeySatellites, buf.ReadUnsignedByte())
	pos.Set(model.KeyBattery, float64(buf.ReadUnsignedShortLE())/1000.0)
	pos.Set(model.KeyOdometer, buf.ReadUnsignedIntLE())

	return pos
}

// Decode decodes one message.
func (d *Decoder) Decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	buf.ReadUnsignedByte() // protocol version
	buf.ReadUnsignedByte() // version id
	sequenceNumber := buf.ReadUnsignedShortLE()
	messageID := buf.ReadUnsignedShortLE()
	buf.ReadUnsignedShortLE() // length
	flags := buf.ReadUnsignedShortLE()
	buf.ReadUnsignedShortLE() // checksum

	ds := m.Session(strconv.FormatInt(buf.ReadUnsignedIntLE(), 10))
	if ds == nil {
		return nil
	}

	timestamp := buf.ReadUnsignedIntLE()

	if flags&0x1 == 0 {
		d.sendAcknowledgment(m, sequenceNumber)
	}

	switch messageID {
	case msgUnitReport:
		return parseUnitReport(m, ds, buf, sequenceNumber)
	case msgTg2Report:
		return parseTg2Report(m, ds, buf, sequenceNumber)
	case msgPositionReport:
		return parsePositionReport(m, ds, buf, sequenceNumber, timestamp)
	case msgPositionReport2:
		return parsePositionReport2(m, ds, buf, sequenceNumber, timestamp)
	case msgSnapshot4:
		return parseSnapshot4(m, ds, buf, sequenceNumber)
	case msgTrackingData:
		return parseTrackingData(m, ds, buf, sequenceNumber, timestamp)
	}
	return nil
}
