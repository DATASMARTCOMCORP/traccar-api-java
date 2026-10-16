// Package t800x is the T800x protocol (T800xProtocol): binary messages with a 0x2323,
// 0x2525, 0x2626 or 0x2727 header over TCP and UDP, and binary commands.
package t800x

import (
	"time"
	"unicode/utf16"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: TCP and UDP servers.
func Protocol() ingest.Protocol {
	pipeline := func(framed bool) func() ingest.Pipeline {
		return func() ingest.Pipeline {
			d := &Decoder{}
			p := ingest.Pipeline{
				Decoder: d,
				Encoder: &Encoder{Decoder: d},
			}
			if framed {
				p.Framer = ingest.NewLengthFramer(ingest.MaxFrameLength, 3, 2, -5, 0)
			}
			return p
		}
	}
	return ingest.Protocol{
		Name:         "t800x",
		DataCommands: protocol.DataCommands("t800x"),
		Servers: []ingest.Server{
			{NewPipeline: pipeline(true)},
			{Datagram: true, NewPipeline: pipeline(false)},
		},
	}
}

const defaultHeader int16 = 0x2323

const (
	msgGps             = 0x02
	msgAlarm           = 0x04
	msgNetwork         = 0x05 // 0x2727
	msgDriverBehavior1 = 0x05 // 0x2626
	msgDriverBehavior2 = 0x06 // 0x2626
	msgBle             = 0x10
	msgNetwork2        = 0x11
	msgGps2            = 0x13
	msgAlarm2          = 0x14
	msgCommand         = 0x81
	modeSetting        = 0x01
)

// Decoder is T800xProtocolDecoder: it remembers the header of the last message, which
// commands reuse.
type Decoder struct {
	header        int16
	headerSet     bool
	ModelOverride *string // setModelOverride
}

// Header is getHeader: the last message's header, 0x2323 before any.
func (d *Decoder) Header() int16 {
	if !d.headerSet {
		return defaultHeader
	}
	return d.header
}

func sendResponse(m *ingest.Message, header int16, typ, index int, imei *kit.Buf, alarm int) {
	length := 15
	if alarm > 0 {
		length = 16
	}
	response := kit.Empty()
	response.WriteShort(int(header))
	response.WriteByte8(typ)
	response.WriteShort(length)
	response.WriteShort(index)
	response.WriteBuf(imei)
	if alarm > 0 {
		response.WriteByte8(alarm)
	}
	m.Send(response)
}

func decodeAlarm1(value int) string {
	switch value {
	case 1:
		return model.AlarmPowerCut
	case 2:
		return model.AlarmLowBattery
	case 3:
		return model.AlarmSos
	case 4:
		return model.AlarmOverspeed
	case 5:
		return model.AlarmGeofenceEnter
	case 6:
		return model.AlarmGeofenceExit
	case 7:
		return model.AlarmTow
	case 8, 10:
		return model.AlarmVibration
	case 21:
		return model.AlarmJamming
	case 23:
		return model.AlarmPowerRestored
	case 24:
		return model.AlarmLowPower
	}
	return ""
}

func decodeAlarm2(value int) string {
	switch value {
	case 1, 4:
		return model.AlarmRemoving
	case 2:
		return model.AlarmTampering
	case 3:
		return model.AlarmSos
	case 5:
		return model.AlarmFallDown
	case 6:
		return model.AlarmLowBattery
	case 14:
		return model.AlarmGeofenceEnter
	case 15:
		return model.AlarmGeofenceExit
	}
	return ""
}

func readDate(buf *kit.Buf) time.Time {
	return kit.NewDateBuilder(nil).
		SetYear(kit.ReadBCD(buf, 2)).
		SetMonth(kit.ReadBCD(buf, 2)).
		SetDay(kit.ReadBCD(buf, 2)).
		SetHour(kit.ReadBCD(buf, 2)).
		SetMinute(kit.ReadBCD(buf, 2)).
		SetSecond(kit.ReadBCD(buf, 2)).
		Date()
}

// utf16LE is new String(bytes, UTF_16LE): malformed input becomes U+FFFD.
func utf16LE(b []byte) string {
	u := make([]uint16, 0, (len(b)+1)/2)
	for i := 0; i+1 < len(b); i += 2 {
		u = append(u, uint16(b[i])|uint16(b[i+1])<<8)
	}
	s := string(utf16.Decode(u))
	if len(b)%2 != 0 {
		s += "�"
	}
	return s
}

// Decode decodes one message.
func (d *Decoder) Decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	d.header, d.headerSet = int16(buf.ReadShort()), true
	header := d.header
	typ := buf.ReadUnsignedByte()
	buf.ReadUnsignedShort() // length
	index := buf.ReadUnsignedShort()
	imei := buf.ReadSlice(8)

	ds := m.Session(kit.SubstringFrom(imei.HexDump(), 1))
	if ds == nil {
		return nil
	}

	positionType := typ == msgGps || typ == msgGps2 || typ == msgAlarm || typ == msgAlarm2

	if !positionType {
		responseIndex := index
		if header == 0x2323 {
			responseIndex = 1
		}
		sendResponse(m, header, typ, responseIndex, imei, 0)
	}

	switch {
	case positionType:
		return d.decodePosition(m, ds, buf, typ, index, imei)

	case typ == msgNetwork && header == 0x2727 || typ == msgNetwork2:
		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID
		t := readDate(buf)
		m.LastLocation(pos, &t)
		pos.Set(model.KeyOperator, utf16LE(buf.ReadBytes(buf.ReadUnsignedByte())))
		pos.Set("networkTechnology", buf.ReadString(buf.ReadUnsignedByte()))
		pos.Set("networkBand", buf.ReadString(buf.ReadUnsignedByte()))
		buf.ReadString(buf.ReadUnsignedByte()) // imsi
		pos.Set(model.KeyIccid, buf.ReadString(buf.ReadUnsignedByte()))
		return pos

	case typ == msgDriverBehavior1 && header == 0x2626:
		var alarm string
		switch buf.ReadUnsignedByte() {
		case 0, 2, 4:
			alarm = model.AlarmBraking
		case 1, 3, 5:
			alarm = model.AlarmAcceleration
		}

		positions := []*model.Position{}
		for buf.ReadableBytes() >= 24 {
			pos := m.NewPosition()
			pos.DeviceID = ds.DeviceID

			t := readDate(buf)
			if buf.GetInt(buf.ReaderIndex()) != -1 {
				pos.Valid = true
				pos.SetTime(t)
				pos.Altitude = float64(buf.ReadFloatLE())
				pos.SetLongitude(float64(buf.ReadFloatLE()))
				pos.SetLatitude(float64(buf.ReadFloatLE()))
			} else {
				buf.Skip(12)
				m.LastLocation(pos, &t)
			}

			pos.Speed = units.KnotsFromKph(float64(kit.ReadBCD(buf, 4)) / 10.0)

			course := buf.ReadUnsignedShort()
			if course != 0xffff {
				pos.Course = float64(course) / 10.0
			}
			rpm := buf.ReadUnsignedShort()
			if rpm != 0xffff {
				pos.Set(model.KeyRpm, rpm)
			}

			positions = append(positions, pos)
		}

		if len(positions) == 0 {
			kit.Throw("NoSuchElementException")
		}
		positions[len(positions)-1].AddAlarm(alarm)
		return positions

	case typ == msgDriverBehavior2 && header == 0x2626:
		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID

		switch buf.ReadUnsignedByte() {
		case 0:
			pos.AddAlarm(model.AlarmBraking)
		case 1:
			pos.AddAlarm(model.AlarmAcceleration)
		case 2:
			pos.AddAlarm(model.AlarmCornering)
		}

		pos.SetTime(readDate(buf))

		status := buf.ReadUnsignedByte()
		pos.Valid = !kit.Check(int64(status), 7)

		buf.Skip(5) // acceleration

		pos.Altitude = float64(buf.ReadFloatLE())
		pos.SetLongitude(float64(buf.ReadFloatLE()))
		pos.SetLatitude(float64(buf.ReadFloatLE()))
		pos.Speed = units.KnotsFromKph(float64(kit.ReadBCD(buf, 4)) / 10.0)
		pos.Course = float64(buf.ReadUnsignedShort()) / 10.0

		pos.Set(model.KeyRpm, buf.ReadUnsignedShort())
		return pos

	case typ == msgBle:
		return d.decodeBle(m, ds, buf, typ, index, imei)

	case typ == msgCommand:
		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID
		m.LastLocation(pos, nil)

		buf.ReadUnsignedByte() // protocol number

		pos.Set(model.KeyResult, utf16LE(buf.Bytes()))
		return pos
	}

	return nil
}

