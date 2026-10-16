// Package mta6 is the MTA6 protocol (Mta6Protocol): binary packets posted over HTTP as
// an "id=...&bin=..." body.
package mta6

import (
	"context"
	"math"
	"net/http"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/config"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one HTTP server; mta6.can selects the full
// (CAN) format instead of the simple one.
func Protocol(cfg *config.Config) (ingest.Protocol, error) {
	return ingest.Protocol{
		Name:    "mta6",
		Servers: []ingest.Server{{HTTP: &Decoder{Simple: !cfg.Bool("mta6.can", false)}}},
	}, nil
}

// Decoder is Mta6ProtocolDecoder.
//
// The Java decoder writes an "HTTP/1.1 100 Continue" response before its "#ACK#"
// response; only the latter is sent here (an HTTP decoder has one response).
type Decoder struct {
	Simple bool
}

// floatReader is FloatReader: floats compressed against the previous value.
type floatReader struct {
	previous int32
}

func (r *floatReader) read(buf *kit.Buf) float32 {
	switch buf.GetUnsignedByte(buf.ReaderIndex()) >> 6 {
	case 0:
		r.previous = buf.ReadInt() << 2
	case 1:
		r.previous = (r.previous & -256) + int32((buf.ReadUnsignedByte()&0x3f)<<2)
	case 2:
		r.previous = (r.previous & -65536) + int32((buf.ReadUnsignedShort()&0x3fff)<<2)
	case 3:
		r.previous = (r.previous & -16777216) + int32((buf.ReadUnsignedMedium()&0x3fffff)<<2)
	}
	return math.Float32frombits(uint32(r.previous))
}

// timeReader is TimeReader: a GPS week time, the week number read once.
type timeReader struct {
	floatReader
	weekNumber int64
}

func (r *timeReader) read(buf *kit.Buf) time.Time {
	weekTime := floatToLong(r.floatReader.read(buf) * 1000)
	if r.weekNumber == 0 {
		r.weekNumber = int64(buf.ReadUnsignedShort())
	}
	b := kit.NewDateBuilder(nil).SetDate(1980, 1, 6)
	b.AddMillis(r.weekNumber*7*24*60*60*1000 + weekTime)
	return b.Date()
}

// floatToLong is Java's (long) cast of a float: NaN is 0, out of range saturates.
func floatToLong(f float32) int64 {
	switch {
	case f != f:
		return 0
	case float64(f) >= math.MaxInt64:
		return math.MaxInt64
	case float64(f) <= math.MinInt64:
		return math.MinInt64
	}
	return int64(f)
}

func skipEvents(buf *kit.Buf) {
	event := buf.ReadUnsignedByte()
	if kit.Check(int64(event), 7) {
		if kit.Check(int64(event), 6) {
			buf.Skip(8)
		} else {
			for kit.Check(int64(event), 7) {
				event = buf.ReadUnsignedByte()
			}
		}
	}
}

func parseFormatA(m *ingest.Message, ds *ingest.DeviceSession, buf *kit.Buf) (positions []*model.Position) {
	positions = []*model.Position{}

	var latitudeReader, longitudeReader floatReader
	var timeReader timeReader

	// A read past the end (IndexOutOfBoundsException) ends the list.
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(*kit.Fault); !ok {
				panic(r)
			}
		}
	}()

	for buf.IsReadable() {
		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID

		flags := buf.ReadUnsignedByte()
		skipEvents(buf)

		pos.SetLatitude(float64(latitudeReader.read(buf)) / math.Pi * 180)
		pos.SetLongitude(float64(longitudeReader.read(buf)) / math.Pi * 180)
		pos.SetTime(timeReader.read(buf))

		if kit.Check(int64(flags), 0) {
			buf.ReadUnsignedByte() // status
		}

		if kit.Check(int64(flags), 1) {
			pos.Altitude = float64(buf.ReadUnsignedShort())
		}

		if kit.Check(int64(flags), 2) {
			pos.Speed = float64(buf.ReadUnsignedShort() & 0x03ff)
			pos.Course = float64(buf.ReadUnsignedByte())
		}

		if kit.Check(int64(flags), 3) {
			pos.Set(model.KeyOdometer, buf.ReadUnsignedShort()*1000)
		}

		if kit.Check(int64(flags), 4) {
			pos.Set(model.KeyFuelConsumption+"Accumulator1", buf.ReadUnsignedInt())
			pos.Set(model.KeyFuelConsumption+"Accumulator2", buf.ReadUnsignedInt())
			pos.Set("hours1", buf.ReadUnsignedShort())
			pos.Set("hours2", buf.ReadUnsignedShort())
		}

		if kit.Check(int64(flags), 5) {
			pos.Set(model.PrefixAdc+"1", buf.ReadUnsignedShort()&0x03ff)
			pos.Set(model.PrefixAdc+"2", buf.ReadUnsignedShort()&0x03ff)
			pos.Set(model.PrefixAdc+"3", buf.ReadUnsignedShort()&0x03ff)
			pos.Set(model.PrefixAdc+"4", buf.ReadUnsignedShort()&0x03ff)
		}

		if kit.Check(int64(flags), 6) {
			pos.Set(model.PrefixTemp+"1", int(buf.ReadSignedByte()))
			buf.GetUnsignedByte(buf.ReaderIndex()) // control (>> 4)
			pos.Set(model.KeyInput, buf.ReadUnsignedShort()&0x0fff)
			buf.ReadUnsignedShort() // old sensor state (& 0x0fff)
		}

		if kit.Check(int64(flags), 7) {
			pos.Set(model.KeyBattery, buf.GetUnsignedByte(buf.ReaderIndex())>>2)
			pos.Set(model.KeyPower, buf.ReadUnsignedShort()&0x03ff)
			pos.Set(model.KeyDeviceTemp, int(buf.ReadSignedByte()))

			pos.Set(model.KeyRssi, (buf.GetUnsignedByte(buf.ReaderIndex())>>4)&0x07)

			satellites := buf.ReadUnsignedByte() & 0x0f
			pos.Valid = satellites >= 3
			pos.Set(model.KeySatellites, satellites)
		}
		positions = append(positions, pos)
	}
	return positions
}

