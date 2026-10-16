// Package fifotrack is the Fifotrack protocol (FifotrackProtocol): "$$<length>,..." text
// reports, command results and photo transfers over TCP.
package fifotrack

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name:         "fifotrack",
		DataCommands: protocol.DataCommands("fifotrack"),
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

// Framer is FifotrackFrameDecoder: the length after "$$" counts from the first comma, and
// a two-byte delimiter follows.
func Framer() ingest.Framer {
	return &ingest.BaseFramer{MaxLength: ingest.MaxFrameLengthLarge, Decode: func(_ *ingest.Message, buf *kit.Buf) any {
		if buf.ReadableBytes() < 10 {
			return nil
		}
		index := buf.IndexOf(buf.ReaderIndex(), buf.WriterIndex(), ',')
		if index != -1 {
			r := buf.ReaderIndex()
			length := index - r + 3 + kit.Int(buf.GetLatin1(r+2, index-r-2))
			if buf.ReadableBytes() >= length+2 {
				frame := buf.ReadSlice(length)
				buf.Skip(2) // delimiter
				return frame
			}
		}
		return nil
	}}
}

const dateFormat = "060102150405" // yyMMddHHmmss, UTC

var pattern = kit.NewPattern().
	Text("$$").
	Number("d+,").           // length
	Number("(d+),").         // imei
	Number("x+,").           // index
	Expression("[^,]+,").    // type
	Number("(d+)?,").        // alarm
	Number("(dd)(dd)(dd)").  // date (yymmdd)
	Number("(dd)(dd)(dd),"). // time (hhmmss)
	Number("([AV]),").       // validity
	Number("(-?d+.d+),").    // latitude
	Number("(-?d+.d+),").    // longitude
	Number("(d+),").         // speed
	Number("(d+),").         // course
	Number("(-?d+),").       // altitude
	Number("(d+),").         // odometer
	Number("(d+),").         // engine hours
	Number("(x+),").         // status
	Number("(x+)?,").        // input
	Number("(x+)?,").        // output
	Number("(d+)|").         // mcc
	Number("(d+)|").         // mnc
	Number("(x+)|").         // lac
	Number("(x+),").         // cid
	// adc: Java's number("([x|]+)") is the class union [[0-9a-fA-F]|], which RE2 (and
	// kit's translation) can't express as written: flattened here.
	Expression("([0-9a-fA-F|]+)").
	Expression(",([^,]+)").              // rfid
	Expression(",([^*]*)").OptionalN(2). // sensors
	Any().
	Compile()

var patternNew = kit.NewPattern().
	Text("$$").
	Number("d+,").           // length
	Number("(d+),").         // imei
	Number("(x+),").         // index
	Text("A03,").            // type
	Number("(d+)?,").        // alarm
	Number("(dd)(dd)(dd)").  // date (yymmdd)
	Number("(dd)(dd)(dd),"). // time (hhmmss)
	Number("(d+)|").         // mcc
	Number("(d+)|").         // mnc
	Number("(x+)|").         // lac
	Number("(x+),").         // cid
	Number("(d+.d+),").      // battery
	Number("(d+),").         // battery level
	Number("(x+),").         // status
	GroupBegin().
	Text("0,").           // gps location
	Number("([AV]),").    // validity
	Number("(d+),").      // speed
	Number("(d+),").      // satellites
	Number("(-?d+.d+),"). // latitude
	Number("(-?d+.d+)").  // longitude
	Or().
	Text("1,").            // wifi location
	Expression("([^*]+)"). // wifi
	GroupEnd().
	Text("*").
	Number("xx"). // checksum
	Compile()

var patternPhoto = kit.NewPattern().
	Text("$$").
	Number("d+,").   // length
	Number("(d+),"). // imei
	Any().
	Number(",(d+),").      // length
	Expression("([^*]+)"). // photo id
	Text("*").
	Number("xx").
	Compile()

