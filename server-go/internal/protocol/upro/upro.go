// Package upro is the Upro protocol (UproProtocol): "*HQ20..." messages with
// '&'-separated data items, delimited by '#', over TCP.
package upro

import (
	"strings"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "upro",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.NewDelimiterFramer(ingest.MaxFrameLength, "#"),
				TextOut: true,
				Decoder: ingest.DecoderFunc(decode),
			}
		}}},
	}
}

var patternHeader = kit.NewPattern().
	Text("*").
	Expression("(..20)"). // head
	Expression("([01])"). // ack
	Number("(d+),").      // device id
	Expression("(.)").    // type
	Expression("(.)").    // subtype
	Any().
	Compile()

var patternLocation = kit.NewPattern().
	Number("(dd)(dd)(dd)").    // time (hhmmss)
	Number("(dd)(dd)(dddd)").  // latitude
	Number("(ddd)(dd)(dddd)"). // longitude
	Number("(d)").             // flags
	Number("(dd)").            // speed
	Number("(dd)").            // course
	Number("(dd)(dd)(dd)").    // date (ddmmyy)
	Compile()

func decodeLocation(pos *model.Position, data string) {
	parser := kit.NewParser(patternLocation, data)
	if !parser.Matches() {
		return
	}
	b := kit.NewDateBuilder(nil)
	b.SetTime(parser.NextIntDefault(0), parser.NextIntDefault(0), parser.NextIntDefault(0))

	pos.SetLatitude(parser.NextCoordinate(kit.DegMinMin))
	pos.SetLongitude(parser.NextCoordinate(kit.DegMinMin))

	flags := int64(parser.NextIntDefault(0))
	pos.Valid = !kit.Check(flags, 0)
	if !kit.Check(flags, 1) {
		pos.SetLatitude(-pos.Latitude)
	}
	if !kit.Check(flags, 2) {
		pos.SetLongitude(-pos.Longitude)
	}

	pos.Speed = float64(parser.NextIntDefault(0) * 2)
	pos.Course = float64(parser.NextIntDefault(0) * 10)

	b.SetDateReverse(parser.NextIntDefault(0), parser.NextIntDefault(0), parser.NextIntDefault(0))
	pos.SetTime(b.Date())
}

func decodeTransparent(buf *kit.Buf, pos *model.Position) {
	dataType := buf.ReadUnsignedShort()
	dataLength := buf.ReadUnsignedShort()
	if dataType == 0x4009 {
		pos.Set(model.KeyDriverUniqueID, buf.ReadString(dataLength))
	} else {
		pos.Set("data", buf.ReadSlice(dataLength).HexDump())
	}
}

func alarmIf(condition bool, alarm string) string {
	if condition {
		return alarm
	}
	return ""
}

func digits(s string, from, n int) []int64 {
	d := make([]int64, n)
	for i := range n {
		d[i] = int64(kit.CharAt(s, from+i) - '0')
	}
	return d
}

func text(b *kit.Buf) string { return kit.Latin1(b.Bytes()) }

