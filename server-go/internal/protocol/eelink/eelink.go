// Package eelink is the Eelink protocol (EelinkProtocol): binary packages over TCP, and
// over UDP with an "EL" header carrying the IMEI.
package eelink

import (
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
	return ingest.Protocol{
		Name:         "eelink",
		DataCommands: protocol.DataCommands("eelink"),
		Servers: []ingest.Server{
			{NewPipeline: func() ingest.Pipeline {
				return ingest.Pipeline{
					Framer:  ingest.NewLengthFramer(ingest.MaxFrameLength, 3, 2, 0, 0),
					Decoder: &Decoder{},
					Encoder: &Encoder{},
				}
			}},
			{Datagram: true, NewPipeline: func() ingest.Pipeline {
				return ingest.Pipeline{
					Decoder: &Decoder{Connectionless: true},
					Encoder: &Encoder{Connectionless: true},
				}
			}},
		},
	}
}

const (
	msgLogin     = 0x01
	msgGPS       = 0x02
	msgHeartbeat = 0x03
	msgAlarm     = 0x04
	msgState     = 0x05
	msgSMS       = 0x06
	msgOBD       = 0x07
	msgDownlink  = 0x80
	msgData      = 0x81

	msgNormal     = 0x12
	msgWarning    = 0x14
	msgReport     = 0x15
	msgCommand    = 0x16
	msgOBDData    = 0x17
	msgOBDBody    = 0x18
	msgOBDCode    = 0x19
	msgCameraInfo = 0x1E
	msgCameraData = 0x1F
)

// Decoder is EelinkProtocolDecoder; Connectionless is set on the UDP server (Java's
// channel instanceof DatagramChannel).
type Decoder struct {
	Connectionless bool
}

func decodeAlarm(value int) string {
	switch value {
	case 0x01:
		return model.AlarmPowerOff
	case 0x02:
		return model.AlarmSos
	case 0x03:
		return model.AlarmLowBattery
	case 0x04:
		return model.AlarmVibration
	case 0x08, 0x09:
		return model.AlarmGpsAntennaCut
	case 0x25:
		return model.AlarmRemoving
	case 0x81:
		return model.AlarmLowSpeed
	case 0x82:
		return model.AlarmOverspeed
	case 0x83:
		return model.AlarmGeofenceEnter
	case 0x84:
		return model.AlarmGeofenceExit
	case 0x85:
		return model.AlarmAccident
	case 0x86:
		return model.AlarmFallDown
	}
	return ""
}

func check(v, bit int) bool { return kit.Check(int64(v), bit) }

func decodeStatus(pos *model.Position, status int) {
	if check(status, 1) {
		pos.Set(model.KeyIgnition, check(status, 2))
	}
	if check(status, 3) {
		pos.Set(model.KeyArmed, check(status, 4))
	}
	if check(status, 5) {
		pos.Set(model.KeyBlocked, !check(status, 6))
	}
	if check(status, 7) {
		pos.Set(model.KeyCharge, check(status, 8))
	}
	pos.Set(model.KeyStatus, status)
}

func decodeOld(m *ingest.Message, ds *ingest.DeviceSession, buf *kit.Buf, typ, index int) *model.Position {
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID
	pos.Set(model.KeyIndex, index)

	pos.SetTime(time.UnixMilli(buf.ReadUnsignedInt() * 1000))
	pos.SetLatitude(float64(buf.ReadInt()) / 1800000.0)
	pos.SetLongitude(float64(buf.ReadInt()) / 1800000.0)
	pos.Speed = units.KnotsFromKph(float64(buf.ReadUnsignedByte()))
	pos.Course = float64(buf.ReadUnsignedShort())

	mcc, mnc, lac := buf.ReadUnsignedShort(), buf.ReadUnsignedShort(), buf.ReadUnsignedShort()
	pos.Network = model.NetworkOf(model.NewCellTower(mcc, mnc, lac, int64(buf.ReadUnsignedMedium())))

	pos.Valid = buf.ReadUnsignedByte()&0x01 != 0

	switch typ {
	case msgGPS:
		if buf.ReadableBytes() >= 2 {
			decodeStatus(pos, buf.ReadUnsignedShort())
		}
		if buf.ReadableBytes() >= 2*4 {
			pos.Set(model.KeyBattery, float64(buf.ReadUnsignedShort())/1000.0)
			pos.Set(model.KeyRssi, buf.ReadUnsignedShort())
			pos.Set(model.PrefixAdc+"1", buf.ReadUnsignedShort())
			pos.Set(model.PrefixAdc+"2", buf.ReadUnsignedShort())
		}
	case msgAlarm:
		pos.AddAlarm(decodeAlarm(buf.ReadUnsignedByte()))
	case msgState:
		statusType := buf.ReadUnsignedByte()
		pos.Set(model.KeyEvent, statusType)
		if statusType == 0x01 || statusType == 0x02 || statusType == 0x03 {
			buf.ReadUnsignedInt() // device time
			if buf.ReadableBytes() >= 2 {
				decodeStatus(pos, buf.ReadUnsignedShort())
			}
		}
	}
	return pos
}