func decodeBleTemp(buf *kit.Buf) float64 {
	value := buf.ReadUnsignedShort()
	v := kit.ToInt(value, 15)
	if kit.Check(int64(value), 15) {
		v = -v
	}
	return float64(v) / 100.0
}

func (d *Decoder) decodeBle(
	m *ingest.Message, ds *ingest.DeviceSession, buf *kit.Buf, typ, index int, imei *kit.Buf) any {

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID
	t := readDate(buf)
	m.LastLocation(pos, &t)

	pos.Set(model.KeyIgnition, buf.ReadUnsignedByte() > 0)

	i := 1
	for buf.IsReadable() {
		tag := "tag" + kit.Format("%d", i)
		switch buf.ReadUnsignedShort() {
		case 0x01:
			pos.Set(tag+"Id", buf.ReadSlice(6).HexDump())
			pos.Set(tag+"Battery", float64(buf.ReadUnsignedByte())/100.0+1.22)
			pos.Set(tag+"TirePressure", float64(buf.ReadUnsignedByte())*1.527*2)
			pos.Set(tag+"TireTemp", buf.ReadUnsignedByte()-55)
			pos.Set(tag+"TireStatus", buf.ReadUnsignedByte())
		case 0x02:
			pos.Set(tag+"Id", buf.ReadSlice(6).HexDump())
			pos.Set(tag+"Battery", float64(kit.ReadBCD(buf, 2))/10.0)
			switch buf.ReadUnsignedByte() {
			case 0:
				pos.AddAlarm(model.AlarmSos)
			case 1:
				pos.AddAlarm(model.AlarmLowBattery)
			}
			buf.ReadUnsignedByte() // status
			buf.Skip(16)           // location
		case 0x03:
			pos.Set(model.KeyDriverUniqueID, buf.ReadSlice(6).HexDump())
			pos.Set(tag+"Battery", float64(kit.ReadBCD(buf, 2))/10.0)
			if buf.ReadUnsignedByte() == 1 {
				pos.AddAlarm(model.AlarmLowBattery)
			}
			buf.ReadUnsignedByte() // status
			buf.Skip(16)           // location
		case 0x04:
			pos.Set(tag+"Id", buf.ReadSlice(6).HexDump())
			pos.Set(tag+"Battery", float64(buf.ReadUnsignedByte())/100.0+2)
			buf.ReadUnsignedByte() // battery level
			pos.Set(tag+"Temp", decodeBleTemp(buf))
			pos.Set(tag+"Humidity", float64(buf.ReadUnsignedShort())/100.0)
			pos.Set(tag+"LightSensor", buf.ReadUnsignedShort())
			pos.Set(tag+"Rssi", buf.ReadUnsignedByte()-128)
		case 0x05:
			pos.Set(tag+"Id", buf.ReadSlice(6).HexDump())
			pos.Set(tag+"Battery", float64(buf.ReadUnsignedByte())/100.0+2)
			buf.ReadUnsignedByte() // battery level
			pos.Set(tag+"Temp", decodeBleTemp(buf))
			pos.Set(tag+"Door", buf.ReadUnsignedByte() > 0)
			pos.Set(tag+"Rssi", buf.ReadUnsignedByte()-128)
		case 0x06:
			pos.Set(tag+"Id", buf.ReadSlice(6).HexDump())
			pos.Set(tag+"Battery", float64(buf.ReadUnsignedByte())/100.0+2)
			pos.Set(tag+"Output", buf.ReadUnsignedByte() > 0)
			pos.Set(tag+"Rssi", buf.ReadUnsignedByte()-128)
		}
		i++
	}

	sendResponse(m, d.header, typ, index, imei, 0)
	return pos
}