func decodeRegular(head string, buf *kit.Buf, pos *model.Position) {
	network := model.NewNetwork()

	for buf.ReadableBytes() > 1 {
		buf.ReadSignedByte() // skip delimiter

		dataType := buf.ReadSignedByte()

		delimiterIndex := buf.IndexOf(buf.ReaderIndex(), buf.WriterIndex(), '&')
		if delimiterIndex < 0 {
			delimiterIndex = buf.IndexOf(buf.ReaderIndex(), buf.WriterIndex(), '#')
			if delimiterIndex < 0 {
				delimiterIndex = buf.WriterIndex()
			}
		}

		data := buf.ReadSlice(delimiterIndex - buf.ReaderIndex())
		var mcc, mnc int

		switch dataType {
		case 'A':
			decodeLocation(pos, text(data))
		case 'B':
			status := text(data)
			s := digits(status, 0, 5)
			pos.Set(model.KeyIgnition, kit.Check(s[1], 0))
			a := digits(status, 5, 5)
			pos.AddAlarm(alarmIf(kit.Check(a[0], 0), model.AlarmSos))
			pos.AddAlarm(alarmIf(kit.Check(a[0], 2), model.AlarmVibration))
			pos.AddAlarm(alarmIf(kit.Check(a[0], 3), model.AlarmMovement))
			pos.AddAlarm(alarmIf(kit.Check(a[1], 2), model.AlarmOverspeed))
			pos.AddAlarm(alarmIf(kit.Check(a[3], 1), model.AlarmTampering))
			pos.AddAlarm(alarmIf(kit.Check(a[3], 2), model.AlarmLowPower))
			pos.Set(model.KeyStatus, status)
		case 'C':
			var odometer int64
			for data.IsReadable() {
				odometer <<= 4
				odometer += int64(data.ReadSignedByte()) - '0'
			}
			pos.Set(model.KeyOdometer, odometer*2*1852/3600)
		case 'F':
			pos.Speed = float64(kit.Int(data.ReadString(4))) / 10.0
		case 'G':
			pos.Altitude = float64(kit.Int(data.ReadString(6))) / 10.0
		case 'I':
			stringValue := text(data)
			count := kit.Int(kit.Substring(stringValue, 0, 1))
			if kit.Len(stringValue) == 6+count*10 {
				mcc = kit.Int(kit.Substring(stringValue, 1, 4))
				mnc = kit.Int(kit.Substring(stringValue, 4, 6))
				for i := range count {
					offset := 6 + i*10
					network.AddCellTower(model.NewCellTowerRSSI(mcc, mnc,
						kit.IntRadix(kit.Substring(stringValue, offset, offset+4), 16),
						int64(kit.IntRadix(kit.Substring(stringValue, offset+4, offset+8), 16)),
						kit.Int(kit.Substring(stringValue, offset+8, offset+10))))
				}
			}
		case 'J':
			if data.ReadableBytes() == 6 {
				index := rune(data.ReadUnsignedByte())
				status := int64(data.ReadUnsignedByte())
				value := float64(kit.Int(data.ReadString(4))) / 10.0
				if kit.Check(status, 0) {
					value = -value
				}
				pos.Set(model.PrefixTemp+string(index), value)
			}
		case 'K':
			pos.Set("statusExtended", text(data))
		case 'M':
			if data.ReadableBytes() == 3 {
				pos.Set(model.KeyBatteryLevel, float64(kit.Int(data.ReadString(3)))/10.0)
			} else if data.ReadableBytes() == 4 {
				index := rune(data.ReadUnsignedByte())
				data.ReadUnsignedByte() // status
				pos.Set("humidity"+string(index), kit.Int(data.ReadString(2)))
			}
		case 'N':
			pos.Set(model.KeyRssi, kit.Int(data.ReadString(2)))
		case 'O':
			pos.Set(model.KeySatellites, kit.Int(data.ReadString(2)))
		case 'P':
			if data.ReadableBytes() >= 16 {
				mcc := kit.Int(data.ReadString(4))
				mnc := kit.Int(data.ReadString(4))
				lac := kit.IntRadix(data.ReadString(4), 16)
				cid := kit.IntRadix(data.ReadString(4), 16)
				pos.Network = model.NetworkOf(model.NewCellTower(mcc, mnc, lac, int64(cid)))
			}
		case 'Q':
			if !strings.HasPrefix(head, "HQ") {
				pos.Set("obdPid", data.HexDump())
			}
		case 'R':
			if strings.HasPrefix(head, "HQ") {
				pos.Set(model.KeyRssi, kit.Int(data.ReadString(2)))
				pos.Set(model.KeySatellites, kit.Int(data.ReadString(2)))
			} else {
				pos.Set("odbTravel", data.HexDump())
			}
		case 'S':
			pos.Set("obdTraffic", data.HexDump())
		case 'T':
			if data.ReadableBytes() == 2 {
				pos.Set(model.KeyBatteryLevel, kit.Int(text(data)))
			}
		case 'V':
			pos.Set(model.KeyPower, float64(kit.Int(data.ReadString(4)))/10.0)
		case 'W':
			alarms := int64(kit.Int(data.ReadString(2)))
			pos.AddAlarm(alarmIf(kit.Check(alarms, 2), model.AlarmTampering))
		case 'X':
			stringData := text(data)
			if strings.HasPrefix(stringData, "(") {
				parameters := kit.Split(kit.Substring(stringData, 1, kit.Len(stringData)-1), ")(")
				for _, parameter := range parameters {
					typ := kit.CharAt(parameter, 0)
					value := kit.SubstringFrom(parameter, 1)
					switch typ {
					case 'J':
						pos.Set("timezone", value)
					case 'k':
						pos.Set(model.KeyIccid, value)
					}
				}
			} else {
				cells := kit.Split(stringData, ";")
				for i, cell := range cells {
					values := kit.Split(cell, ",")
					index := 0
					if i == 0 {
						mcc = kit.Int(values[index])
						index++
						mnc = kit.Int(values[index])
						index++
					}
					lac := kit.Int(values[index])
					cid := kit.Int(values[index+1])
					network.AddCellTower(model.NewCellTowerRSSI(mcc, mnc, lac, int64(cid), kit.Int(values[index+2])))
				}
				pos.Network = network
			}
		case 'Y':
			stringValue := text(data)
			count := kit.Int(kit.Substring(stringValue, 0, 1))
			if kit.Len(stringValue) == 6+count*14 {
				mcc = kit.Int(kit.Substring(stringValue, 1, 4))
				mnc = kit.Int(kit.Substring(stringValue, 4, 6))
				for i := range count {
					offset := 6 + i*14
					network.AddCellTower(model.NewCellTowerRSSI(mcc, mnc,
						kit.IntRadix(kit.Substring(stringValue, offset, offset+4), 16),
						kit.LongRadix(kit.Substring(stringValue, offset+4, offset+12), 16),
						kit.IntRadix(kit.Substring(stringValue, offset+12, offset+14), 16)))
				}
			} else {
				pos.Set(model.KeyPower, float64(kit.Int(data.ReadString(5)))/1000.0)
			}
		case 'b':
			if data.ReadableBytes() > 3 {
				pos.Set("serial", kit.SubstringFrom(text(data), 3))
			}
		case 'd':
			pos.Set(model.PrefixAdc+"1", float64(kit.Int(text(data)))/100.0)
		case 'l':
			s := digits(text(data), 0, 4)
			pos.Set("logisticsLock", int(kit.To(s[0], 3)))
		}
	}

	if network.CellTowers != nil || network.WifiAccessPoints != nil {
		pos.Network = network
	}
}