// readMac is hexDump(readSlice(6)) with ':' between the bytes.
func readMac(buf *kit.Buf) string {
	h := buf.ReadSlice(6).HexDump()
	parts := make([]string, 0, 6)
	for i := 0; i < len(h); i += 2 {
		parts = append(parts, h[i:i+2])
	}
	return strings.Join(parts, ":")
}

func decodeNew(m *ingest.Message, ds *ingest.DeviceSession, buf *kit.Buf, typ, index int) *model.Position {
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID
	pos.Set(model.KeyIndex, index)

	pos.SetTime(time.UnixMilli(buf.ReadUnsignedInt() * 1000))

	flags := buf.ReadUnsignedByte()

	if check(flags, 0) {
		pos.SetLatitude(float64(buf.ReadInt()) / 1800000.0)
		pos.SetLongitude(float64(buf.ReadInt()) / 1800000.0)
		pos.Altitude = float64(buf.ReadShort())
		pos.Speed = units.KnotsFromKph(float64(buf.ReadUnsignedShort()))
		pos.Course = float64(buf.ReadUnsignedShort())
		pos.Set(model.KeySatellites, buf.ReadUnsignedByte())
	} else {
		deviceTime := pos.DeviceTime.Time
		m.LastLocation(pos, &deviceTime)
	}

	network := model.NewNetwork()

	mcc, mnc := 0, 0
	if check(flags, 1) {
		mcc = buf.ReadUnsignedShort()
		mnc = buf.ReadUnsignedShort()
		lac, cid := buf.ReadUnsignedShort(), buf.ReadUnsignedInt()
		network.AddCellTower(model.NewCellTowerRSSI(mcc, mnc, lac, cid, buf.ReadUnsignedByte()))
	}
	for _, bit := range []int{2, 3} {
		if check(flags, bit) {
			lac, cid := buf.ReadUnsignedShort(), buf.ReadUnsignedInt()
			network.AddCellTower(model.NewCellTowerRSSI(mcc, mnc, lac, cid, buf.ReadUnsignedByte()))
		}
	}
	for _, bit := range []int{4, 5, 6} {
		if check(flags, bit) {
			mac := readMac(buf)
			network.AddWifiAccessPoint(model.NewWifiAccessPoint(mac, buf.ReadUnsignedByte()))
		}
	}

	if check(flags, 7) {
		buf.ReadUnsignedByte() // radio access technology
		count := buf.ReadUnsignedByte()
		lac := 0
		if count > 0 {
			mcc = buf.ReadUnsignedShort()
			mnc = buf.ReadUnsignedShort()
			lac = buf.ReadUnsignedShort() // lac
			buf.ReadUnsignedShort()       // tac
			buf.ReadUnsignedInt()         // cid
			buf.ReadUnsignedShort()       // ta
		}
		for i := 0; i < count; i++ {
			cid := buf.ReadUnsignedShort() // physical cid
			buf.ReadUnsignedShort()        // e-arfcn
			rssi := buf.ReadUnsignedByte()
			network.AddCellTower(model.NewCellTowerRSSI(mcc, mnc, lac, int64(cid), rssi))
		}
	}

	if network.CellTowers != nil || network.WifiAccessPoints != nil {
		pos.Network = network
	}

	switch typ {
	case msgWarning:
		pos.AddAlarm(decodeAlarm(buf.ReadUnsignedByte()))
	case msgReport:
		buf.ReadUnsignedByte() // report type
	}

	if typ == msgNormal || typ == msgWarning || typ == msgReport {
		status := buf.ReadUnsignedShort()
		pos.Valid = check(status, 0)
		if check(status, 1) {
			pos.Set(model.KeyIgnition, check(status, 2))
		}
		if check(status, 3) {
			pos.Set(model.KeyArmed, check(status, 4))
			pos.Set(model.KeyMotion, check(status, 9))
		}
		if check(status, 5) {
			pos.Set(model.KeyBlocked, check(status, 6))
		}
		if check(status, 7) {
			pos.Set(model.KeyCharge, check(status, 8))
		}
		pos.Set(model.KeyGps, check(status, 10))
		pos.Set(model.KeyStatus, status)
	}

	if typ == msgNormal {
		if buf.ReadableBytes() >= 2 {
			pos.Set(model.KeyBattery, float64(buf.ReadUnsignedShort())/1000.0)
		}
		if buf.ReadableBytes() >= 4 {
			pos.Set(model.PrefixAdc+"0", buf.ReadUnsignedShort())
			pos.Set(model.PrefixAdc+"1", buf.ReadUnsignedShort())
		}
		if buf.ReadableBytes() >= 4 {
			pos.Set(model.KeyOdometer, buf.ReadUnsignedInt())
		}
		if buf.ReadableBytes() >= 4 {
			buf.ReadUnsignedShort() // gsm counter
			buf.ReadUnsignedShort() // gps counter
		}
		if buf.ReadableBytes() >= 4 {
			pos.Set(model.KeySteps, buf.ReadUnsignedShort())
			buf.ReadUnsignedShort() // walking time
		}
		if buf.ReadableBytes() >= 12 {
			pos.Set(model.PrefixTemp+"1", float64(buf.ReadShort())/256.0)
			pos.Set(model.KeyHumidity, float64(buf.ReadUnsignedShort())/10.0)
			pos.Set("illuminance", float64(buf.ReadUnsignedInt())/256.0)
			pos.Set("co2", buf.ReadUnsignedInt())
		}
		if buf.ReadableBytes() >= 2 {
			pos.Set(model.PrefixTemp+"2", float64(buf.ReadShort())/16.0)
		}
		if buf.ReadableBytes() >= 2 {
			count := buf.ReadUnsignedByte()
			buf.ReadUnsignedByte() // id
			for i := 1; i <= count; i++ {
				tag := "tag" + strconv.Itoa(i)
				pos.Set(tag+"Id", buf.ReadSlice(6).HexDump())
				buf.ReadUnsignedByte() // signal level
				buf.ReadUnsignedByte() // reserved
				buf.ReadUnsignedByte() // model
				buf.ReadUnsignedByte() // version
				pos.Set(tag+"Battery", float64(buf.ReadUnsignedShort())/1000.0)
				pos.Set(tag+"Temp", float64(buf.ReadShort())/256.0)
				pos.Set(tag+"Data", buf.ReadUnsignedShort())
			}
		}
	}
	return pos
}

