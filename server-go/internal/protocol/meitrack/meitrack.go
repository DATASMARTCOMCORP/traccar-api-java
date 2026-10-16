// Package meitrack is the Meitrack protocol (MeitrackProtocol): "$$" text reports, the
// binary CCC and CCE reports and photo transfers, over TCP and UDP.
package meitrack

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: TCP and UDP servers.
func Protocol() ingest.Protocol {
	pipeline := func(framer bool) func() ingest.Pipeline {
		return func() ingest.Pipeline {
			pl := ingest.Pipeline{TextOut: true, Decoder: &Decoder{}, Encoder: ingest.CommandEncoderFunc(encode)}
			if framer {
				pl.Framer = Framer()
			}
			return pl
		}
	}
	return ingest.Protocol{
		Name:         "meitrack",
		DataCommands: protocol.DataCommands("meitrack"),
		Servers:      []ingest.Server{{NewPipeline: pipeline(true)}, {Datagram: true, NewPipeline: pipeline(false)}},
	}
}

// Framer is MeitrackFrameDecoder: the length follows the 3-byte header, up to the first
// comma.
func Framer() ingest.Framer {
	return &ingest.BaseFramer{MaxLength: ingest.MaxFrameLengthLarge, Decode: func(_ *ingest.Message, buf *kit.Buf) any {
		if buf.ReadableBytes() < 10 {
			return nil
		}
		index := buf.IndexOf(buf.ReaderIndex(), buf.WriterIndex(), ',')
		if index != -1 {
			length := index - buf.ReaderIndex() + kit.Int(
				buf.GetLatin1(buf.ReaderIndex()+3, index-buf.ReaderIndex()-3))
			if buf.ReadableBytes() >= length {
				return buf.ReadSlice(length)
			}
		}
		return nil
	}}
}

var pattern = kit.NewPattern().
	Text("$$").Expression("."). // flag
	Number("d+,").              // length
	Number("(d+),").            // imei
	Number("xxx,").             // command
	Number("d+,").Optional().
	Number("(d+),").         // event
	Number("(-?d+.d+),").    // latitude
	Number("(-?d+.d+),").    // longitude
	Number("(dd)(dd)(dd)").  // date (yymmdd)
	Number("(dd)(dd)(dd),"). // time (hhmmss)
	Number("([AV]),").       // validity
	Number("(d+),").         // satellites
	Number("(d+),").         // rssi
	Number("(d+.?d*),").     // speed
	Number("(d+),").         // course
	Number("(d+.?d*),").     // hdop
	Number("(-?d+),").       // altitude
	Number("(d+),").         // odometer
	Number("(d+),").         // runtime
	Number("(d+)|").         // mcc
	Number("(d+)|").         // mnc
	Number("(x+)?|").        // lac
	Number("(x+)?,").        // cid
	Number("(xx)").          // input
	Number("(xx),").         // output
	GroupBegin().
	Number("(d+.d+)|"). // battery
	Number("(d+.d+)|"). // power
	Number("d+.d+|").   // rtc voltage
	Number("d+.d+|").   // mcu voltage
	Number("d+.d+,").   // gps voltage
	Or().
	Number("(x+)?|"). // adc1
	Number("(x+)?|"). // adc2
	Number("(x+)?|"). // adc3
	Number("(x+)|").  // battery
	Number("(x+)?,"). // power
	GroupEnd().
	GroupBegin().
	Expression("([^,]+)?,").Optional(). // event specific
	Expression("[^,]*,").               // reserved
	Number("(d+)?,").                   // protocol
	Number("(x{4})?").                  // fuel
	GroupBegin().
	Number(",(x{6}(?:|x{6})*)?"). // temperature
	GroupBegin().
	Number(",(d+)").        // data count
	Expression(",([^*]*)"). // data
	GroupEndWith("?").
	GroupEndWith("?").
	Or().
	Any().
	GroupEnd().
	Text("*").
	Number("xx").
	Text("\r\n").Optional().
	Compile()

