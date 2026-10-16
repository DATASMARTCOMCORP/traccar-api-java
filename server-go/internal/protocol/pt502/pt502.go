// Package pt502 is the PT502 protocol (Pt502Protocol): "$POS" text reports over TCP,
// photos fetched in chunks.
package pt502

import (
	"strconv"
	"strings"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/javatime"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name:         "pt502",
		DataCommands: protocol.DataCommands("pt502"),
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  Framer(),
				TextOut: true,
				Decoder: &Decoder{},
				Encoder: ingest.CommandEncoderFunc(encode),
			}
		}}},
	}
}

const binaryHeader = 5

// Framer is Pt502FrameDecoder: binary photo chunks ("$PHD" after a 5-byte header, with
// a length), else lines ending in CR or LF (an optional 5-byte header is skipped).
func Framer() ingest.Framer {
	return &ingest.BaseFramer{MaxLength: ingest.MaxFrameLengthLarge, Decode: func(_ *ingest.Message, buf *kit.Buf) any {
		if buf.ReadableBytes() < 10 {
			return nil
		}
		r := buf.ReaderIndex()
		if buf.GetUnsignedByte(r) == 0xbf && buf.GetString(r+binaryHeader, 4) == "$PHD" {
			length := buf.GetUnsignedShortLE(r + 3)
			if buf.ReadableBytes() >= length {
				buf.Skip(binaryHeader)
				result := buf.ReadSlice(length - binaryHeader - 2)
				buf.Skip(2) // line break
				return result
			}
			return nil
		}
		if buf.GetUnsignedByte(r) == 0xbf {
			buf.Skip(binaryHeader)
		}
		index := buf.IndexOf(buf.ReaderIndex(), buf.WriterIndex(), '\r')
		if index < 0 {
			index = buf.IndexOf(buf.ReaderIndex(), buf.WriterIndex(), '\n')
		}
		if index > 0 {
			result := buf.ReadSlice(index - buf.ReaderIndex())
			for buf.IsReadable() && (buf.GetByte(buf.ReaderIndex()) == '\r' || buf.GetByte(buf.ReaderIndex()) == '\n') {
				buf.Skip(1)
			}
			return result
		}
		return nil
	}}
}

var pattern = kit.NewPattern().
	Any().Text("$").
	Expression("([^,]+),").        // type
	Number("(d+),").               // id
	Number("(dd)(dd)(dd).(ddd),"). // time (hhmmss.sss)
	Expression("([AV]),").         // validity
	Number("(d+)(dd.dddd),").      // latitude
	Expression("([NS]),").
	Number("(d+)(dd.dddd),"). // longitude
	Expression("([EW]),").
	Number("(d+.d+)?,").        // speed
	Number("(d+.d+)?,").        // course
	Number("(dd)(dd)(dd),,,?"). // date (ddmmyy)
	Expression(".?/").
	Expression("([01]+),").       // input
	Expression("([01]+)/").       // output
	Expression("([^/]+)?/").      // adc
	Number("(d+)").               // odometer
	Expression("/([^/]+)?/").     // rfid
	Number("(xxx)").OptionalN(2). // state
	Any().
	Compile()

func decodeAlarm(value string) string {
	switch value {
	case "IN1":
		return model.AlarmSos
	case "GOF":
		return model.AlarmGeofence
	case "TOW":
		return model.AlarmTow
	case "HDA":
		return model.AlarmAcceleration
	case "HDB":
		return model.AlarmBraking
	case "FDA":
		return model.AlarmFatigueDriving
	case "SKA":
		return model.AlarmVibration
	case "PMA":
		return model.AlarmMovement
	case "CPA":
		return model.AlarmPowerCut
	}
	return ""
}