var patternPhotoData = kit.NewPattern().
	Text("$$").
	Number("d+,").          // length
	Number("(d+),").        // imei
	Number("x+,").          // index
	Expression("[^,]+,").   // type
	Expression("([^,]+),"). // photo id
	Number("(d+),").        // offset
	Number("(d+),").        // size
	Compile()

var patternResult = kit.NewPattern().
	Text("$$").
	Number("d+,").   // length
	Number("(d+),"). // imei
	Any().
	Expression(",([A-Z]+)"). // result
	Text("*").
	Number("xx").
	Compile()

var xdigits = regexp.MustCompile(`^[0-9a-fA-F]+$`) // \p{XDigit}+

var pairs = regexp.MustCompile(`(..)`)

// Decoder is FifotrackProtocolDecoder: it holds the photo being transferred.
type Decoder struct {
	photo *photoBuffer
}

// photoBuffer is the decoder's media buffer (Unpooled.buffer(length, media.bufferSize)):
// the bytes plus the capacity Netty would give the buffer, which decides when the photo
// is complete.
type photoBuffer struct {
	data        []byte
	capacity    int
	maxCapacity int
}

func newPhotoBuffer(m *ingest.Message, length int) *photoBuffer {
	maxCapacity, err := m.Config().Int("media.bufferSize", 32*1024*1024)
	if err != nil {
		panic(err)
	}
	if length < 0 || length > maxCapacity {
		kit.Throw("IllegalArgumentException: initialCapacity: %d (expected: 0-maxCapacity(%d))", length, maxCapacity)
	}
	return &photoBuffer{capacity: length, maxCapacity: maxCapacity}
}

func (p *photoBuffer) writableBytes() int { return p.capacity - len(p.data) }

// write is writeBytes with ByteBuf.ensureWritable: the capacity grows as
// AbstractByteBufAllocator.calculateNewCapacity does.
func (p *photoBuffer) write(b []byte) {
	minCapacity := len(p.data) + len(b)
	if minCapacity > p.capacity {
		if minCapacity > p.maxCapacity {
			kit.Throw("IndexOutOfBoundsException: writerIndex(%d) + minWritableBytes(%d) exceeds maxCapacity(%d)", len(p.data), len(b), p.maxCapacity)
		}
		p.capacity = calculateNewCapacity(minCapacity, p.maxCapacity)
	}
	p.data = append(p.data, b...)
}

func calculateNewCapacity(minNewCapacity, maxCapacity int) int {
	const threshold = 4 * 1024 * 1024
	if minNewCapacity == threshold {
		return threshold
	}
	if minNewCapacity > threshold {
		newCapacity := minNewCapacity / threshold * threshold
		if newCapacity > maxCapacity-threshold {
			return maxCapacity
		}
		return newCapacity + threshold
	}
	newCapacity := 64
	for newCapacity < minNewCapacity {
		newCapacity <<= 1
	}
	return min(newCapacity, maxCapacity)
}

func sendResponse(m *ingest.Message, imei, content string) {
	length := 1 + kit.Len(imei) + 1 + kit.Len(content)
	response := kit.Format("##%02d,%s,%s*", length, imei, content)
	m.Send(response + kit.SumString(response) + "\r\n")
}

func (d *Decoder) requestPhoto(m *ingest.Message, imei, file string) {
	content := "1,D06," + file + "," + strconv.Itoa(len(d.photo.data)) + "," + strconv.Itoa(min(1024, d.photo.writableBytes()))
	sendResponse(m, imei, content)
}

func decodeAlarm(alarm *int) string {
	if alarm == nil {
		return ""
	}
	switch *alarm {
	case 2:
		return model.AlarmSos
	case 14:
		return model.AlarmLowPower
	case 15:
		return model.AlarmPowerCut
	case 16:
		return model.AlarmPowerRestored
	case 17:
		return model.AlarmLowBattery
	case 18:
		return model.AlarmOverspeed
	case 20:
		return model.AlarmGpsAntennaCut
	case 21:
		return model.AlarmVibration
	case 23:
		return model.AlarmAcceleration
	case 24:
		return model.AlarmBraking
	case 27:
		return model.AlarmFatigueDriving
	case 30, 32:
		return model.AlarmJamming
	case 31:
		return model.AlarmFallDown
	case 33:
		return model.AlarmGeofenceExit
	case 34:
		return model.AlarmGeofenceEnter
	case 35:
		return model.AlarmIdle
	case 40, 41:
		return model.AlarmTemperature
	case 53:
		return model.AlarmPowerOn
	case 54:
		return model.AlarmPowerOff
	}
	return ""
}