func decodeAlarm(event int) string {
	switch event {
	case 1:
		return model.AlarmSos
	case 17:
		return model.AlarmLowBattery
	case 18:
		return model.AlarmLowPower
	case 19:
		return model.AlarmOverspeed
	case 20:
		return model.AlarmGeofenceEnter
	case 21:
		return model.AlarmGeofenceExit
	case 22:
		return model.AlarmPowerRestored
	case 23:
		return model.AlarmPowerCut
	case 36:
		return model.AlarmTow
	case 44:
		return model.AlarmJamming
	case 78:
		return model.AlarmAccident
	case 90, 91:
		return model.AlarmCornering
	case 129:
		return model.AlarmBraking
	case 130:
		return model.AlarmAcceleration
	case 135:
		return model.AlarmFatigueDriving
	}
	return ""
}

// Decoder is MeitrackProtocolDecoder; it holds the media buffer of a photo transfer.
type Decoder struct {
	media *kit.Buf
}

func decodeRegular(m *ingest.Message, buf *kit.Buf) *model.Position {
	parser := kit.NewParser(pattern, kit.Latin1(buf.Bytes()))
	if !parser.Matches() {
		return nil
	}

	pos := m.NewPosition()

	ds := m.Session(parser.Next())
	if ds == nil {
		return nil
	}
	pos.DeviceID = ds.DeviceID

	event := parser.MustInt()
	pos.Set(model.KeyEvent, event)
	pos.AddAlarm(decodeAlarm(event))

	pos.SetLatitude(parser.MustDouble())
	pos.SetLongitude(parser.MustDouble())

	pos.SetTime(parser.NextDateTimeDefault())

	pos.Valid = parser.Next() == "A"

	pos.Set(model.KeySatellites, parser.NextInt())
	rssi := parser.MustInt()

	pos.Speed = units.KnotsFromKph(parser.MustDouble())
	pos.Course = parser.MustDouble()

	pos.Set(model.KeyHdop, parser.NextDouble())

	pos.Altitude = parser.MustDouble()

	pos.Set(model.KeyOdometer, parser.NextInt())
	pos.Set("runtime", parser.NextNullable())

	mcc := parser.MustInt()
	mnc := parser.MustInt()
	lac := parser.NextHexIntDefault(0)
	cid := parser.NextHexIntDefault(0)
	if mcc != 0 && mnc != 0 {
		pos.Network = model.NetworkOf(model.NewCellTowerRSSI(mcc, mnc, lac, int64(cid), rssi))
	}

	pos.Set(model.KeyInput, parser.NextHexInt())
	pos.Set(model.KeyOutput, parser.NextHexInt())

	if parser.HasNextN(2) {
		pos.Set(model.KeyBattery, parser.NextDouble())
		pos.Set(model.KeyPower, parser.NextDouble())
	} else {
		for i := 1; i <= 3; i++ {
			pos.Set(model.PrefixAdc+strconv.Itoa(i), parser.NextHexInt())
		}
		switch strings.ToUpper(m.DeviceModel(ds)) {
		case "MVT340", "MVT380":
			pos.Set(model.KeyBattery, float64(parser.MustHexInt())*3.0*2.0/1024.0)
			pos.Set(model.KeyPower, float64(parser.NextHexIntDefault(0))*3.0*16.0/1024.0)
		case "MT90":
			pos.Set(model.KeyBattery, float64(parser.MustHexInt())*3.3*2.0/4096.0)
			pos.Set(model.KeyPower, parser.NextHexIntDefault(0))
		case "MT90G":
			pos.Set(model.KeyBattery, float64(parser.MustHexInt())*3.0*2.0/4096.0)
			pos.Set(model.KeyPower, parser.NextHexIntDefault(0))
		case "T1", "T3", "MVT100", "MVT600", "MVT800", "TC68", "TC68S":
			pos.Set(model.KeyBattery, float64(parser.MustHexInt())*3.3*2.0/4096.0)
			pos.Set(model.KeyPower, float64(parser.NextHexIntDefault(0))*3.3*16.0/4096.0)
		default:
			pos.Set(model.KeyBattery, float64(parser.MustHexInt())/100.0)
			pos.Set(model.KeyPower, float64(parser.NextHexIntDefault(0))/100.0)
		}
	}

	if eventData := parser.NextNullable(); eventData != nil && *eventData != "" {
		if event == 37 {
			pos.Set(model.KeyDriverUniqueID, *eventData)
		} else {
			pos.Set("eventData", *eventData)
		}
	}

	protocol := parser.NextIntDefault(0)

	if parser.HasNext() {
		fuel := parser.Next()
		pos.Set(model.KeyFuel,
			float64(kit.IntRadix(kit.Substring(fuel, 0, 2), 16))+float64(kit.IntRadix(kit.SubstringFrom(fuel, 2), 16))/100.0)
	}

	if parser.HasNext() {
		for _, temp := range kit.Split(parser.Next(), "|") {
			index := kit.IntRadix(kit.Substring(temp, 0, 2), 16)
			if protocol >= 3 {
				value := float64(int16(kit.IntRadix(kit.SubstringFrom(temp, 2), 16)))
				pos.Set(model.PrefixTemp+strconv.Itoa(index), value/100.0)
			} else {
				value := float64(parseByteHex(kit.Substring(temp, 2, 4)))
				step := 0.01
				if value < 0 {
					step = -0.01
				}
				value += step * float64(kit.IntRadix(kit.SubstringFrom(temp, 4), 16))
				pos.Set(model.PrefixTemp+strconv.Itoa(index), value)
			}
		}
	}

	if parser.HasNextN(2) {
		parser.NextInt() // count
		decodeDataFields(pos, kit.Split(parser.Next(), ","))
	}

	return pos
}