var pattern = kit.NewPattern().
	Text("Lat:").
	Number("([NS])(d+.d+)"). // latitude
	Any().
	Text("Lon:").
	Number("([EW])(d+.d+)"). // longitude
	Any().
	Text("Course:").
	Number("(d+.d+)"). // course
	Any().
	Text("Speed:").
	Number("(d+.d+)"). // speed
	Any().
	Expression("Date ?Time:").
	Number("(dddd)-(dd)-(dd) "). // date
	Number("(dd):(dd):(dd)").    // time
	Compile()

func decodeResult(m *ingest.Message, ds *ingest.DeviceSession, buf *kit.Buf, index int) *model.Position {
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID
	pos.Set(model.KeyIndex, index)

	buf.ReadUnsignedByte() // type
	buf.ReadUnsignedInt()  // uid

	sentence := kit.UTF8(buf.Bytes())

	parser := kit.NewParser(pattern, sentence)
	if parser.Matches() {
		pos.Valid = true
		pos.SetLatitude(parser.NextCoordinate(kit.HemDeg))
		pos.SetLongitude(parser.NextCoordinate(kit.HemDeg))
		pos.Course = parser.MustDouble()
		pos.Speed = units.KnotsFromKph(parser.MustDouble())
		pos.SetTime(parser.NextDateTimeDefault())
	} else {
		m.LastLocation(pos, nil)
		pos.Set(model.KeyResult, sentence)
	}
	return pos
}

func decodeOBD(m *ingest.Message, ds *ingest.DeviceSession, buf *kit.Buf) *model.Position {
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	t := time.UnixMilli(buf.ReadUnsignedInt() * 1000)
	m.LastLocation(pos, &t)

	for buf.ReadableBytes() > 0 {
		pid := buf.ReadUnsignedByte()
		value := int(buf.ReadInt())
		switch pid {
		case 0x89:
			pos.Set(model.KeyFuelConsumption, value)
		case 0x8a:
			pos.Set(model.KeyOdometer, int64(value)*1000)
		case 0x8b:
			pos.Set(model.KeyFuel, value/10)
		}
	}
	return pos
}

