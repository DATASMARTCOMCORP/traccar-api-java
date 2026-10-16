// Package teltonika is the Teltonika protocol (TeltonikaProtocol): binary AVL codecs (GH3000,
// 8, 8 extended, 12, 13, 16) over TCP (after an IMEI handshake) and UDP.
package teltonika

import (
	"regexp"
	"strings"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/config"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: a TCP and a UDP server. The decoder reads
// teltonika.extended.
func Protocol(cfg *config.Config) (ingest.Protocol, error) {
	extended := cfg.Bool("teltonika.extended", false)
	return ingest.Protocol{
		Name:         "teltonika",
		DataCommands: protocol.DataCommands("teltonika"),
		Servers: []ingest.Server{
			{NewPipeline: func() ingest.Pipeline {
				return ingest.Pipeline{
					Framer:  Framer(),
					Decoder: &Decoder{Extended: extended},
					Encoder: ingest.CommandEncoderFunc(encode),
				}
			}},
			{Datagram: true, NewPipeline: func() ingest.Pipeline {
				return ingest.Pipeline{
					Decoder: &Decoder{Connectionless: true, Extended: extended},
					Encoder: ingest.CommandEncoderFunc(encode),
				}
			}},
		},
	}, nil
}

// Framer is TeltonikaFrameDecoder: a single 0xFF ping, an identification (a length
// prefix), or an AVL packet (a zero preamble and a data length).
func Framer() ingest.Framer {
	return &ingest.BaseFramer{MaxLength: ingest.MaxFrameLengthLarge, Decode: func(_ *ingest.Message, buf *kit.Buf) any {
		if buf.IsReadable() && buf.GetByte(buf.ReaderIndex()) == -1 {
			return buf.ReadSlice(1)
		}
		if buf.ReadableBytes() < 12 {
			return nil
		}
		length := buf.GetUnsignedShort(buf.ReaderIndex())
		if length > 0 {
			if buf.ReadableBytes() >= length+2 {
				return buf.ReadSlice(length + 2)
			}
		} else {
			dataLength := int(buf.GetInt(buf.ReaderIndex() + 4))
			if buf.ReadableBytes() >= dataLength+12 {
				return buf.ReadSlice(dataLength + 12)
			}
		}
		return nil
	}}
}

const (
	imagePacketMax = 2048

	codecGH3000 = 0x07
	codec8      = 0x08
	codec8Ext   = 0x8E
	codec12     = 0x0C
	codec13     = 0x0D
	codec16     = 0x10
)

// Decoder is TeltonikaProtocolDecoder. Extended is teltonika.extended (setExtended);
// ModelOverride is BaseProtocolDecoder.setModelOverride (used by the Java tests).
type Decoder struct {
	Connectionless bool
	Extended       bool
	ModelOverride  *string
	photo          *kit.MediaBuffer
}

func (d *Decoder) parseIdentification(m *ingest.Message, buf *kit.Buf) {
	length := buf.ReadUnsignedShort()
	imei := buf.GetString(buf.ReaderIndex(), length)
	ds := m.Session(imei)

	if m.HasChannel() {
		response := kit.Empty()
		if ds != nil {
			response.WriteByte8(1)
		} else {
			response.WriteByte8(0)
		}
		m.Send(response)
	}
}

func sendImageRequest(m *ingest.Message, id int64, offset, size int) {
	if !m.HasChannel() {
		return
	}
	response := kit.Empty()
	response.WriteInt(0)
	response.WriteShort(0)
	response.WriteShort(19) // length
	response.WriteByte8(codec12)
	response.WriteByte8(1)    // nod
	response.WriteByte8(0x0D) // camera
	response.WriteInt(11)     // payload length
	response.WriteByte8(2)    // command
	response.WriteInt(id)
	response.WriteInt(int64(offset))
	response.WriteShort(size)
	response.WriteByte8(1) // nod
	response.WriteShort(0)
	data := response.Bytes()
	response.WriteShort(kit.CRC16(kit.CRC16_IBM, data[8:len(data)-2]))
	m.Send(response)
}

func (d *Decoder) decodeSerial(m *ingest.Message, ds *ingest.DeviceSession, pos *model.Position, buf *kit.Buf) {
	m.LastLocation(pos, nil)

	typ := buf.ReadUnsignedByte()
	if typ == 0x0D {

		buf.ReadInt() // length
		subtype := buf.ReadUnsignedByte()
		if subtype == 0x01 {

			photoID := buf.ReadUnsignedInt()
			d.photo = m.NewSizedMediaBuffer(int(buf.ReadInt()))
			sendImageRequest(m, photoID, 0, min(imagePacketMax, d.photo.Capacity))

		} else if subtype == 0x02 {

			photoID := buf.ReadUnsignedInt()
			buf.ReadInt() // offset
			if d.photo == nil {
				kit.Throw("NullPointerException: no media buffer")
			}
			d.photo.Write(buf.ReadBytes(buf.ReadUnsignedShort()))
			if d.photo.Writable() > 0 {
				sendImageRequest(m, photoID, len(d.photo.Data), min(imagePacketMax, d.photo.Writable()))
			} else {
				data := d.photo.Data
				d.photo = nil // writeMediaFile releases the buffer
				pos.Set(model.KeyImage, m.WriteMedia(ds.UniqueID, "jpg", data))
			}

		}

	} else {

		pos.Set(model.KeyType, typ)

		length := int(buf.ReadInt())
		if kit.IsPrintable(buf, length) {
			data := kit.Trim(buf.ReadString(length))
			if strings.HasPrefix(data, "UUUUww") && strings.HasSuffix(data, "SSS") {
				values := kit.Split(kit.Substring(data, 6, kit.Len(data)-4), ";")
				for i := 0; i < 8; i++ {
					pos.Set("axle"+kit.Format("%d", i+1), kit.Double(values[i]))
				}
				pos.Set("loadTruck", kit.Double(values[8]))
				pos.Set("loadTrailer", kit.Double(values[9]))
				pos.Set("totalTruck", kit.Double(values[10]))
				pos.Set("totalTrailer", kit.Double(values[11]))
			} else {
				pos.Set(model.KeyResult, data)
			}
		} else {
			pos.Set(model.KeyResult, buf.ReadSlice(length).HexDump())
		}
	}
}

func readValue(buf *kit.Buf, length int) int64 {
	switch length {
	case 1:
		return int64(buf.ReadUnsignedByte())
	case 2:
		return int64(buf.ReadUnsignedShort())
	case 4:
		return buf.ReadUnsignedInt()
	}
	return buf.ReadLong()
}

func decodeGh3000Parameter(pos *model.Position, id int, buf *kit.Buf, length int) {
	switch id {
	case 1:
		pos.Set(model.KeyBatteryLevel, readValue(buf, length))
	case 2:
		pos.Set("usbConnected", readValue(buf, length) == 1)
	case 5:
		pos.Set("uptime", readValue(buf, length))
	case 20:
		pos.Set(model.KeyHdop, float64(readValue(buf, length))/10.0)
	case 21:
		pos.Set(model.KeyVdop, float64(readValue(buf, length))/10.0)
	case 22:
		pos.Set(model.KeyPdop, float64(readValue(buf, length))/10.0)
	case 67:
		pos.Set(model.KeyBattery, float64(readValue(buf, length))/1000.0)
	case 221:
		pos.Set("button", readValue(buf, length))
	case 222:
		if readValue(buf, length) == 1 {
			pos.AddAlarm(model.AlarmSos)
		}
	case 240:
		pos.Set(model.KeyMotion, readValue(buf, length) == 1)
	case 244:
		pos.Set(model.KeyRoaming, readValue(buf, length) == 1)
	default:
		pos.Set(model.PrefixIo+kit.Format("%d", id), readValue(buf, length))
	}
}

func (d *Decoder) decodeParameter(pos *model.Position, id int, buf *kit.Buf, length, codec int, deviceModel string) {
	if codec == codecGH3000 {
		decodeGh3000Parameter(pos, id, buf, length)
		return
	}
	index := buf.ReaderIndex()
	if p, ok := parameters[id]; ok && p.match(deviceModel) {
		p.decode(pos, buf)
		buf.SetReaderIndex(index + length)
		return
	}
	pos.Set(model.PrefixIo+kit.Format("%d", id), readValue(buf, length))
}

// removeLong is ExtendedModel.removeLong: the value's longValue (or Long.parseLong of its
// text), nil when absent.
func removeLong(pos *model.Position, key string) *int64 {
	if !pos.Attributes.Has(key) {
		return nil
	}
	v, err := pos.Attributes.JavaLong(key)
	if err != nil {
		panic(err)
	}
	pos.Attributes.Delete(key)
	return &v
}

// removeInteger is removeInteger: intValue, the low 32 bits of the stored integer.
func removeInteger(pos *model.Position, key string) *int {
	v := removeLong(pos, key)
	if v == nil {
		return nil
	}
	i := int(int32(*v))
	return &i
}

func must[T any](v *T) T {
	if v == nil {
		kit.Throw("NullPointerException")
	}
	return *v
}

func (d *Decoder) decodeCell(m *ingest.Message, pos *model.Position, network *model.Network, mncKey, lacKey, cidKey, rssiKey string) {
	if !pos.Attributes.Has(mncKey) || !pos.Attributes.Has(lacKey) || !pos.Attributes.Has(cidKey) {
		return
	}
	mcc, err := m.Config().Int("geolocation.mcc", 0)
	if err != nil {
		panic(err)
	}
	mnc := must(removeInteger(pos, mncKey))
	lac := must(removeInteger(pos, lacKey))
	cid := must(removeLong(pos, cidKey))
	cellTower := model.NewCellTower(mcc, mnc, lac, cid)
	cellTower.SetSignalStrength(must(removeInteger(pos, rssiKey)))
	network.AddCellTower(cellTower)
}

func (d *Decoder) decodeNetwork(m *ingest.Message, pos *model.Position, deviceModel string) {
	if deviceModel == "TAT100" {
		network := model.NewNetwork()
		d.decodeCell(m, pos, network, "io1200", "io287", "io288", "io289")
		d.decodeCell(m, pos, network, "io1201", "io290", "io291", "io292")
		d.decodeCell(m, pos, network, "io1202", "io293", "io294", "io295")
		d.decodeCell(m, pos, network, "io1203", "io296", "io297", "io298")
		if network.CellTowers != nil {
			pos.Network = network
		}
		return
	}
	cid2g := removeInteger(pos, "cid2g")
	cid4g := removeLong(pos, "cid4g")
	lac := removeInteger(pos, "lac")
	if lac != nil && (cid2g != nil || cid4g != nil) {
		var cellTower model.CellTower
		if cid2g != nil {
			cellTower = m.CellTowerFromLacCid(*lac, int64(*cid2g))
		} else {
			// Java sets the radio type "lte" on a Network it then discards.
			cellTower = m.CellTowerFromLacCid(*lac, *cid4g)
		}
		operator, err := pos.Attributes.JavaLong(model.KeyOperator) // getInteger: intValue
		if err != nil {
			panic(err)
		}
		operator = int64(int32(operator))
		if operator >= 1000 {
			if err := cellTower.SetOperator(operator); err != nil {
				panic(err)
			}
		}
		pos.Network = model.NetworkOf(cellTower)
	}
}

func readExtByte(buf *kit.Buf, codec int, codecs ...int) int {
	for _, c := range codecs {
		if codec == c {
			return buf.ReadUnsignedShort()
		}
	}
	return buf.ReadUnsignedByte()
}

var fm6xx = regexp.MustCompile(`^FM.6..$`)

func (d *Decoder) decodeLocation(m *ingest.Message, pos *model.Position, buf *kit.Buf, codec int, deviceModel string) {
	globalMask := 0x0f

	if codec == codecGH3000 {

		t := buf.ReadUnsignedInt() & 0x3fffffff
		t += 1167609600 // 2007-01-01 00:00:00

		globalMask = buf.ReadUnsignedByte()
		if kit.Check(int64(globalMask), 0) {

			pos.SetTime(time.UnixMilli(t * 1000))

			locationMask := buf.ReadUnsignedByte()

			if kit.Check(int64(locationMask), 0) {
				pos.SetLatitude(float64(buf.ReadFloat()))
				pos.SetLongitude(float64(buf.ReadFloat()))
			}

			if kit.Check(int64(locationMask), 1) {
				pos.Altitude = float64(buf.ReadUnsignedShort())
			}

			if kit.Check(int64(locationMask), 2) {
				pos.Course = float64(buf.ReadUnsignedByte()) * 360.0 / 256
			}

			if kit.Check(int64(locationMask), 3) {
				pos.Speed = units.KnotsFromKph(float64(buf.ReadUnsignedByte()))
			}

			if kit.Check(int64(locationMask), 4) {
				pos.Set(model.KeySatellites, buf.ReadUnsignedByte())
			}

			if kit.Check(int64(locationMask), 5) {
				lac := buf.ReadUnsignedShort()
				cellTower := m.CellTowerFromLacCid(lac, int64(buf.ReadUnsignedShort()))

				if kit.Check(int64(locationMask), 6) {
					cellTower.SetSignalStrength(buf.ReadUnsignedByte())
				}

				if kit.Check(int64(locationMask), 7) {
					if err := cellTower.SetOperator(buf.ReadUnsignedInt()); err != nil {
						panic(err)
					}
				}

				pos.Network = model.NetworkOf(cellTower)

			} else {
				if kit.Check(int64(locationMask), 6) {
					pos.Set(model.KeyRssi, buf.ReadUnsignedByte())
				}
				if kit.Check(int64(locationMask), 7) {
					pos.Set(model.KeyOperator, buf.ReadUnsignedInt())
				}
			}

		} else {

			deviceTime := time.UnixMilli(t * 1000)
			m.LastLocation(pos, &deviceTime)

		}

	} else {

		pos.SetTime(time.UnixMilli(buf.ReadLong()))

		pos.Set("priority", buf.ReadUnsignedByte())

		pos.SetLongitude(float64(buf.ReadInt()) / 10000000.0)
		pos.SetLatitude(float64(buf.ReadInt()) / 10000000.0)
		pos.Altitude = float64(buf.ReadShort())
		pos.Course = float64(buf.ReadUnsignedShort())

		satellites := buf.ReadUnsignedByte()
		pos.Set(model.KeySatellites, satellites)

		pos.Valid = satellites != 0

		pos.Speed = units.KnotsFromKph(float64(buf.ReadUnsignedShort()))

		pos.Set(model.KeyEvent, readExtByte(buf, codec, codec8Ext, codec16))
		if codec == codec16 {
			buf.ReadUnsignedByte() // generation type
		}

		readExtByte(buf, codec, codec8Ext) // total IO data records

	}

	// 1, 2 and 4 byte data
	for i, size := range []int{1, 2, 4} {
		if kit.Check(int64(globalMask), i+1) {
			cnt := readExtByte(buf, codec, codec8Ext)
			for j := 0; j < cnt; j++ {
				d.decodeParameter(pos, readExtByte(buf, codec, codec8Ext, codec16), buf, size, codec, deviceModel)
			}
		}
	}

	// 8 byte data
	if codec == codec8 || codec == codec8Ext || codec == codec16 {
		cnt := readExtByte(buf, codec, codec8Ext)
		for j := 0; j < cnt; j++ {
			d.decodeParameter(pos, readExtByte(buf, codec, codec8Ext, codec16), buf, 8, codec, deviceModel)
		}
	}

	// 16 byte data
	if d.Extended {
		cnt := readExtByte(buf, codec, codec8Ext)
		for j := 0; j < cnt; j++ {
			id := readExtByte(buf, codec, codec8Ext, codec16)
			pos.Set(model.PrefixIo+kit.Format("%d", id), buf.ReadSlice(16).HexDump())
		}
	}

	// X byte data
	if codec == codec8Ext {
		cnt := buf.ReadUnsignedShort()
		for j := 0; j < cnt; j++ {
			id := buf.ReadUnsignedShort()
			length := buf.ReadUnsignedShort()
			switch id {
			case 256, 325:
				pos.Set(model.KeyVin, buf.ReadString(length))
			case 281:
				pos.Set(model.KeyDtcs, strings.ReplaceAll(buf.ReadString(length), ",", " "))
			case 385:
				decodeBeacons(pos, buf.ReadSlice(length))
			case 548, 10828, 10829, 10831, 11317:
				decodeTags(pos, buf.ReadSlice(length))
			default:
				pos.Set(model.PrefixIo+kit.Format("%d", id), buf.ReadSlice(length).HexDump())
			}
		}
	}

	d.decodeNetwork(m, pos, deviceModel)

	if fm6xx.MatchString(deviceModel) {
		driverMsb, hasMsb := pos.Attributes.Get("io195")
		driverLsb, hasLsb := pos.Attributes.Get("io196")
		if hasMsb && hasLsb {
			msb, ok1 := driverMsb.(int64)
			lsb, ok2 := driverLsb.(int64)
			if !ok1 || !ok2 {
				kit.Throw("ClassCastException: driver id is not a Long")
			}
			driver := kit.Empty()
			driver.WriteLong(msb)
			driver.WriteLong(lsb)
			pos.Set(model.KeyDriverUniqueID, kit.UTF8(driver.Bytes()))
		}
	}
}

func decodeBeacons(pos *model.Position, data *kit.Buf) {
	data.ReadUnsignedByte() // data part
	for index := 1; data.IsReadable(); index++ {
		prefix := "beacon" + kit.Format("%d", index)
		flags := data.ReadUnsignedByte()
		if kit.FromInt(flags, 4) > 0 {
			pos.Set(prefix+"Uuid", data.ReadSlice(16).HexDump())
			pos.Set(prefix+"Major", data.ReadUnsignedShort())
			pos.Set(prefix+"Minor", data.ReadUnsignedShort())
		} else {
			pos.Set(prefix+"Namespace", data.ReadSlice(10).HexDump())
			pos.Set(prefix+"Instance", data.ReadSlice(6).HexDump())
		}
		pos.Set(prefix+"Rssi", int(data.ReadSignedByte()))
		if kit.Check(int64(flags), 1) {
			pos.Set(prefix+"Battery", float64(data.ReadUnsignedShort())/100.0)
		}
		if kit.Check(int64(flags), 2) {
			pos.Set(prefix+"Temp", data.ReadUnsignedShort())
		}
	}
}

func decodeTags(pos *model.Position, data *kit.Buf) {
	data.ReadUnsignedByte() // header
	for i := 1; data.IsReadable(); i++ {
		prefix := "tag" + kit.Format("%d", i)
		beacon := data.ReadSlice(data.ReadUnsignedByte())
		for beacon.IsReadable() {
			parameterID := beacon.ReadUnsignedByte()
			parameterLength := beacon.ReadUnsignedByte()
			switch parameterID {
			case 0:
				pos.Set(prefix+"Rssi", int(beacon.ReadSignedByte()))
			case 1:
				pos.Set(prefix+"Id", beacon.ReadSlice(parameterLength).HexDump())
			case 2:
				beaconData := beacon.ReadSlice(parameterLength)
				flag := beaconData.ReadUnsignedByte()
				if kit.Check(int64(flag), 6) {
					pos.Set(prefix+"LowBattery", true)
				}
				if kit.Check(int64(flag), 7) {
					pos.Set(prefix+"Voltage", beaconData.ReadUnsignedByte()*10+2000)
				}
			case 5:
				pos.Set(prefix+"Name", kit.UTF8(beacon.ReadBytes(parameterLength)))
			case 6:
				pos.Set(prefix+"Temp", beacon.ReadShort())
			case 7:
				pos.Set(prefix+"Humidity", beacon.ReadUnsignedByte())
			case 8:
				pos.Set(prefix+"Magnet", beacon.ReadUnsignedByte() > 0)
			case 9:
				pos.Set(prefix+"Motion", beacon.ReadUnsignedByte() > 0)
			case 10:
				pos.Set(prefix+"MotionCount", beacon.ReadUnsignedShort())
			case 11:
				pos.Set(prefix+"Pitch", int(beacon.ReadSignedByte()))
			case 12:
				pos.Set(prefix+"AngleRoll", beacon.ReadShort())
			case 13:
				pos.Set(prefix+"LowBattery", beacon.ReadUnsignedByte())
			case 14:
				pos.Set(prefix+"Battery", beacon.ReadUnsignedShort())
			case 15:
				pos.Set(prefix+"Mac", beacon.ReadSlice(6).HexDump())
			default:
				beacon.Skip(parameterLength)
			}
		}
	}
}

func (d *Decoder) deviceModel(m *ingest.Message, ds *ingest.DeviceSession) string {
	if d.ModelOverride != nil {
		return *d.ModelOverride
	}
	return m.DeviceModel(ds)
}

func (d *Decoder) parseData(m *ingest.Message, buf *kit.Buf, locationPacketID int, imei ...string) any {
	positions := []*model.Position{}

	if !d.Connectionless {
		buf.ReadUnsignedInt() // data length
	}

	codec := buf.ReadUnsignedByte()
	count := buf.ReadUnsignedByte()

	ds := m.Session(imei...)
	if ds == nil {
		return nil
	}

	for i := 0; i < count; i++ {
		pos := m.NewPosition()

		pos.DeviceID = ds.DeviceID
		pos.Valid = true

		switch codec {
		case codec13:
			buf.ReadUnsignedByte() // type
			length := int(buf.ReadInt()) - 4
			deviceTime := time.UnixMilli(buf.ReadUnsignedInt() * 1000)
			m.LastLocation(pos, &deviceTime)
			if kit.IsPrintable(buf, length) {
				data := kit.Trim(buf.ReadString(length))
				if strings.HasPrefix(data, "GTSL") {
					pos.Set(model.KeyDriverUniqueID, kit.Split(data, "|")[4])
				} else {
					pos.Set(model.KeyResult, data)
				}
			} else {
				pos.Set(model.KeyResult, buf.ReadSlice(length).HexDump())
			}
		case codec12:
			d.decodeSerial(m, ds, pos, buf)
		default:
			d.decodeLocation(m, pos, buf, codec, d.deviceModel(m, ds))
		}

		if !pos.Outdated || pos.Attributes.Len() > 0 {
			positions = append(positions, pos)
		}
	}

	if m.HasChannel() && codec != codec12 && codec != codec13 {
		response := kit.Empty()
		if d.Connectionless {
			response.WriteShort(5)
			response.WriteShort(0)
			response.WriteByte8(0x01)
			response.WriteByte8(locationPacketID)
			response.WriteByte8(count)
		} else {
			response.WriteInt(int64(count))
		}
		m.Send(response)
	}

	if len(positions) == 0 {
		return nil
	}
	return positions
}

// Decode decodes one frame.
func (d *Decoder) Decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)
	if d.Connectionless {
		return d.decodeUDP(m, buf)
	}
	return d.decodeTCP(m, buf)
}

func (d *Decoder) decodeTCP(m *ingest.Message, buf *kit.Buf) any {
	if buf.ReadableBytes() == 1 && buf.ReadUnsignedByte() == 0xff {
		return nil
	} else if buf.GetUnsignedShort(0) > 0 {
		d.parseIdentification(m, buf)
	} else {
		buf.Skip(4)
		return d.parseData(m, buf, 0)
	}
	return nil
}

func (d *Decoder) decodeUDP(m *ingest.Message, buf *kit.Buf) any {
	buf.ReadUnsignedShort() // length
	buf.ReadUnsignedShort() // packet id
	buf.ReadUnsignedByte()  // packet type
	locationPacketID := buf.ReadUnsignedByte()
	imei := buf.ReadString(buf.ReadUnsignedShort())

	return d.parseData(m, buf, locationPacketID, imei)
}
