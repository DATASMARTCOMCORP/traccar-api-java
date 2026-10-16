// Package wli is the WLI protocol (WliProtocol): escaped binary frames between 0x02 and
// 0x03 over TCP, with numbered fields.
package wli

import (
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "wli",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{Framer: Framer(), Decoder: ingest.DecoderFunc(decode)}
		}}},
	}
}

// Framer is WliFrameDecoder: up to the 0x03 end byte, with 0xDB escapes undone.
func Framer() ingest.Framer {
	return &ingest.BaseFramer{Decode: func(_ *ingest.Message, buf *kit.Buf) any {
		if buf.ReadableBytes() < 2 {
			return nil
		}
		index := buf.IndexOf(buf.ReaderIndex()+1, buf.WriterIndex(), 0x03)
		if index == -1 {
			return nil
		}
		result := kit.Empty()
		for buf.ReaderIndex() <= index {
			b := buf.ReadUnsignedByte()
			if b == 0xDB {
				switch buf.ReadUnsignedByte() {
				case 0xD2:
					result.WriteByte8(0x02)
				case 0xD3:
					result.WriteByte8(0x03)
				case 0xDD:
					result.WriteByte8(0xDB)
				}
			} else {
				result.WriteByte8(b)
			}
		}
		return result
	}}
}

func deviceTime(pos *model.Position) *time.Time {
	if pos.DeviceTime.IsZero() {
		return nil
	}
	t := pos.DeviceTime.Time
	return &t
}

func ptr[T any](v T) *T { return &v }

// decode is WliProtocolDecoder.decode.
func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	buf.ReadUnsignedByte() // header
	switch buf.ReadUnsignedByte() {
	case '1':
		ds := m.Session()
		if ds == nil {
			return nil
		}

		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID

		var cellTower model.CellTower

		pos.Set(model.KeyIndex, buf.ReadUnsignedShort())

		buf.ReadUnsignedShort() // length
		buf.ReadUnsignedShort() // checksum
		typ := buf.ReadUnsignedByte()
		buf.ReadUnsignedByte() // delimiter

		for buf.ReadableBytes() > 1 {

			fieldNumber := buf.ReadUnsignedByte()

			buf.ReadUnsignedByte() // delimiter

			if buf.GetUnsignedByte(buf.ReaderIndex()) == 0xFF {

				buf.ReadUnsignedByte() // binary type indication
				endIndex := buf.ReadUnsignedShort() + buf.ReaderIndex()

				if fieldNumber == 52 {
					pos.Valid = true
					buf.ReadUnsignedByte() // reason
					buf.ReadUnsignedByte() // century
					dateBuilder := kit.NewDateBuilder(nil).
						SetDate(buf.ReadUnsignedByte(), buf.ReadUnsignedByte(), buf.ReadUnsignedByte()).
						SetTime(buf.ReadUnsignedByte(), buf.ReadUnsignedByte(), buf.ReadUnsignedByte())
					pos.FixTime = model.NewTime(dateBuilder.Date())
					pos.SetLatitude(float64(buf.ReadInt()) / 600000.0)
					pos.SetLongitude(float64(buf.ReadInt()) / 600000.0)
					pos.Speed = float64(buf.ReadUnsignedShort())
					pos.Course = float64(buf.ReadUnsignedShort()) / 10.0
					pos.Set(model.KeyOdometer, units.MetersFromFeet(float64(buf.ReadUnsignedInt())))
					pos.Altitude = float64(buf.ReadInt()) / 10.0
				}

				buf.SetReaderIndex(endIndex)

			} else {

				endIndex := buf.IndexOf(buf.ReaderIndex(), buf.WriterIndex(), 0)
				value := buf.ReadString(endIndex - buf.ReaderIndex())

				networkFieldsOffset := 35
				switch typ {
				case 0xE4:
					networkFieldsOffset = 10
				case 0xCB:
					networkFieldsOffset = 80
				case 0x1E:
					networkFieldsOffset = 182
				}
				if field := fieldNumber - networkFieldsOffset; field >= 0 && field < 10 {
					switch field {
					case 0:
						cellTower.MobileCountryCode = ptr(int64(kit.Int(value)))
					case 1:
						cellTower.MobileNetworkCode = ptr(int64(kit.Int(value)))
					case 2:
						cellTower.LocationAreaCode = ptr(int64(kit.Int(value)))
					case 3:
						cellTower.CellID = ptr(kit.Long(value))
					case 4:
						cellTower.SetSignalStrength(kit.Int(value))
					}
				} else {
					switch fieldNumber {
					case 246:
						values := kit.Split(value, ",")
						pos.Set(model.KeyPower, float64(kit.Int(values[2]))/100.0)
						pos.Set(model.KeyBattery, float64(kit.Int(values[3]))/100.0)
					case 255:
						pos.DeviceTime = model.NewTime(time.UnixMilli(kit.Long(value) * 1000))
					}
				}

			}

			buf.ReadUnsignedByte() // delimiter

		}

		if typ == 0xE4 {
			m.LastLocation(pos, deviceTime(pos))
		}

		if cellTower.CellID != nil {
			pos.Network = model.NetworkOf(cellTower)
		}

		if !pos.Valid {
			m.LastLocation(pos, deviceTime(pos))
		}

		return pos

	case '2':
		id := buf.GetString(buf.ReaderIndex(), buf.ReadableBytes()-1)
		m.Session(kit.SubstringFrom(id, len("wli:")))
		return nil
	}

	return nil
}
