// Package castel is the Castel protocol (CastelProtocol): binary "@@" messages of the SC,
// CC and MPIP families over TCP and UDP.
package castel

import (
	"encoding/hex"
	"strconv"
	"strings"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: a TCP and a UDP server.
func Protocol() ingest.Protocol {
	pipeline := func(framer bool) func() ingest.Pipeline {
		return func() ingest.Pipeline {
			pl := ingest.Pipeline{Decoder: ingest.DecoderFunc(decode), Encoder: ingest.CommandEncoderFunc(encode)}
			if framer {
				f := ingest.NewLengthFramer(ingest.MaxFrameLength, 2, 2, -4, 0)
				f.LittleEndian = true
				pl.Framer = f
			}
			return pl
		}
	}
	return ingest.Protocol{
		Name:         "castel",
		DataCommands: protocol.DataCommands("castel"),
		Servers:      []ingest.Server{{NewPipeline: pipeline(true)}, {Datagram: true, NewPipeline: pipeline(false)}},
	}
}

var pidLengths = map[int]int{}

func addPidLength(length int, pids ...int) {
	for _, pid := range pids {
		pidLengths[pid] = length
	}
}

func init() {
	addPidLength(1,
		0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0b, 0x0d,
		0x0e, 0x0f, 0x11, 0x12, 0x13, 0x1c, 0x1d, 0x1e, 0x2c,
		0x2d, 0x2e, 0x2f, 0x30, 0x33, 0x43, 0x45, 0x46,
		0x47, 0x48, 0x49, 0x4a, 0x4b, 0x4c, 0x51, 0x52,
		0x5a)
	addPidLength(2,
		0x02, 0x03, 0x0a, 0x0c, 0x10, 0x14, 0x15, 0x16,
		0x17, 0x18, 0x19, 0x1a, 0x1b, 0x1f, 0x21, 0x22,
		0x23, 0x31, 0x32, 0x3c, 0x3d, 0x3e, 0x3f, 0x42,
		0x44, 0x4d, 0x4e, 0x50, 0x53, 0x54, 0x55, 0x56,
		0x57, 0x58, 0x59, 0x9d)
	addPidLength(4,
		0x00, 0x01, 0x20, 0x24, 0x25, 0x26, 0x27, 0x28,
		0x29, 0x2a, 0x2b, 0x34, 0x35, 0x36, 0x37, 0x38,
		0x39, 0x3a, 0x3b, 0x40, 0x41, 0x4f, 0x67, 0xa6)
}

// short is a Java short constant: message types are compared as signed 16-bit values.
func short(v uint16) int { return int(int16(v)) }

var (
	msgScLogin           = short(0x1001)
	msgScLoginResponse   = short(0x9001)
	msgScLogout          = short(0x1002)
	msgScHeartbeat       = short(0x1003)
	msgScHeartbeatResp   = short(0x9003)
	msgScGPS             = short(0x4001)
	msgScPidData         = short(0x4002)
	msgScGSensor         = short(0x4003)
	msgScSupportedPid    = short(0x4004)
	msgScObdData         = short(0x4005)
	msgScDtcsPassenger   = short(0x4006)
	msgScDtcsCommercial  = short(0x400B)
	msgScAlarm           = short(0x4007)
	msgScAlarmResponse   = short(0xC007)
	msgScCell            = short(0x4008)
	msgScGPSSleep        = short(0x4009)
	msgScFuel            = short(0x400E)
	msgScComprehensive   = short(0x401F)
	msgScAgpsRequest     = short(0x5101)
	msgScQueryResponse   = short(0xA002)
	msgScCurrentLocation = short(0xB001)
	msgCcLogin           = short(0x4001)
	msgCcLoginResponse   = short(0x8001)
	msgCcHeartbeat       = short(0x4206)
	msgCcPetrolControl   = short(0x4583)
	msgCcHeartbeatResp   = short(0x8206)
)

func readPosition(m *ingest.Message, ds *ingest.DeviceSession, buf *kit.Buf) *model.Position {
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	date := kit.NewDateBuilder(nil).
		SetDateReverse(buf.ReadUnsignedByte(), buf.ReadUnsignedByte(), buf.ReadUnsignedByte()).
		SetTime(buf.ReadUnsignedByte(), buf.ReadUnsignedByte(), buf.ReadUnsignedByte())
	pos.SetTime(date.Date())

	lat := float64(buf.ReadUnsignedIntLE()) / 3600000.0
	lon := float64(buf.ReadUnsignedIntLE()) / 3600000.0
	pos.Speed = units.KnotsFromCps(float64(buf.ReadUnsignedShortLE()))
	pos.Course = float64(buf.ReadUnsignedShortLE()) / 10.0

	flags := buf.ReadUnsignedByte()
	if flags&0x02 == 0 {
		lat = -lat
	}
	if flags&0x01 == 0 {
		lon = -lon
	}
	pos.SetLatitude(lat)
	pos.SetLongitude(lon)
	pos.Valid = flags&0x0C > 0
	pos.Set(model.KeySatellites, flags>>4)
	return pos
}

func createPosition(m *ingest.Message, ds *ingest.DeviceSession) *model.Position {
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID
	m.LastLocation(pos, nil)
	return pos
}

func decodeObd(pos *model.Position, buf *kit.Buf, groups bool) {
	count := buf.ReadUnsignedByte()
	pids := make([]int, count)
	for i := range pids {
		pids[i] = buf.ReadUnsignedShortLE() & 0xff
	}
	if groups {
		buf.ReadUnsignedByte() // group count
		buf.ReadUnsignedByte() // group size
	}
	for _, pid := range pids {
		length, ok := pidLengths[pid]
		if !ok {
			kit.Throw("RuntimeException: %s", kit.Format("Unknown PID 0x%02x", pid))
		}
		var value int
		switch length {
		case 1:
			value = buf.ReadUnsignedByte()
		case 2:
			value = buf.ReadUnsignedShortLE()
		case 4:
			value = int(buf.ReadIntLE())
		}
		kit.ObdDecodeData(pid, int64(value), false).Add(pos)
	}
}

func decodeStat(pos *model.Position, buf *kit.Buf) {
	buf.ReadUnsignedIntLE() // ACC ON time
	buf.ReadUnsignedIntLE() // UTC time
	pos.Set(model.KeyOdometer, buf.ReadUnsignedIntLE())
	pos.Set(model.KeyOdometerTrip, buf.ReadUnsignedIntLE())
	pos.Set(model.KeyFuelConsumption, buf.ReadUnsignedIntLE())
	buf.ReadUnsignedShortLE() // current fuel consumption

	state := buf.ReadUnsignedIntLE()
	if kit.Check(state, 4) {
		pos.AddAlarm(model.AlarmAcceleration)
	}
	if kit.Check(state, 5) {
		pos.AddAlarm(model.AlarmBraking)
	}
	if kit.Check(state, 6) {
		pos.AddAlarm(model.AlarmIdle)
	}
	pos.Set(model.KeyIgnition, kit.Check(state, 2*8+2))
	pos.Set(model.KeyStatus, state)

	buf.Skip(8)
}

func sendResponse(m *ingest.Message, version int, id []byte, typ int, content *kit.Buf) {
	if !m.HasChannel() {
		return
	}
	length := 2 + 2 + 1 + len(id) + 2 + 2 + 2
	if content != nil {
		length += content.ReadableBytes()
	}
	response := kit.Empty()
	response.WriteByte8('@')
	response.WriteByte8('@')
	response.WriteShortLE(length)
	response.WriteByte8(version)
	response.WriteBytes(id)
	response.WriteShort(typ)
	if content != nil {
		response.WriteBuf(content)
	}
	response.WriteShortLE(kit.CRC16(kit.CRC16_X25, response.Bytes()))
	response.WriteByte8(0x0D)
	response.WriteByte8(0x0A)
	m.Send(response)
}

func sendMpipResponse(m *ingest.Message, id []byte, typ int) {
	length := 2 + 2 + len(id) + 2 + 4 + 8 + 2 + 2
	response := kit.Empty()
	response.WriteByte8('@')
	response.WriteByte8('@')
	response.WriteShortLE(length)
	response.WriteBytes(id)
	response.WriteShort(typ)
	response.WriteIntLE(0)
	for i := 0; i < 8; i++ {
		response.WriteByte8(0xff)
	}
	response.WriteShortLE(kit.CRC16(kit.CRC16_X25, response.Bytes()))
	response.WriteByte8(0x0D)
	response.WriteByte8(0x0A)
	m.Send(response)
}

func decodeAlarm(pos *model.Position, alarm int) {
	switch alarm {
	case 0x01:
		pos.AddAlarm(model.AlarmOverspeed)
	case 0x02:
		pos.AddAlarm(model.AlarmLowPower)
	case 0x03:
		pos.AddAlarm(model.AlarmTemperature)
	case 0x04:
		pos.AddAlarm(model.AlarmAcceleration)
	case 0x05:
		pos.AddAlarm(model.AlarmBraking)
	case 0x06:
		pos.AddAlarm(model.AlarmIdle)
	case 0x07:
		pos.AddAlarm(model.AlarmTow)
	case 0x08:
		pos.AddAlarm(model.AlarmHighRpm)
	case 0x09:
		pos.AddAlarm(model.AlarmPowerOn)
	case 0x0B:
		pos.AddAlarm(model.AlarmLaneChange)
	case 0x0C:
		pos.AddAlarm(model.AlarmCornering)
	case 0x0D:
		pos.AddAlarm(model.AlarmFatigueDriving)
	case 0x0E:
		pos.AddAlarm(model.AlarmPowerOff)
	case 0x11:
		pos.AddAlarm(model.AlarmAccident)
	case 0x12:
		pos.AddAlarm(model.AlarmTampering)
	case 0x16:
		pos.Set(model.KeyIgnition, true)
	case 0x17:
		pos.Set(model.KeyIgnition, false)
	case 0x1C:
		pos.AddAlarm(model.AlarmVibration)
	}
}

// cellTower is CellTower.fromLacCid(config, lac, cid).
func cellTower(m *ingest.Message, lac int, cid int64) model.CellTower {
	mcc, err := m.Config().Int("geolocation.mcc", 0)
	if err != nil {
		panic(err)
	}
	mnc, err := m.Config().Int("geolocation.mnc", 0)
	if err != nil {
		panic(err)
	}
	return model.NewCellTower(mcc, mnc, lac, cid)
}

func decodeComprehensive(pos *model.Position, buf *kit.Buf) {
	for buf.ReadableBytes() > 4 {
		tag := buf.ReadUnsignedShortLE()
		length := buf.ReadUnsignedShortLE()
		switch tag {
		case 0x0002:
			pidCount := buf.ReadUnsignedByte()
			for i := 0; i < pidCount; i++ {
				pidTag := buf.ReadUnsignedShortLE()
				pidLength := buf.ReadUnsignedShortLE()
				pos.Set("pid"+strconv.Itoa(pidTag), hex.EncodeToString(buf.ReadBytes(pidLength)))
			}
		case 0x0004, 0x0005:
			buf.Skip(length) // supported data streams, snapshot data
		case 0x0006:
			buf.ReadUnsignedByte() // fault flag
			faultCount := buf.ReadUnsignedByte()
			for i := 1; i <= faultCount; i++ {
				pos.Set("fault"+strconv.Itoa(i), buf.ReadUnsignedShortLE())
			}
		case 0x0007:
			buf.ReadUnsignedIntLE() // alarm index
			alarmCount := buf.ReadUnsignedByte()
			for i := 0; i < alarmCount; i++ {
				alarmFlag := buf.ReadUnsignedByte()
				event := buf.ReadUnsignedByte()
				if alarmFlag > 0 {
					decodeAlarm(pos, event)
				}
				buf.ReadUnsignedShortLE() // description
				buf.ReadUnsignedShortLE() // threshold
			}
		case 0x000B:
			buf.ReadUnsignedByte() // fault flag
			faultCount := buf.ReadUnsignedByte()
			for i := 1; i <= faultCount; i++ {
				pos.Set("fault"+strconv.Itoa(i), buf.ReadUnsignedIntLE())
			}
			buf.ReadUnsignedShortLE() // mil status
		case 0x0010:
			pos.Set(model.KeyDeviceTemp, float64(buf.ReadShortLE())/10.0)
		case 0x0011, 0x0012, 0x0013, 0x0014:
			pos.Set(model.PrefixTemp+strconv.Itoa(tag-0x0010), float64(buf.ReadShortLE())/10.0)
		case 0x0020:
			pos.Set(model.KeyPower, float64(buf.ReadUnsignedShortLE())/100.0)
		case 0x0021:
			pos.Set(model.KeyBattery, float64(buf.ReadUnsignedShortLE())/100.0)
		default:
			buf.Skip(length)
		}
	}
}

func decodeSc(m *ingest.Message, buf *kit.Buf, version int, id []byte, typ int, ds *ingest.DeviceSession) any {
	switch typ {
	case msgScHeartbeat:
		sendResponse(m, version, id, msgScHeartbeatResp, nil)
		return nil

	case msgScLogin, msgScLogout, msgScGPS, msgScAlarm, msgScCurrentLocation, msgScFuel, msgScComprehensive:
		switch typ {
		case msgScLogin:
			response := kit.Empty()
			response.WriteIntLE(0xFFFFFFFF)
			response.WriteShortLE(0)
			response.WriteIntLE(m.Now.UnixMilli() / 1000)
			sendResponse(m, version, id, msgScLoginResponse, response)
		case msgScGPS, msgScComprehensive:
			buf.ReadUnsignedByte() // historical
			if typ == msgScComprehensive {
				buf.ReadUnsignedIntLE() // index
			}
		case msgScAlarm:
			response := kit.Empty()
			response.WriteIntLE(int64(buf.ReadIntLE())) // alarm index
			sendResponse(m, version, id, msgScAlarmResponse, response)
		case msgScCurrentLocation:
			buf.ReadUnsignedShortLE()
		}

		buf.ReadUnsignedIntLE() // ACC ON time
		buf.ReadUnsignedIntLE() // UTC time
		odometer := buf.ReadUnsignedIntLE()
		tripOdometer := buf.ReadUnsignedIntLE()
		fuelConsumption := buf.ReadUnsignedIntLE()
		buf.ReadUnsignedShortLE() // current fuel consumption
		status := buf.ReadUnsignedIntLE()
		buf.Skip(8)

		count := buf.ReadUnsignedByte()

		var positions []*model.Position
		var pos *model.Position
		for i := 0; i < count; i++ {
			pos = readPosition(m, ds, buf)
			pos.Set(model.KeyOdometer, odometer)
			pos.Set(model.KeyOdometerTrip, tripOdometer)
			pos.Set(model.KeyFuelConsumption, fuelConsumption)
			pos.Set(model.KeyStatus, status)
			positions = append(positions, pos)
		}

		switch typ {
		case msgScAlarm:
			alarmCount := buf.ReadUnsignedByte()
			for i := 0; i < alarmCount; i++ {
				if buf.ReadUnsignedByte() != 0 {
					event := buf.ReadUnsignedByte()
					for _, p := range positions {
						decodeAlarm(p, event)
					}
					buf.ReadUnsignedShortLE() // description
					buf.ReadUnsignedShortLE() // threshold
				}
			}
		case msgScFuel:
			for _, p := range positions {
				p.Set(model.PrefixAdc+"1", buf.ReadUnsignedShortLE())
			}
		case msgScComprehensive:
			if pos == nil {
				// as in Java, this position is not returned
				pos = createPosition(m, ds)
			}
			decodeComprehensive(pos, buf)
		}

		if len(positions) == 0 {
			return nil
		}
		return positions

	case msgScGPSSleep:
		buf.ReadUnsignedIntLE() // device time
		return readPosition(m, ds, buf)

	case msgScAgpsRequest:
		return readPosition(m, ds, buf)

	case msgScPidData:
		pos := createPosition(m, ds)
		decodeStat(pos, buf)
		buf.ReadUnsignedShortLE() // sample rate
		decodeObd(pos, buf, true)
		return pos

	case msgScGSensor:
		pos := createPosition(m, ds)
		decodeStat(pos, buf)
		buf.ReadUnsignedShortLE() // sample rate

		count := buf.ReadUnsignedByte()
		var data strings.Builder
		data.WriteString("[")
		for i := 0; i < count; i++ {
			if i > 0 {
				data.WriteString(",")
			}
			data.WriteString("[")
			data.WriteString(model.JavaToString(float64(buf.ReadShortLE()) * 0.015625))
			data.WriteString(",")
			data.WriteString(model.JavaToString(float64(buf.ReadShortLE()) * 0.015625))
			data.WriteString(",")
			data.WriteString(model.JavaToString(float64(buf.ReadShortLE()) * 0.015625))
			data.WriteString("]")
		}
		data.WriteString("]")
		pos.Set(model.KeyGSensor, data.String())
		return pos

	case msgScDtcsPassenger, msgScDtcsCommercial:
		pos := createPosition(m, ds)
		decodeStat(pos, buf)
		buf.ReadUnsignedByte() // flag

		count := buf.ReadUnsignedByte()
		var codes strings.Builder
		for i := 0; i < count; i++ {
			codes.WriteString(kit.ObdDecodeCode(buf.ReadUnsignedShortLE()))
			if typ == msgScDtcsCommercial {
				buf.ReadUnsignedByte() // attribute
				buf.ReadUnsignedByte() // occurrence
			}
			codes.WriteByte(' ')
		}
		pos.Set(model.KeyDtcs, kit.Trim(codes.String()))
		return pos

	case msgScObdData:
		pos := createPosition(m, ds)
		decodeStat(pos, buf)
		buf.ReadUnsignedByte() // flag
		decodeObd(pos, buf, false)
		return pos

	case msgScCell:
		pos := createPosition(m, ds)
		decodeStat(pos, buf)
		lac := buf.ReadUnsignedShortLE()
		cid := buf.ReadUnsignedShortLE()
		pos.Network = model.NetworkOf(cellTower(m, lac, int64(cid)))
		return pos

	case msgScQueryResponse:
		pos := createPosition(m, ds)

		buf.ReadUnsignedShortLE() // index
		buf.ReadUnsignedByte()    // response count
		buf.ReadUnsignedByte()    // response index

		failureCount := buf.ReadUnsignedByte()
		for i := 0; i < failureCount; i++ {
			buf.ReadUnsignedShortLE() // tag
		}
		successCount := buf.ReadUnsignedByte()
		for i := 0; i < successCount; i++ {
			buf.ReadUnsignedShortLE() // tag
			pos.Set(model.KeyResult, buf.ReadString(buf.ReadUnsignedShortLE()))
		}
		return pos
	}
	return nil
}

// ccStatus reads the status block after a CC position.
func ccStatus(pos *model.Position, buf *kit.Buf) {
	pos.Set(model.KeyStatus, buf.ReadUnsignedIntLE())
	pos.Set(model.KeyBattery, buf.ReadUnsignedByte())
	pos.Set(model.KeyOdometer, buf.ReadUnsignedIntLE())

	buf.ReadUnsignedByte() // geo-fencing id
	buf.ReadUnsignedByte() // geo-fencing flags
	buf.ReadUnsignedByte() // additional flags
}

func decodeCc(m *ingest.Message, buf *kit.Buf, version int, id []byte, typ int, ds *ingest.DeviceSession) any {
	switch typ {
	case msgCcHeartbeat:
		sendResponse(m, version, id, msgCcHeartbeatResp, nil)

		buf.ReadUnsignedByte() // 0x01 for history
		count := buf.ReadUnsignedByte()

		positions := []*model.Position{}
		for i := 0; i < count; i++ {
			pos := readPosition(m, ds, buf)
			ccStatus(pos, buf)
			lac := buf.ReadUnsignedShortLE()
			cid := buf.ReadUnsignedShortLE()
			pos.Network = model.NetworkOf(cellTower(m, lac, int64(cid)))
			positions = append(positions, pos)
		}
		return positions

	case msgCcLogin:
		sendResponse(m, version, id, msgCcLoginResponse, nil)

		pos := readPosition(m, ds, buf)
		ccStatus(pos, buf)
		// GSM_CELL_CODE
		// STR_Z - firmware version
		// STR_Z - hardware version
		return pos
	}
	return nil
}

func decodeMpip(m *ingest.Message, buf *kit.Buf, version int, id []byte, typ int, ds *ingest.DeviceSession) any {
	switch typ {
	case 0x4001:
		sendResponse(m, version, id, typ, nil)
		return readPosition(m, ds, buf)

	case 0x2001:
		sendMpipResponse(m, id, 0x1001)
		buf.ReadUnsignedIntLE() // index
		buf.ReadUnsignedIntLE() // unix time
		buf.ReadUnsignedByte()
		return readPosition(m, ds, buf)

	case 0x4201, 0x4202, 0x4206:
		return readPosition(m, ds, buf)

	case 0x4204:
		positions := []*model.Position{}
		for i := 0; i < 8; i++ {
			pos := readPosition(m, ds, buf)
			buf.Skip(31)
			positions = append(positions, pos)
		}
		return positions
	}
	return nil
}

func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	header := buf.ReadUnsignedShortLE()
	buf.ReadUnsignedShortLE() // length

	version := -1
	if header == 0x4040 {
		version = buf.ReadUnsignedByte()
	}

	id := buf.ReadBytes(20)
	typ := buf.ReadShort()

	ds := m.Session(kit.Trim(kit.Latin1(id)))
	if ds == nil {
		return nil
	}

	switch version {
	case -1:
		return decodeMpip(m, buf, version, id, typ, ds)
	case 3, 4:
		return decodeSc(m, buf, version, id, typ, ds)
	}
	return decodeCc(m, buf, version, id, typ, ds)
}