func (d *Decoder) Decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	var uniqueID string
	var ds *ingest.DeviceSession
	if buf.GetByte(0) == 'E' && buf.GetByte(1) == 'L' {
		buf.Skip(2 + 2 + 2) // udp header
		uniqueID = kit.SubstringFrom(buf.ReadSlice(8).HexDump(), 1)
		ds = m.Session(uniqueID)
	} else {
		ds = m.Session()
	}

	var positions []*model.Position
	for buf.IsReadable() {
		if pos := d.decodePackage(m, buf, uniqueID, ds); pos != nil {
			positions = append(positions, pos)
		}
	}
	switch len(positions) {
	case 0:
		return nil
	case 1:
		return positions[0]
	}
	return positions
}

func (d *Decoder) decodePackage(m *ingest.Message, buf *kit.Buf, uniqueID string, ds *ingest.DeviceSession) *model.Position {
	buf.Skip(2) // header
	typ := buf.ReadUnsignedByte()
	buf = buf.ReadSlice(buf.ReadUnsignedShort())
	index := buf.ReadUnsignedShort()

	if typ != msgGPS && typ != msgData {
		content := kit.Empty()
		if typ == msgLogin {
			content.WriteInt(int64(int32(m.Now.UnixMilli() / 1000)))
			content.WriteShort(1) // protocol version
			content.WriteByte8(0) // action mask
		}
		m.Send(encodeContent(d.Connectionless, uniqueID, typ, index, content))
	}

	if typ == msgLogin {
		if ds == nil {
			m.Session(kit.SubstringFrom(buf.ReadSlice(8).HexDump(), 1))
		}
		return nil
	}
	if ds == nil {
		return nil
	}

	switch {
	case typ == msgGPS || typ == msgAlarm || typ == msgState || typ == msgSMS:
		return decodeOld(m, ds, buf, typ, index)
	case typ >= msgNormal && typ <= msgOBDCode:
		return decodeNew(m, ds, buf, typ, index)
	case typ == msgHeartbeat && buf.ReadableBytes() >= 2 || typ == msgOBD && buf.ReadableBytes() == 4:
		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID
		m.LastLocation(pos, nil)
		decodeStatus(pos, buf.ReadUnsignedShort())
		return pos
	case typ == msgOBD:
		return decodeOBD(m, ds, buf)
	case typ == msgDownlink:
		return decodeResult(m, ds, buf, index)
	}
	return nil
}

// checksum is EelinkProtocolEncoder.checksum.
func checksum(data []byte) int {
	sum := 0
	for _, b := range data {
		sum = (((sum << 1) | (sum >> 15)) + int(b)) & 0xFFFF
	}
	return sum
}

// encodeContent is EelinkProtocolEncoder.encodeContent: a package, with the UDP header
// when connectionless.
func encodeContent(connectionless bool, uniqueID string, typ, index int, content *kit.Buf) *kit.Buf {
	buf := kit.Empty()
	if connectionless {
		buf.WriteBytes(kit.ParseHex("0" + uniqueID))
	}
	buf.WriteByte8(0x67)
	buf.WriteByte8(0x67)
	buf.WriteByte8(typ)
	length := 2
	if content != nil {
		length += content.ReadableBytes()
	}
	buf.WriteShort(length)
	buf.WriteShort(index)
	if content != nil {
		buf.WriteBuf(content)
	}

	result := kit.Empty()
	if connectionless {
		result.WriteByte8('E')
		result.WriteByte8('L')
		result.WriteShort(2 + buf.ReadableBytes()) // length
		result.WriteShort(checksum(buf.Bytes()))
	}
	result.WriteBuf(buf)
	return result
}

// Encoder is EelinkProtocolEncoder.
type Encoder struct {
	Connectionless bool
}

func (e *Encoder) encode(m *ingest.Message, deviceID int64, content string) *kit.Buf {
	buf := kit.Empty()
	buf.WriteByte8(0x01) // command
	buf.WriteInt(0)      // server id
	buf.WriteBytes([]byte(content))
	return encodeContent(e.Connectionless, m.UniqueID(deviceID), msgDownlink, 0, buf)
}

func (e *Encoder) Encode(m *ingest.Message, c *model.Command) any {
	switch c.Type {
	case model.CommandCustom:
		data := kit.CommandString(c, model.CommandKeyData)
		if data == nil {
			kit.Throw("NullPointerException: data")
		}
		return e.encode(m, c.DeviceID, *data)
	case model.CommandPositionSingle:
		return e.encode(m, c.DeviceID, "WHERE#")
	case model.CommandEngineStop:
		return e.encode(m, c.DeviceID, "RELAY,1#")
	case model.CommandEngineResume:
		return e.encode(m, c.DeviceID, "RELAY,0#")
	case model.CommandRebootDevice:
		return e.encode(m, c.DeviceID, "RESET#")
	}
	return nil
}