func decodeLocationNew(m *ingest.Message, sentence string) any {
	parser := kit.NewParser(patternNew, sentence)
	if !parser.Matches() {
		return nil
	}
	imei := parser.Next()
	ds := m.Session(imei)
	if ds == nil {
		return nil
	}
	index := parser.Next()

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	pos.AddAlarm(decodeAlarm(parser.NextInt()))
	pos.DeviceTime = model.NewTime(parser.NextDateTimeDefault())

	network := model.NewNetwork()
	mcc, mnc := parser.MustInt(), parser.MustInt()
	lac, cid := parser.MustHexInt(), parser.MustHexInt()
	network.AddCellTower(model.NewCellTower(mcc, mnc, lac, int64(cid)))

	pos.Set(model.KeyBattery, parser.NextDouble())
	pos.Set(model.KeyBatteryLevel, parser.NextInt())
	pos.Set(model.KeyStatus, parser.NextHexInt())

	if parser.HasNextN(5) {
		pos.Valid = parser.Next() == "A"
		pos.FixTime = pos.DeviceTime
		pos.Speed = units.KnotsFromKph(float64(parser.MustInt()))
		pos.Set(model.KeySatellites, parser.NextInt())
		pos.SetLatitude(parser.MustDouble())
		pos.SetLongitude(parser.MustDouble())
	} else {
		deviceTime := pos.DeviceTime.Time
		m.LastLocation(pos, &deviceTime)
		for _, point := range kit.Split(parser.Next(), "|") {
			wifi := kit.Split(point, ":")
			mac := pairs.ReplaceAllString(wifi[0], "$1:")
			network.AddWifiAccessPoint(model.NewWifiAccessPoint(kit.Substring(mac, 0, kit.Len(mac)-1), kit.Int(wifi[1])))
		}
	}

	pos.Network = network

	sendResponse(m, imei, index+",A03,"+m.Now.UTC().Format(dateFormat))
	return pos
}

func decodeLocation(m *ingest.Message, sentence string) any {
	parser := kit.NewParser(pattern, sentence)
	if !parser.Matches() {
		return nil
	}
	ds := m.Session(parser.Next())
	if ds == nil {
		return nil
	}
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	pos.AddAlarm(decodeAlarm(parser.NextInt()))
	pos.SetTime(parser.NextDateTimeDefault())

	pos.Valid = parser.Next() == "A"
	pos.SetLatitude(parser.MustDouble())
	pos.SetLongitude(parser.MustDouble())
	pos.Speed = units.KnotsFromKph(float64(parser.MustInt()))
	pos.Course = float64(parser.MustInt())
	pos.Altitude = float64(parser.MustInt())

	pos.Set(model.KeyOdometer, parser.NextLong())
	pos.Set(model.KeyHours, parser.MustLong()*1000)

	status := parser.MustHexLong()
	pos.Set(model.KeyRssi, kit.Between(status, 3, 8))
	pos.Set(model.KeySatellites, kit.From(status, 28))
	pos.Set(model.KeyStatus, status)

	pos.Set(model.KeyInput, parser.NextHexInt())
	pos.Set(model.KeyOutput, parser.NextHexInt())

	mcc, mnc := parser.MustInt(), parser.MustInt()
	lac, cid := parser.MustHexInt(), parser.MustHexInt()
	pos.Network = model.NetworkOf(model.NewCellTower(mcc, mnc, lac, int64(cid)))

	for i, adc := range kit.Split(parser.Next(), "|") {
		pos.Set(model.PrefixAdc+strconv.Itoa(i+1), kit.IntRadix(adc, 16))
	}

	if parser.HasNext() {
		value := parser.Next()
		if xdigits.MatchString(value) {
			pos.Set(model.KeyDriverUniqueID, strconv.Itoa(kit.IntRadix(value, 16)))
		} else {
			pos.Set(model.KeyCard, value)
		}
	}

	if parser.HasNext() {
		for i, sensor := range kit.Split(parser.Next(), "|") {
			pos.Set(model.PrefixIo+strconv.Itoa(i+1), sensor)
		}
	}
	return pos
}