// parseByteHex is Byte.parseByte(s, 16): out of the byte range throws.
func parseByteHex(s string) int {
	v := kit.IntRadix(s, 16)
	if v < -128 || v > 127 {
		kit.Throw("NumberFormatException: Value out of range. Value:%q Radix:16", s)
	}
	return v
}

func decodeDataFields(pos *model.Position, values []string) {
	if len(values) > 1 && values[1] != "" {
		pos.Set("tempData", values[1])
	}
	if len(values) > 5 && values[5] != "" {
		data := kit.Split(values[5], "|")
		pos.Set("taximeterOn", kit.CharAt(data[0], 1) == '0')
		pos.Set("taximeterStart", data[1])
		if len(data) > 2 {
			pos.Set("taximeterEnd", data[2])
			pos.Set("taximeterDistance", kit.Int(data[3]))
			pos.Set("taximeterFare", kit.Int(data[4]))
			pos.Set("taximeterTrip", data[5])
			pos.Set("taximeterWait", data[6])
		}
	}
}

// epoch2000 is 2000-01-01 in Unix seconds.
const epoch2000 = 946684800

func decodeBinaryC(m *ingest.Message, buf *kit.Buf) any {
	positions := []*model.Position{}

	flag := buf.GetLatin1(2, 1)
	index := buf.IndexOf(buf.ReaderIndex(), buf.WriterIndex(), ',')

	imei := buf.GetLatin1(index+1, 15)
	ds := m.Session(imei)
	if ds == nil {
		return nil
	}

	buf.Skip(index + 1 + 15 + 1 + 3 + 1 + 2 + 2 + 4)

	for buf.ReadableBytes() >= 0x34 {
		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID

		pos.Set(model.KeyEvent, buf.ReadUnsignedByte())

		pos.SetLatitude(float64(buf.ReadIntLE()) / 1000000.0)
		pos.SetLongitude(float64(buf.ReadIntLE()) / 1000000.0)

		pos.SetTime(time.UnixMilli((epoch2000 + buf.ReadUnsignedIntLE()) * 1000))

		pos.Valid = buf.ReadUnsignedByte() == 1

		pos.Set(model.KeySatellites, buf.ReadUnsignedByte())
		rssi := buf.ReadUnsignedByte()

		pos.Speed = units.KnotsFromKph(float64(buf.ReadUnsignedShortLE()))
		pos.Course = float64(buf.ReadUnsignedShortLE())

		pos.Set(model.KeyHdop, float64(buf.ReadUnsignedShortLE())/10.0)

		pos.Altitude = float64(buf.ReadUnsignedShortLE())

		pos.Set(model.KeyOdometer, buf.ReadUnsignedIntLE())
		pos.Set("runtime", buf.ReadUnsignedIntLE())

		mcc, mnc := buf.ReadUnsignedShortLE(), buf.ReadUnsignedShortLE()
		lac, cid := buf.ReadUnsignedShortLE(), buf.ReadUnsignedShortLE()
		pos.Network = model.NetworkOf(model.NewCellTowerRSSI(mcc, mnc, lac, int64(cid), rssi))

		pos.Set(model.KeyStatus, buf.ReadUnsignedShortLE())

		pos.Set(model.PrefixAdc+"1", buf.ReadUnsignedShortLE())
		pos.Set(model.KeyBattery, float64(buf.ReadUnsignedShortLE())/100.0)
		pos.Set(model.KeyPower, buf.ReadUnsignedShortLE())

		buf.ReadUnsignedIntLE() // geo-fence

		positions = append(positions, pos)
	}

	if m.HasChannel() {
		command := "@@" + flag + strconv.Itoa(27+len(positions)/10) + "," +
			imei + ",CCC," + strconv.Itoa(len(positions)) + "*"
		m.Send(command + kit.SumString(command) + "\r\n")
	}

	return positions
}