func readFloat(buf *kit.Buf) float32 {
	var r floatReader
	return r.read(buf)
}

func parseFormatA1(m *ingest.Message, ds *ingest.DeviceSession, buf *kit.Buf) *model.Position {
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	flags := buf.ReadUnsignedByte()
	skipEvents(buf)

	pos.SetLatitude(float64(readFloat(buf)) / math.Pi * 180)
	pos.SetLongitude(float64(readFloat(buf)) / math.Pi * 180)
	var tr timeReader
	pos.SetTime(tr.read(buf))

	pos.Set(model.KeyStatus, buf.ReadUnsignedByte())

	if kit.Check(int64(flags), 0) {
		pos.Altitude = float64(buf.ReadUnsignedShort())
		pos.Speed = float64(buf.ReadUnsignedByte())
		pos.Course = float64(buf.ReadSignedByte())
		pos.Set(model.KeyOdometer, float64(readFloat(buf)))
	}

	if kit.Check(int64(flags), 1) {
		pos.Set(model.KeyFuelConsumption, float64(readFloat(buf)))
		pos.Set(model.KeyHours, units.MsFromHoursFloat(float64(readFloat(buf))))
		pos.Set("tank", float64(buf.ReadUnsignedByte())*0.4)
	}

	if kit.Check(int64(flags), 2) {
		pos.Set("engine", float64(buf.ReadUnsignedShort())*0.125)
		pos.Set("pedals", buf.ReadUnsignedByte())
		pos.Set(model.PrefixTemp+"1", buf.ReadUnsignedByte()-40)
		pos.Set(model.KeyOdometerService, buf.ReadUnsignedShort())
	}

	if kit.Check(int64(flags), 3) {
		pos.Set(model.KeyFuel, buf.ReadUnsignedShort())
		pos.Set(model.PrefixAdc+"2", buf.ReadUnsignedShort())
		pos.Set(model.PrefixAdc+"3", buf.ReadUnsignedShort())
		pos.Set(model.PrefixAdc+"4", buf.ReadUnsignedShort())
	}

	if kit.Check(int64(flags), 4) {
		pos.Set(model.PrefixTemp+"1", int(buf.ReadSignedByte()))
		buf.GetUnsignedByte(buf.ReaderIndex()) // control (>> 4)
		pos.Set(model.KeyInput, buf.ReadUnsignedShort()&0x0fff)
		buf.ReadUnsignedShort() // old sensor state (& 0x0fff)
	}

	if kit.Check(int64(flags), 5) {
		pos.Set(model.KeyBattery, buf.GetUnsignedByte(buf.ReaderIndex())>>2)
		pos.Set(model.KeyPower, buf.ReadUnsignedShort()&0x03ff)
		pos.Set(model.KeyDeviceTemp, int(buf.ReadSignedByte()))

		pos.Set(model.KeyRssi, buf.GetUnsignedByte(buf.ReaderIndex())>>5)

		satellites := buf.ReadUnsignedByte() & 0x1f
		pos.Valid = satellites >= 3
		pos.Set(model.KeySatellites, satellites)
	}

	return pos
}

// DecodeHTTP decodes one request; an error is where the Java decoder throws.
func (d *Decoder) DecodeHTTP(_ context.Context, m *ingest.Message, _ *http.Request, body []byte) (resp *ingest.HTTPResponse, positions []*model.Position, err error) {
	defer kit.Recover(&err)
	buf := kit.NewBuf(body)

	buf.Skip(len("id="))
	index := buf.IndexOf(buf.ReaderIndex(), buf.WriterIndex(), '&')
	uniqueID := buf.GetString(buf.ReaderIndex(), index-buf.ReaderIndex())
	ds := m.Session(uniqueID)
	if ds == nil {
		return nil, nil, nil
	}
	buf.Skip(len(uniqueID))
	buf.Skip(len("&bin="))

	packetID := buf.ReadUnsignedByte()
	offset := buf.ReadUnsignedByte() // dataOffset
	packetCount := buf.ReadUnsignedByte()
	buf.ReadUnsignedByte() // reserved
	buf.ReadUnsignedByte() // timezone
	buf.Skip(offset - 5)

	resp = &ingest.HTTPResponse{
		Status: http.StatusOK,
		Body:   append([]byte("#ACK#"), byte(packetID), byte(packetCount), 0),
	}

	if packetID == 0x31 || packetID == 0x32 || packetID == 0x36 {
		if d.Simple {
			return resp, []*model.Position{parseFormatA1(m, ds, buf)}, nil
		}
		return resp, parseFormatA(m, ds, buf), nil
	}
	return resp, nil, nil
}