func decodeResult(m *ingest.Message, sentence string) any {
	parser := kit.NewParser(patternResult, sentence)
	if !parser.Matches() {
		return nil
	}
	ds := m.Session(parser.Next())
	if ds == nil {
		return nil
	}
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID
	pos.Set(model.KeyResult, parser.Next())
	return pos
}

func (d *Decoder) Decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)
	typeIndex := buf.IndexOf(buf.ReaderIndex(), buf.WriterIndex(), ',') + 1
	typeIndex = buf.IndexOf(typeIndex, buf.WriterIndex(), ',') + 1
	typeIndex = buf.IndexOf(typeIndex, buf.WriterIndex(), ',') + 1
	typ := buf.GetLatin1(typeIndex, 3)

	switch {
	case strings.HasPrefix(typ, "B"):
		return decodeResult(m, kit.Latin1(buf.Bytes()))

	case typ == "D05":
		parser := kit.NewParser(patternPhoto, kit.Latin1(buf.Bytes()))
		if parser.Matches() {
			imei := parser.Next()
			length := parser.MustInt()
			photoID := parser.Next()
			d.photo = newPhotoBuffer(m, length)
			d.requestPhoto(m, imei, photoID)
		}

	case typ == "D06":
		if d.photo == nil {
			return nil
		}
		dataIndex := buf.IndexOf(typeIndex+4, buf.WriterIndex(), ',') + 1
		dataIndex = buf.IndexOf(dataIndex, buf.WriterIndex(), ',') + 1
		dataIndex = buf.IndexOf(dataIndex, buf.WriterIndex(), ',') + 1
		parser := kit.NewParser(patternPhotoData, buf.GetLatin1(buf.ReaderIndex(), dataIndex))
		if parser.Matches() {
			imei := parser.Next()
			photoID := parser.Next()
			parser.MustInt() // offset
			parser.MustInt() // size
			buf.SetReaderIndex(dataIndex)
			d.photo.write(buf.ReadBytes(buf.ReadableBytes() - 3)) // ignore checksum
			if d.photo.writableBytes() > 0 {
				d.requestPhoto(m, imei, photoID)
			} else {
				pos := m.NewPosition()
				ds := m.Session(imei)
				if ds == nil {
					kit.Throw("NullPointerException: device session")
				}
				pos.DeviceID = ds.DeviceID
				m.LastLocation(pos, nil)
				data := d.photo.data
				d.photo = nil // writeMediaFile releases the buffer
				pos.Set(model.KeyImage, m.WriteMedia(imei, "jpg", data))
				return pos
			}
		}

	case typ == "A03":
		return decodeLocationNew(m, kit.Latin1(buf.Bytes()))

	default:
		return decodeLocation(m, kit.Latin1(buf.Bytes()))
	}
	return nil
}

// encode is FifotrackProtocolEncoder.
func encode(m *ingest.Message, c *model.Command) any {
	format := func(content string) string {
		uniqueID := m.UniqueID(c.DeviceID)
		length := 1 + kit.Len(uniqueID) + 3 + kit.Len(content)
		result := kit.Format("##%02d,%s,1,%s*", length, uniqueID, content)
		return result + kit.SumString(result) + "\r\n"
	}
	switch c.Type {
	case model.CommandCustom:
		data := kit.CommandString(c, model.CommandKeyData)
		if data == nil {
			kit.Throw("NullPointerException: data")
		}
		return format(*data)
	case model.CommandRequestPhoto:
		return format("D05,3")
	}
	return nil
}