func (d *Decoder) decodePosition(
	m *ingest.Message, ds *ingest.DeviceSession, buf *kit.Buf, typ, index int, imei *kit.Buf) any {

	header := d.header

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID
	pos.Set(model.KeyIndex, index)

	if header != 0x2727 {
		buf.ReadUnsignedShort() // acc on interval
		buf.ReadUnsignedShort() // acc off interval
		buf.ReadUnsignedByte()  // angle compensation
		buf.ReadUnsignedShort() // distance compensation
		pos.Set(model.KeyRssi, kit.ToInt(buf.ReadUnsignedShort(), 7))
	}

	status := buf.ReadUnsignedByte()
	pos.Set(model.KeySatellites, kit.ToInt(status, 5))

	if header != 0x2727 {
		buf.ReadUnsignedByte()  // gsensor manager status
		buf.ReadUnsignedByte()  // other flags
		buf.ReadUnsignedByte()  // heartbeat
		buf.ReadUnsignedByte()  // relay status
		buf.ReadUnsignedShort() // drag alarm setting

		io := int64(buf.ReadUnsignedShort())
		pos.Set(model.KeyIgnition, kit.Check(io, 14))
		pos.Set("ac", kit.Check(io, 13))
		pos.Set(model.PrefixIn+"3", kit.Check(io, 12))
		pos.Set(model.PrefixIn+"4", kit.Check(io, 11))

		if typ == msgGps2 || typ == msgAlarm2 {
			pos.Set(model.KeyOutput, buf.ReadUnsignedByte())
			buf.ReadUnsignedByte() // reserved
		} else {
			pos.Set(model.PrefixOut+"1", kit.Check(io, 7))
			pos.Set(model.PrefixOut+"2", kit.Check(io, 8))
			pos.Set(model.PrefixOut+"3", kit.Check(io, 9))
		}

		if header != 0x2626 {
			adcCount := 2
			if typ == msgGps2 || typ == msgAlarm2 {
				adcCount = 5
			}
			for i := 1; i <= adcCount; i++ {
				value := buf.ReadSlice(2).HexDump()
				if value != "ffff" {
					pos.Set(model.PrefixAdc+kit.Format("%d", i), float64(kit.IntRadix(value, 16))/100.0)
				}
			}
		}
	}

	alarm := buf.ReadUnsignedByte()
	if header != 0x2727 {
		pos.AddAlarm(decodeAlarm1(alarm))
	} else {
		pos.AddAlarm(decodeAlarm2(alarm))
	}
	pos.Set("alarmCode", alarm)

	if header != 0x2727 {
		buf.ReadUnsignedByte() // reserved
		pos.Set(model.KeyOdometer, buf.ReadUnsignedInt())
		battery := kit.ReadBCD(buf, 2)
		if battery <= 0 {
			battery = 100
		}
		pos.Set(model.KeyBatteryLevel, battery)
	}

	if kit.Check(int64(status), 6) {
		pos.Valid = true
		pos.SetTime(readDate(buf))
		pos.Altitude = float64(buf.ReadFloatLE())
		pos.SetLongitude(float64(buf.ReadFloatLE()))
		pos.SetLatitude(float64(buf.ReadFloatLE()))
		if header == 0x2626 {
			buf.ReadUnsignedShort() // reserved or hdop
		} else {
			pos.Speed = units.KnotsFromKph(float64(kit.ReadBCD(buf, 4)) / 10.0)
		}
		pos.Course = float64(buf.ReadUnsignedShort())
	} else {
		t := readDate(buf)
		m.LastLocation(pos, &t)
		mcc := buf.ReadUnsignedShortLE()
		mnc := buf.ReadUnsignedShortLE()
		if mcc != 0xffff && mnc != 0xffff {
			network := model.NewNetwork()
			for range 3 {
				lac := buf.ReadUnsignedShortLE()
				cid := buf.ReadUnsignedShortLE()
				network.AddCellTower(model.NewCellTower(mcc, mnc, lac, int64(cid)))
			}
			pos.Network = network
		}
	}

	if header == 0x2727 {
		accelerationBytes := buf.ReadBytes(5)
		var acceleration int64 // new BigInteger(bytes).longValue(): signed big-endian
		if accelerationBytes[0]&0x80 != 0 {
			acceleration = -1
		}
		for _, b := range accelerationBytes {
			acceleration = acceleration<<8 | int64(b)
		}
		accelerationZ := float64(kit.Between(acceleration, 8, 15)) + float64(kit.Between(acceleration, 4, 8))/10.0
		if !kit.Check(acceleration, 15) {
			accelerationZ = -accelerationZ
		}
		accelerationY := float64(kit.Between(acceleration, 20, 27)) + float64(kit.Between(acceleration, 16, 20))/10.0
		if !kit.Check(acceleration, 27) {
			accelerationY = -accelerationY
		}
		accelerationX := float64(kit.Between(acceleration, 28, 32)) + float64(kit.Between(acceleration, 32, 39))/10.0
		if !kit.Check(acceleration, 39) {
			accelerationX = -accelerationX
		}
		pos.Set(model.KeyGSensor, "["+model.JavaToString(accelerationX)+","+
			model.JavaToString(accelerationY)+","+model.JavaToString(accelerationZ)+"]")

		battery := kit.ReadBCD(buf, 2)
		if battery <= 0 {
			battery = 100
		}
		pos.Set(model.KeyBatteryLevel, battery)
		pos.Set(model.KeyDeviceTemp, int(buf.ReadSignedByte()))
		pos.Set("lightSensor", float64(kit.ReadBCD(buf, 2))/10.0)
		pos.Set(model.KeyBattery, float64(kit.ReadBCD(buf, 2))/10.0)
		pos.Set("solarPanel", float64(kit.ReadBCD(buf, 2))/10.0)
		pos.Set(model.KeyOdometer, buf.ReadUnsignedInt())

		inputStatus := buf.ReadUnsignedShort()
		pos.Set(model.KeyIgnition, kit.Check(int64(inputStatus), 2))
		pos.Set(model.KeyRssi, kit.BetweenInt(inputStatus, 4, 11))
		pos.Set(model.KeyInput, inputStatus)

		buf.ReadUnsignedShort() // ignition on upload interval
		buf.ReadUnsignedInt()   // ignition off upload interval
		buf.ReadUnsignedByte()  // angle upload interval
		buf.ReadUnsignedShort() // distance upload interval
		buf.ReadUnsignedByte()  // heartbeat
	} else {
		deviceModel := m.DeviceModel(ds)
		if d.ModelOverride != nil {
			deviceModel = *d.ModelOverride
		}
		if deviceModel == "TLW2-2BL" || typ == msgGps2 || typ == msgAlarm2 {
			pos.Set(model.KeyBattery, float64(kit.ReadBCD(buf, 4))/100.0)
		}

		if buf.ReadableBytes() >= 2 {
			pos.Set(model.KeyPower, float64(kit.ReadBCD(buf, 4))/100.0)
		}

		if buf.ReadableBytes() >= 19 {
			pos.Speed = units.KnotsFromKph(float64(kit.ReadBCD(buf, 4)) / 10.0)
			pos.Set(model.KeyObdSpeed, float64(kit.ReadBCD(buf, 4))/100.0)
			pos.Set(model.KeyFuelUsed, float64(buf.ReadUnsignedInt())/1000.0)
			pos.Set(model.KeyFuelConsumption, float64(buf.ReadUnsignedInt())/1000.0)
			pos.Set(model.KeyRpm, buf.ReadUnsignedShort())
			// Java reads one byte and reports it under every key.
			value := buf.ReadUnsignedByte()
			if value != 0xff {
				pos.Set("airInput", value)
				pos.Set("airPressure", value)
				pos.Set(model.KeyCoolantTemp, value-40)
				pos.Set("airTemp", value-40)
				pos.Set(model.KeyEngineLoad, value)
				pos.Set(model.KeyThrottle, value)
				pos.Set(model.KeyFuel, value)
			}
		}
	}

	ack := m.LookupBool(ds.DeviceID, m.Protocol()+".ack", false, true)
	if ack != nil && *ack || typ == msgAlarm || typ == msgAlarm2 {
		responseIndex := index
		if header == 0x2323 {
			responseIndex = 1
		}
		sendResponse(m, header, typ, responseIndex, imei, alarm)
	}

	return pos
}

// Encoder is T800xProtocolEncoder.
type Encoder struct {
	Decoder *Decoder // the connection's decoder; nil without a channel
}

// Encode encodes a command.
func (e *Encoder) Encode(m *ingest.Message, c *model.Command) any {
	header := defaultHeader
	if e.Decoder != nil {
		header = e.Decoder.Header()
	}
	switch c.Type {
	case model.CommandCustom:
		content := *kit.CommandString(c, model.CommandKeyData)
		buf := kit.Empty()
		buf.WriteShort(int(header))
		buf.WriteByte8(msgCommand)
		buf.WriteShort(7 + 8 + 1 + kit.Len(content))
		buf.WriteShort(1) // serial number
		buf.WriteBytes(kit.ParseHex("0" + m.UniqueID(c.DeviceID)))
		buf.WriteByte8(modeSetting)
		buf.WriteBytes(kit.ASCIIBytes(content))
		return buf
	}
	return nil
}