var dateFolder = regexp.MustCompile(`(\d{4})(\d{2})(\d{2})`)

func decodeBinaryE(m *ingest.Message, buf *kit.Buf) any {
	positions := []*model.Position{}

	buf.SetReaderIndex(buf.IndexOf(buf.ReaderIndex(), buf.WriterIndex(), ',') + 1)
	imei := buf.ReadString(15)
	buf.Skip(1 + 3 + 1)

	ds := m.Session(imei)
	if ds == nil {
		return nil
	}

	buf.ReadUnsignedIntLE() // remaining cache
	count := buf.ReadUnsignedShortLE()

	readID := func() int {
		if buf.GetUnsignedByte(buf.ReaderIndex()) == 0xFE {
			return buf.ReadUnsignedShort()
		}
		return buf.ReadUnsignedByte()
	}

	for i := 0; i < count; i++ {
		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID

		network := model.NewNetwork()

		dataLength := buf.ReadUnsignedShortLE()
		dataEnd := buf.ReaderIndex() + dataLength
		buf.ReadUnsignedShortLE() // index

		paramCount := buf.ReadUnsignedByte()
		for j := 0; j < paramCount; j++ {
			switch readID() {
			case 0x01:
				pos.Set(model.KeyEvent, buf.ReadUnsignedByte())
			case 0x05:
				pos.Valid = buf.ReadUnsignedByte() > 0
			case 0x06:
				pos.Set(model.KeySatellites, buf.ReadUnsignedByte())
			case 0x07:
				pos.Set(model.KeyRssi, buf.ReadUnsignedByte())
			case 0x14:
				pos.Set(model.KeyOutput, buf.ReadUnsignedByte())
			case 0x15:
				pos.Set(model.KeyInput, buf.ReadUnsignedByte())
			case 0x47:
				if lockState := buf.ReadUnsignedByte(); lockState > 0 {
					pos.Set(model.KeyLock, lockState == 2)
				}
			case 0x97:
				pos.Set(model.KeyThrottle, buf.ReadUnsignedByte())
			case 0x9D:
				pos.Set(model.KeyFuel, buf.ReadUnsignedByte())
			case 0xFE69:
				pos.Set(model.KeyBatteryLevel, buf.ReadUnsignedByte())
			default:
				buf.ReadUnsignedByte()
			}
		}

		paramCount = buf.ReadUnsignedByte()
		for j := 0; j < paramCount; j++ {
			switch readID() {
			case 0x08:
				pos.Speed = units.KnotsFromKph(float64(buf.ReadUnsignedShortLE()))
			case 0x09:
				pos.Course = float64(buf.ReadUnsignedShortLE())
			case 0x0A:
				pos.Set(model.KeyHdop, buf.ReadUnsignedShortLE())
			case 0x0B:
				pos.Altitude = float64(buf.ReadShortLE())
			case 0x16:
				pos.Set(model.PrefixAdc+"1", float64(buf.ReadUnsignedShortLE())/100.0)
			case 0x17:
				pos.Set(model.PrefixAdc+"2", float64(buf.ReadUnsignedShortLE())/100.0)
			case 0x19:
				pos.Set(model.KeyBattery, float64(buf.ReadUnsignedShortLE())/100.0)
			case 0x1A:
				pos.Set(model.KeyPower, float64(buf.ReadUnsignedShortLE())/100.0)
			case 0x29:
				pos.Set(model.KeyFuel, float64(buf.ReadUnsignedShortLE())/100.0)
			case 0x40:
				pos.Set(model.KeyEvent, buf.ReadUnsignedShortLE())
			case 0x91, 0x92:
				pos.Set(model.KeyObdSpeed, buf.ReadUnsignedShortLE())
			case 0x98:
				pos.Set(model.KeyFuelUsed, buf.ReadUnsignedShortLE())
			case 0x99:
				pos.Set(model.KeyRpm, buf.ReadUnsignedShortLE())
			case 0x9C:
				pos.Set(model.KeyCoolantTemp, buf.ReadUnsignedShortLE())
			case 0x9F:
				pos.Set(model.PrefixTemp+"1", buf.ReadUnsignedShortLE())
			case 0xC9:
				pos.Set(model.KeyFuelConsumption, buf.ReadUnsignedShortLE())
			default:
				buf.ReadUnsignedShortLE()
			}
		}

		paramCount = buf.ReadUnsignedByte()
		for j := 0; j < paramCount; j++ {
			switch readID() {
			case 0x02:
				pos.SetLatitude(float64(buf.ReadIntLE()) / 1000000.0)
			case 0x03:
				pos.SetLongitude(float64(buf.ReadIntLE()) / 1000000.0)
			case 0x04:
				pos.SetTime(time.UnixMilli((epoch2000 + buf.ReadUnsignedIntLE()) * 1000))
			case 0x0C:
				pos.Set(model.KeyOdometer, buf.ReadUnsignedIntLE())
			case 0x0D:
				pos.Set("runtime", buf.ReadUnsignedIntLE())
			case 0x25:
				pos.Set(model.KeyDriverUniqueID, strconv.FormatInt(buf.ReadUnsignedIntLE(), 10))
			case 0x9B:
				pos.Set(model.KeyObdOdometer, buf.ReadUnsignedIntLE())
			case 0xA0:
				pos.Set(model.KeyFuelUsed, float64(buf.ReadUnsignedIntLE())/1000.0)
			case 0xA2:
				pos.Set(model.KeyFuelConsumption, float64(buf.ReadUnsignedIntLE())/100.0)
			case 0xFEF4:
				pos.Set(model.KeyHours, buf.ReadUnsignedIntLE()*60000)
			default:
				buf.ReadUnsignedIntLE()
			}
		}

		paramCount = buf.ReadUnsignedByte()
		for j := 0; j < paramCount; j++ {
			id := readID()
			length := buf.ReadUnsignedByte()
			switch id {
			case 0x1D, 0x1E, 0x1F, 0x20, 0x21, 0x22, 0x23, 0x24, 0x25:
				mac := buf.ReadSlice(6).HexDump()
				var b strings.Builder
				for k := 0; k < len(mac); k += 2 {
					if k > 0 {
						b.WriteByte(':')
					}
					b.WriteString(mac[k : k+2])
				}
				network.AddWifiAccessPoint(model.NewWifiAccessPoint(b.String(), buf.ReadShortLE()))
			case 0x0E, 0x0F, 0x10, 0x12, 0x13:
				mcc, mnc, lac := buf.ReadUnsignedShortLE(), buf.ReadUnsignedShortLE(), buf.ReadUnsignedShortLE()
				cid := buf.ReadUnsignedIntLE()
				network.AddCellTower(model.NewCellTowerRSSI(mcc, mnc, lac, cid, buf.ReadShortLE()))
			case 0x2A, 0x2B, 0x2C, 0x2D, 0x2E, 0x2F, 0x30, 0x31:
				buf.ReadUnsignedByte() // label
				pos.Set(model.PrefixTemp+strconv.Itoa(id-0x2A), float64(buf.ReadShortLE())/100.0)
			case 0x4B:
				buf.Skip(length) // network information
			case 0xFE31:
				alarmProtocol := buf.ReadUnsignedByte()
				pos.Set("alarmType", buf.ReadUnsignedByte())
				if alarmProtocol == 0x02 && length > 3 {
					file := buf.ReadString(length - 2)
					folder := dateFolder.ReplaceAllString(kit.Substring(file, 0, 8), "$1-$2-$3")
					pos.Set(model.KeyImage, folder+"/"+file)
				} else {
					buf.Skip(length - 2)
				}
			case 0xFE73:
				buf.ReadUnsignedByte() // version
				pos.Set("tagName", buf.ReadString(buf.ReadUnsignedByte()))
				buf.Skip(6) // mac
				pos.Set("tagBattery", buf.ReadUnsignedByte())
				pos.Set("tagTemp", float64(buf.ReadShortLE())/256.0)
				pos.Set("tagHumidity", float64(buf.ReadShortLE())/256.0)
				buf.ReadUnsignedShortLE() // high temperature threshold
				buf.ReadUnsignedShortLE() // low temperature threshold
				buf.ReadUnsignedShortLE() // high humidity threshold
				buf.ReadUnsignedShortLE() // low humidity threshold
			case 0xFEA8:
				for k := 1; k <= 3; k++ {
					if buf.ReadUnsignedByte() > 0 {
						key := model.KeyBatteryLevel
						if k != 1 {
							key = "battery" + strconv.Itoa(k) + "Level"
						}
						pos.Set(key, buf.ReadUnsignedByte())
					} else {
						buf.ReadUnsignedByte()
					}
				}
				buf.ReadUnsignedByte() // battery alert
			default:
				buf.Skip(length)
			}
		}

		buf.SetReaderIndex(dataEnd)

		if network.CellTowers != nil || network.WifiAccessPoints != nil {
			pos.Network = network
		}
		positions = append(positions, pos)
	}

	return positions
}