func encodeContent(m *ingest.Message, deviceID int64, typ int, content *kit.Buf) *kit.Buf {
	buf := kit.Empty()
	uniqueID := m.UniqueID(deviceID)

	buf.WriteByte8('@')
	buf.WriteByte8('@')

	buf.WriteShortLE(2 + 2 + 1 + 20 + 2 + content.ReadableBytes() + 2 + 2) // length

	buf.WriteByte8(1) // protocol version

	buf.WriteBytes(kit.ASCIIBytes(uniqueID))
	buf.WriteZero(20 - kit.Len(uniqueID))

	buf.WriteShort(typ)
	buf.WriteBuf(content)

	buf.WriteShortLE(kit.CRC16(kit.CRC16_X25, buf.Bytes()))

	buf.WriteByte8('\r')
	buf.WriteByte8('\n')
	return buf
}

// encode is CastelProtocolEncoder.
func encode(m *ingest.Message, c *model.Command) any {
	content := kit.Empty()
	switch c.Type {
	case model.CommandEngineStop:
		content.WriteByte8(1)
		return encodeContent(m, c.DeviceID, msgCcPetrolControl, content)
	case model.CommandEngineResume:
		content.WriteByte8(0)
		return encodeContent(m, c.DeviceID, msgCcPetrolControl, content)
	}
	return nil
}