func isLetterOrDigit(b int8) bool {
	return b >= '0' && b <= '9' || b >= 'A' && b <= 'Z' || b >= 'a' && b <= 'z'
}

func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	if buf.GetByte(buf.ReaderIndex()) != '*' {
		return nil
	}

	headerIndex := buf.IndexOf(buf.ReaderIndex(), buf.WriterIndex(), ',') + 1
	for headerIndex < buf.WriterIndex() && isLetterOrDigit(buf.GetByte(headerIndex)) {
		headerIndex++
	}
	header := text(buf.ReadSlice(headerIndex - buf.ReaderIndex()))

	parser := kit.NewParser(patternHeader, header)
	if !parser.Matches() {
		return nil
	}

	head := parser.Next()
	reply := parser.Next() == "1"

	ds := m.Session(parser.Next())
	if ds == nil {
		return nil
	}

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	typ := parser.Next()
	subtype := parser.Next()

	if reply {
		m.Send("*" + head + "Y" + typ + subtype + "#")
	}

	if typ == "G" && subtype == "Y" {
		decodeTransparent(buf, pos)
	} else {
		decodeRegular(head, buf, pos)
	}

	if pos.Latitude == 0 || pos.Longitude == 0 {
		if pos.Attributes.Len() == 0 {
			return nil
		}
		if pos.DeviceTime.IsZero() {
			m.LastLocation(pos, nil)
		} else {
			t := pos.DeviceTime.Time
			m.LastLocation(pos, &t)
		}
	}

	return pos
}