func requestPhotoPacket(m *ingest.Message, imei, file string, index int) {
	if m.HasChannel() {
		content := "D00," + file + "," + strconv.Itoa(index)
		length := 1 + kit.Len(imei) + 1 + kit.Len(content) + 5
		response := kit.Format("@@O%02d,%s,%s*", length, imei, content)
		m.Send(response + kit.SumString(response) + "\r\n")
	}
}

func (d *Decoder) Decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	index := buf.IndexOf(buf.ReaderIndex(), buf.WriterIndex(), ',')
	imei := buf.GetLatin1(index+1, 15)
	index = buf.IndexOf(index+1, buf.WriterIndex(), ',')
	typ := buf.GetLatin1(index+1, 3)

	switch typ {
	case "AAC":
		if m.HasChannel() {
			response := kit.Format("@@z27,%s,AAC,1*", imei)
			m.Send(response + kit.SumString(response) + "\r\n")
		}
		return nil
	case "D00":
		if d.media == nil {
			d.media = m.NewMediaBuffer()
		}

		index = index + 1 + kit.Len(typ) + 1
		end := buf.IndexOf(index, buf.WriterIndex(), ',')
		file := buf.GetLatin1(index, end-index)
		index = end + 1
		end = buf.IndexOf(index, buf.WriterIndex(), ',')
		total := kit.Int(buf.GetLatin1(index, end-index))
		index = end + 1
		end = buf.IndexOf(index, buf.WriterIndex(), ',')
		current := kit.Int(buf.GetLatin1(index, end-index))

		buf.SetReaderIndex(end + 1)
		d.media.WriteBytes(buf.ReadBytes(buf.ReadableBytes() - 1 - 2 - 2))

		if current == total-1 {
			pos := m.NewPosition()
			ds := m.Session(imei)
			if ds == nil {
				kit.Throw("NullPointerException: unknown device")
			}
			pos.DeviceID = ds.DeviceID
			m.LastLocation(pos, nil)
			pos.Set(model.KeyImage, m.WriteMedia(imei, "jpg", d.media.Bytes()))
			d.media = nil
			return pos
		}
		if (current+1)%8 == 0 {
			requestPhotoPacket(m, imei, file, current+1)
		}
		return nil
	case "D03":
		d.media = m.NewMediaBuffer()
		requestPhotoPacket(m, imei, "camera_picture.jpg", 0)
		return nil
	case "D82":
		pos := m.NewPosition()
		ds := m.Session(imei)
		if ds == nil {
			kit.Throw("NullPointerException: unknown device")
		}
		pos.DeviceID = ds.DeviceID
		m.LastLocation(pos, nil)
		pos.Set(model.KeyResult, buf.GetLatin1(index+1, buf.WriterIndex()-index-4))
		return pos
	case "CCC":
		return decodeBinaryC(m, buf)
	case "CCE":
		return decodeBinaryE(m, buf)
	}
	if pos := decodeRegular(m, buf); pos != nil {
		return pos
	}
	return nil
}