func decodePosition(m *ingest.Message, sentence string) any {
	parser := kit.NewParser(pattern, sentence)
	if !parser.Matches() {
		return nil
	}
	pos := m.NewPosition()
	pos.AddAlarm(decodeAlarm(parser.Next()))
	ds := m.Session(parser.Next())
	if ds == nil {
		return nil
	}
	pos.DeviceID = ds.DeviceID

	b := kit.NewDateBuilder(nil).SetTimeMillis(parser.MustInt(), parser.MustInt(), parser.MustInt(), parser.MustInt())
	pos.Valid = parser.Next() == "A"
	pos.SetLatitude(parser.NextCoordinate(kit.DegMinHem))
	pos.SetLongitude(parser.NextCoordinate(kit.DegMinHem))
	pos.Speed = parser.NextDoubleDefault(0)
	pos.Course = parser.NextDoubleDefault(0)
	b.SetDateReverse(parser.MustInt(), parser.MustInt(), parser.MustInt())
	pos.SetTime(b.Date())

	pos.Set(model.KeyInput, parser.NextBinInt())
	pos.Set(model.KeyOutput, parser.NextBinInt())
	if parser.HasNext() {
		for i, v := range kit.Split(parser.Next(), ",") {
			pos.Set(model.PrefixAdc+strconv.Itoa(i+1), kit.IntRadix(v, 16))
		}
	}
	pos.Set(model.KeyOdometer, parser.NextIntDefault(0))
	pos.Set(model.KeyDriverUniqueID, parser.NextNullable())
	if parser.HasNext() {
		value := parser.NextHexIntDefault(0)
		pos.Set(model.KeyBattery, value>>8)
		pos.Set(model.KeyRssi, (value>>4)&0xf)
		pos.Set(model.KeySatellites, value&0xf)
	}
	return pos
}

const maxChunkSize = 960

// Decoder is Pt502ProtocolDecoder with the connection's photo in progress.
type Decoder struct {
	photo *kit.MediaBuffer
}

func (d *Decoder) requestPhotoFragment(m *ingest.Message) {
	if m.HasChannel() {
		m.Send("#PHD" + strconv.Itoa(len(d.photo.Data)) + "," + strconv.Itoa(min(d.photo.Writable(), maxChunkSize)) + "\r\n")
	}
}

func (d *Decoder) Decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)
	typeEnd := buf.IndexOf(buf.ReaderIndex(), buf.WriterIndex(), ',')
	typ := buf.GetString(buf.ReaderIndex(), typeEnd-buf.ReaderIndex())

	if strings.HasPrefix(typ, "$PHD") {
		buf.SetReaderIndex(buf.IndexOf(typeEnd+1, buf.WriterIndex(), ',') + 1)
		if d.photo == nil {
			return nil
		}
		d.photo.Write(buf.ReadBytes(buf.ReadableBytes()))
		if d.photo.Writable() > 0 {
			d.requestPhotoFragment(m)
			return nil
		}
		ds := m.Session()
		if ds == nil {
			kit.Throw("NullPointerException: no device session")
		}
		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID
		m.LastLocation(pos, nil)
		image := m.WriteMedia(ds.UniqueID, "jpg", d.photo.Data)
		d.photo = nil
		pos.Set(model.KeyImage, image)
		return pos
	}

	if strings.HasPrefix(typ, "$PHO") {
		size := kit.Int(kit.SubstringFrom(kit.Split(typ, "-")[0], 4))
		if size > 0 {
			d.photo = m.NewSizedMediaBuffer(size)
			d.requestPhotoFragment(m)
		}
	}
	return decodePosition(m, kit.Latin1(buf.Bytes()))
}

func encode(m *ingest.Message, c *model.Command) any {
	format := func(f string, keys ...string) any {
		return kit.FormatCommandWith(c, m.UniqueID(c.DeviceID), f, func(key string, value any) (string, bool) {
			if key != model.CommandKeyTimezone {
				return "", false
			}
			name, ok := value.(string)
			if !ok {
				kit.Throw("ClassCastException: timezone is %T", value)
			}
			return strconv.Itoa(javatime.RawOffset(javatime.TimeZone(name), m.Now) / 3600), true
		}, keys...)
	}
	switch c.Type {
	case model.CommandCustom:
		return format("%s\r\n", model.CommandKeyData)
	case model.CommandOutputControl:
		return format("#OPC%s,%s\r\n", model.CommandKeyIndex, model.CommandKeyData)
	case model.CommandSetTimezone:
		return format("#TMZ%s\r\n", model.CommandKeyTimezone)
	case model.CommandAlarmSpeed:
		return format("#SPD%s\r\n", model.CommandKeyData)
	case model.CommandRequestPhoto:
		return format("#PHO\r\n")
	}
	return nil
}