func formatCommand(m *ingest.Message, c *model.Command, content *string) string {
	if content == nil {
		kit.Throw("NullPointerException: command content")
	}
	uniqueID := m.UniqueID(c.DeviceID)
	length := 1 + kit.Len(uniqueID) + 1 + kit.Len(*content) + 5
	result := kit.Format("@@A%02d,%s,%s*", length, uniqueID, *content)
	return result + kit.SumString(result) + "\r\n"
}

func encode(m *ingest.Message, c *model.Command) any {
	alt := m.LookupBool(c.DeviceID, m.Protocol()+".alternative", false, true)
	alternative := alt != nil && *alt
	text := func(s string) *string { return &s }
	pick := func(a, b string) *string {
		if alternative {
			return &a
		}
		return &b
	}
	switch c.Type {
	case model.CommandCustom:
		return formatCommand(m, c, kit.CommandString(c, model.CommandKeyData))
	case model.CommandPositionSingle:
		return formatCommand(m, c, text("A10"))
	case model.CommandEngineStop:
		return formatCommand(m, c, text("C01,0,12222"))
	case model.CommandEngineResume:
		return formatCommand(m, c, text("C01,0,02222"))
	case model.CommandAlarmArm:
		return formatCommand(m, c, pick("B21,1", "C01,0,22122"))
	case model.CommandAlarmDisarm:
		return formatCommand(m, c, pick("B21,0", "C01,0,22022"))
	case model.CommandRequestPhoto:
		index := kit.CommandInt(c, model.CommandKeyIndex)
		if index <= 0 {
			index = 1
		}
		return formatCommand(m, c, text("D03,"+strconv.Itoa(index)+",camera_picture.jpg"))
	case model.CommandSendSms:
		return formatCommand(m, c, text("C02,0,"+javaString(kit.CommandString(c, model.CommandKeyPhone))+
			","+javaString(kit.CommandString(c, model.CommandKeyMessage))))
	}
	return nil
}

// javaString is a nullable string as Java concatenates it.
func javaString(s *string) string {
	if s == nil {
		return "null"
	}
	return *s
}
