// Package suntech is the Suntech protocol (SuntechProtocol): ST300/ST600/ST4955/ST910
// text reports, the "universal" formats and their binary and zip encodings.
//
// The binary decoder keeps SIR's fix for two's-complement coordinates (MIGRATION.md
// §8): devices that send latitude and longitude as two's complement are read as such,
// and signed magnitude is the fallback when that reading is out of range.
package suntech

import (
	"encoding/hex"
	"strings"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name:         "suntech",
		DataCommands: protocol.DataCommands("suntech"),
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			d := &Decoder{}
			return ingest.Pipeline{
				Framer:  Framer(),
				TextOut: true,
				Decoder: d,
				Encoder: &Encoder{Decoder: d},
			}
		}}},
	}
}

// Framer is SuntechFrameDecoder: zip (0x02) and binary frames carry their length, text
// frames end with '\r' not followed by '\n'.
func Framer() ingest.Framer {
	return &ingest.BaseFramer{Decode: func(_ *ingest.Message, buf *kit.Buf) any {
		if buf.GetByte(buf.ReaderIndex()) == 0x02 {
			length := 1 + 2 + buf.GetShort(buf.ReaderIndex()+1) + 1
			if buf.ReadableBytes() >= length {
				return buf.ReadSlice(length)
			}
		} else if buf.GetByte(buf.ReaderIndex()+1) == 0 {
			length := 1 + 2 + buf.GetShort(buf.ReaderIndex()+1)
			if buf.ReadableBytes() >= length {
				return buf.ReadSlice(length)
			}
		} else {
			delimiter := buf.IndexOf(buf.ReaderIndex(), buf.WriterIndex(), '\r')
			for delimiter > 0 {
				if delimiter+1 < buf.WriterIndex() && buf.GetByte(delimiter+1) == '\n' {
					delimiter = buf.IndexOf(delimiter+1, buf.WriterIndex(), '\r')
				} else {
					frame := buf.ReadSlice(delimiter - buf.ReaderIndex())
					buf.Skip(1)
					return frame
				}
			}
		}
		return nil
	}}
}

// Decoder is SuntechProtocolDecoder. The exported fields are the defaults of settings
// that the suntech.protocolType, suntech.hbm and suntech.include* keys (config or device
// attributes) override.
type Decoder struct {
	ProtocolType int
	HBM          int
	IncludeADC   bool
	IncludeRPM   bool
	IncludeTemp  bool

	// Seen by the encoder of the same connection.
	universal bool
	prefix    string
	hasPrefix bool

	crash *kit.Buf
}

var dateFormat = kit.NewDateFormat("yyyyMMddHH:mm:ss", time.UTC)

func (d *Decoder) protocolType(m *ingest.Message, deviceID int64) int {
	if v := m.LookupInt(deviceID, "suntech.protocolType", false, true); v != nil {
		return *v
	}
	return d.ProtocolType
}

func (d *Decoder) hbm(m *ingest.Message, deviceID int64) int {
	if v := m.LookupInt(deviceID, "suntech.hbm", false, true); v != nil {
		return *v
	}
	return d.HBM
}

func flag(m *ingest.Message, deviceID int64, suffix string, def bool) bool {
	if v := m.LookupBool(deviceID, m.Protocol()+suffix, false, true); v != nil {
		return *v
	}
	return def
}

func (d *Decoder) Decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)
	if buf.GetByte(buf.ReaderIndex()) == 0x02 {
		return d.decodeZip(m, buf)
	} else if buf.GetByte(buf.ReaderIndex()+1) == 0 {
		d.universal = true
		return d.decodeBinary(m, buf)
	}
	values := strings.Split(kit.Latin1(buf.Bytes()), ";") // toString(US_ASCII)
	d.prefix, d.hasPrefix = values[0], true
	switch {
	case d.prefix == "CRR":
		return d.decodeCrashReport(m, buf)
	case kit.Len(d.prefix) < 5:
		d.universal = true
		return d.decodeUniversal(m, values)
	case strings.HasSuffix(d.prefix, "HTE"):
		return decodeTravelReport(m, values)
	case strings.HasPrefix(d.prefix, "ST9"):
		return d.decode9(m, values)
	case strings.HasPrefix(d.prefix, "ST4"):
		return decode4(m, values)
	case strings.HasPrefix(d.prefix, "S"):
		return d.decode2356(m, kit.Substring(d.prefix, 0, 5), values)
	}
	return nil
}

// fields reads values in order; reading past the end panics, as Java's array access.
type fields struct {
	values []string
	i      int
}

func (f *fields) next() string {
	v := f.values[f.i]
	f.i++
	return v
}

func (f *fields) more() bool { return f.i < len(f.values) }

func (d *Decoder) decode9(m *ingest.Message, values []string) any {
	f := &fields{values: values, i: 1}
	typ := f.next()
	if typ != "Location" && typ != "Emergency" && typ != "Alert" {
		return nil
	}
	ds := m.Session(f.next())
	if ds == nil {
		return nil
	}
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID
	if typ == "Emergency" || typ == "Alert" {
		pos.AddAlarm(model.AlarmGeneral)
	}
	if typ != "Alert" || d.protocolType(m, ds.DeviceID) == 0 {
		pos.Set(model.KeyVersionFw, f.next())
	}
	pos.SetTime(dateFormat.Parse(f.next() + f.next()))
	if d.protocolType(m, ds.DeviceID) == 1 {
		f.i++ // cell
	}
	pos.SetLatitude(kit.Double(f.next()))
	pos.SetLongitude(kit.Double(f.next()))
	pos.Speed = units.KnotsFromKph(kit.Double(f.next()))
	pos.Course = kit.Double(f.next())
	pos.Valid = f.next() == "1"
	if d.protocolType(m, ds.DeviceID) == 1 {
		pos.Set(model.KeyOdometer, kit.Int(f.next()))
	}
	return pos
}

func decodeEmergency(value int) string {
	switch value {
	case 1:
		return model.AlarmSos
	case 2:
		return model.AlarmParking
	case 3:
		return model.AlarmPowerCut
	case 5, 6:
		return model.AlarmDoor
	case 7:
		return model.AlarmMovement
	case 8:
		return model.AlarmVibration
	}
	return ""
}

func decodeAlert(value int) string {
	switch value {
	case 1:
		return model.AlarmOverspeed
	case 5:
		return model.AlarmGeofenceExit
	case 6:
		return model.AlarmGeofenceEnter
	case 14:
		return model.AlarmLowBattery
	case 15:
		return model.AlarmVibration
	case 16:
		return model.AlarmAccident
	case 40:
		return model.AlarmPowerRestored
	case 41:
		return model.AlarmPowerCut
	case 42:
		return model.AlarmSos
	case 46:
		return model.AlarmAcceleration
	case 47:
		return model.AlarmBraking
	case 50:
		return model.AlarmJamming
	case 132:
		return model.AlarmDoor
	}
	return ""
}

func decode4(m *ingest.Message, values []string) any {
	f := &fields{values: values}
	typ := kit.SubstringFrom(f.next(), 5)
	if typ != "STT" && typ != "ALT" {
		return nil
	}
	ds := m.Session(f.next())
	if ds == nil {
		return nil
	}
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID
	pos.Set(model.KeyType, typ)

	pos.Set(model.KeyVersionFw, f.next())
	deviceModel := kit.Int(f.next())
	if deviceModel == 41 {
		f.i++ // variant
	}

	network := model.NewNetwork()
	for i := 0; i < 7; i++ {
		cid := kit.Int(f.next())
		mcc := kit.Int(f.next())
		mnc := kit.Int(f.next())
		var lac, rssi int
		if i == 0 {
			rssi = kit.Int(f.next())
			lac = kit.Int(f.next())
		} else {
			lac = kit.Int(f.next())
			rssi = kit.Int(f.next())
		}
		f.i++ // timing advance
		if cid > 0 {
			network.AddCellTower(model.NewCellTowerRSSI(mcc, mnc, lac, int64(cid), rssi))
		}
	}
	pos.Network = network

	pos.Set(model.KeyBattery, kit.Double(f.next()))
	if f.next() == "0" {
		pos.Set(model.KeyArchive, true)
	}
	pos.Set(model.KeyIndex, kit.Int(f.next()))
	pos.Set(model.KeyStatus, kit.Int(f.next()))

	if kit.Len(values[f.i]) == 3 {
		f.i++ // collaborative network
	}
	if deviceModel == 41 {
		f.i++ // collaborative network
		f.i++ // temperature
		pos.Set(model.KeyMotion, kit.Int(f.next()) == 2)
	}

	if values[f.i] == "" {
		m.LastLocation(pos, nil)
	} else {
		pos.SetTime(dateFormat.Parse(f.next() + f.next()))
		pos.SetLatitude(kit.Double(f.next()))
		pos.SetLongitude(kit.Double(f.next()))
		pos.Speed = units.KnotsFromKph(kit.Double(f.next()))
		pos.Course = kit.Double(f.next())
		pos.Set(model.KeySatellites, kit.Int(f.next()))
		pos.Valid = f.next() == "1"
	}
	return pos
}

// decodeSerialData reads the serial data of UEX reports; it returns the index after the
// checksum.
func decodeSerialData(pos *model.Position, f *fields) {
	remaining := kit.Int(f.next())
	totalFuel := 0.0
	for remaining > 0 {
		attribute := f.next()
		switch {
		case strings.HasPrefix(attribute, "CabAVL"):
			data := kit.Split(attribute, ",")
			fuel1 := kit.Double(data[2])
			if fuel1 > 0 {
				totalFuel += fuel1
				pos.Set("fuel1", fuel1)
			}
			fuel2 := kit.Double(data[3])
			if fuel2 > 0 {
				totalFuel += fuel2
				pos.Set("fuel2", fuel2)
			}
		case strings.HasPrefix(attribute, "GTSL"):
			pos.Set(model.KeyDriverUniqueID, kit.Split(attribute, "|")[4])
		case strings.Contains(attribute, "="):
			pair := kit.Split(attribute, "=")
			if len(pair) >= 2 {
				value := kit.Trim(pair[1])
				if i := kit.IndexOf(value, "."); i >= 0 {
					value = kit.Substring(value, 0, i)
				}
				switch kit.CharAt(pair[0], 0) {
				case 't':
					pos.Set(model.PrefixTemp+string(kit.CharAt(pair[0], 2)), kit.IntRadix(value, 16))
				case 'N':
					fuel := kit.IntRadix(value, 16)
					totalFuel += float64(fuel)
					pos.Set("fuel"+string(kit.CharAt(pair[0], 2)), fuel)
				case 'Q':
					pos.Set("drivingQuality", kit.IntRadix(value, 16))
				}
			}
		default:
			pos.Set("serial", kit.Trim(attribute))
		}
		remaining -= kit.Len(attribute) + 1
	}
	if totalFuel > 0 {
		pos.Set(model.KeyFuel, totalFuel)
	}
	f.i++ // checksum
}

func (d *Decoder) decode2356(m *ingest.Message, protocol string, values []string) any {
	f := &fields{values: values}
	typ := kit.SubstringFrom(f.next(), 5)

	result := values[f.i] == "Res"
	if result {
		f.i++
	}
	ds := m.Session(f.next())
	if ds == nil {
		return nil
	}
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID
	pos.Set(model.KeyType, typ)

	if result {
		m.LastLocation(pos, nil)
		pos.Set(model.KeyResult, strings.Join(values[f.i:], ";"))
		return pos
	}

	if typ != "STT" && typ != "EMG" && typ != "EVT" && typ != "ALT" && typ != "UEX" {
		return nil
	}
	if strings.HasPrefix(protocol, "ST3") || protocol == "ST500" || protocol == "ST600" {
		f.i++ // model
	}
	pos.Set(model.KeyVersionFw, f.next())
	pos.SetTime(dateFormat.Parse(f.next() + f.next()))

	if protocol != "ST500" {
		cid := kit.LongRadix(f.next(), 16)
		if protocol == "ST600" {
			mcc, mnc := kit.Int(f.next()), kit.Int(f.next())
			lac := kit.IntRadix(f.next(), 16)
			pos.Network = model.NetworkOf(model.NewCellTowerRSSI(mcc, mnc, lac, cid, kit.Int(f.next())))
		}
	}

	pos.SetLatitude(kit.Double(f.next()))
	pos.SetLongitude(kit.Double(f.next()))
	pos.Speed = units.KnotsFromKph(kit.Double(f.next()))
	pos.Course = kit.Double(f.next())
	pos.Set(model.KeySatellites, kit.Int(f.next()))
	pos.Valid = f.next() == "1"
	pos.Set(model.KeyOdometer, kit.Int(f.next()))
	pos.Set(model.KeyPower, kit.Double(f.next()))

	io := f.next()
	if kit.Len(io) >= 6 {
		pos.Set(model.KeyIgnition, kit.CharAt(io, 0) == '1')
		pos.Set(model.PrefixIn+"1", kit.CharAt(io, 1) == '1')
		pos.Set(model.PrefixIn+"2", kit.CharAt(io, 2) == '1')
		pos.Set(model.PrefixIn+"3", kit.CharAt(io, 3) == '1')
		pos.Set(model.PrefixOut+"1", kit.CharAt(io, 4) == '1')
		pos.Set(model.PrefixOut+"2", kit.CharAt(io, 5) == '1')
	}

	switch typ {
	case "STT":
		pos.Set(model.KeyStatus, kit.Int(f.next()))
		pos.Set(model.KeyIndex, kit.Int(f.next()))
	case "EMG":
		pos.AddAlarm(decodeEmergency(kit.Int(f.next())))
	case "EVT":
		pos.Set(model.KeyEvent, kit.Int(f.next()))
	case "ALT":
		pos.AddAlarm(decodeAlert(kit.Int(f.next())))
	case "UEX":
		decodeSerialData(pos, f)
	}

	hbm := d.hbm(m, ds.DeviceID)
	if hbm >= 1 {
		if f.more() {
			pos.Set(model.KeyHours, units.MsFromMinutes(int64(kit.Int(f.next()))))
		}
		if f.more() {
			pos.Set(model.KeyBattery, kit.Double(f.next()))
		}
		if f.more() && f.next() == "0" {
			pos.Set(model.KeyArchive, true)
		}
		if flag(m, ds.DeviceID, ".includeAdc", d.IncludeADC) {
			for i := 1; i <= 3; i++ {
				if f.more() {
					if v := f.next(); v != "" {
						pos.Set(model.PrefixAdc+itoa(i), kit.Double(v))
					}
				}
			}
		}
		if flag(m, ds.DeviceID, ".includeRpm", d.IncludeRPM) && f.more() {
			pos.Set(model.KeyRpm, kit.Int(f.next()))
		}
		need := 7
		if hbm == 1 {
			need = 2
		}
		if len(values)-f.i >= need {
			if driver := f.next(); driver != "" {
				pos.Set(model.KeyDriverUniqueID, driver)
			}
			f.i++ // registered
		}
		if flag(m, ds.DeviceID, ".includeTemp", d.IncludeTemp) {
			for i := 1; i <= 3; i++ {
				temperature := f.next()
				value := kit.SubstringFrom(temperature, kit.IndexOf(temperature, ":")+1)
				if value != "" {
					pos.Set(model.PrefixTemp+itoa(i), kit.Double(value))
				}
			}
		}
		if hbm >= 2 && len(values)-f.i >= 5 {
			cid := kit.Int(f.next())
			mcc := kit.Int(f.next())
			mnc := kit.Int(f.next())
			rssi := kit.Int(f.next())
			lac := kit.Int(f.next())
			pos.Network = model.NetworkOf(model.NewCellTowerRSSI(mcc, mnc, lac, int64(cid), rssi))
		}
	}
	return pos
}

func itoa(i int) string { return kit.Format("%d", i) }

func (d *Decoder) decodeUniversal(m *ingest.Message, values []string) any {
	f := &fields{values: values}
	typ := f.next()
	if typ != "STT" && typ != "ALT" && typ != "BLE" && typ != "RES" && typ != "UEX" {
		return nil
	}
	ds := m.Session(f.next())
	if ds == nil {
		return nil
	}
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID
	pos.Set(model.KeyType, typ)

	if typ == "RES" {
		m.LastLocation(pos, nil)
		pos.Set(model.KeyResult, strings.Join(values[f.i:], ";"))
		return pos
	}

	var mask int
	if typ == "BLE" {
		mask = 0b1100000110110
	} else {
		mask = kit.IntRadix(f.next(), 16)
	}
	check := func(bit int) bool { return kit.Check(int64(mask), bit) }

	if check(1) {
		f.i++ // model
	}
	if check(2) {
		pos.Set(model.KeyVersionFw, f.next())
	}
	if check(3) && f.next() == "0" {
		pos.Set(model.KeyArchive, true)
	}
	if check(4) && check(5) {
		pos.SetTime(dateFormat.Parse(f.next() + f.next()))
	}

	var tower model.CellTower
	if check(6) {
		cid := kit.LongRadix(f.next(), 16)
		tower.CellID = &cid
	}
	if check(7) {
		v := int64(kit.Int(f.next()))
		tower.MobileCountryCode = &v
	}
	if check(8) {
		v := int64(kit.Int(f.next()))
		tower.MobileNetworkCode = &v
	}
	if check(9) {
		v := int64(kit.IntRadix(f.next(), 16))
		tower.LocationAreaCode = &v
	}
	if tower.CellID != nil {
		pos.Network = model.NetworkOf(tower)
	}

	if check(10) {
		pos.Set(model.KeyRssi, kit.Int(f.next()))
	}
	if check(11) {
		pos.SetLatitude(kit.Double(f.next()))
	}
	if check(12) {
		pos.SetLongitude(kit.Double(f.next()))
	}

	if typ == "BLE" {
		pos.Valid = true
		count := kit.Int(f.next())
		for i := 1; i <= count; i++ {
			tag := "tag" + itoa(i)
			pos.Set(tag+"Rssi", kit.Int(f.next()))
			f.i++ // rssi min
			f.i++ // rssi max
			pos.Set(tag+"Id", f.next())
			pos.Set(tag+"Samples", kit.Int(f.next()))
			pos.Set(tag+"Major", kit.Int(f.next()))
			pos.Set(tag+"Minor", kit.Int(f.next()))
		}
		return pos
	}

	if check(13) {
		pos.Speed = units.KnotsFromKph(kit.Double(f.next()))
	}
	if check(14) {
		pos.Course = kit.Double(f.next())
	}
	if check(15) {
		pos.Set(model.KeySatellites, kit.Int(f.next()))
	}
	if check(16) {
		pos.Valid = f.next() == "1"
	}
	if check(17) {
		input := kit.Int(f.next())
		pos.Set(model.KeyIgnition, kit.Check(int64(input), 0))
		pos.Set(model.KeyInput, input)
	}
	if check(18) {
		pos.Set(model.KeyOutput, kit.Int(f.next()))
	}

	switch typ {
	case "ALT":
		if check(19) {
			pos.AddAlarm(decodeAlert(kit.Int(f.next())))
		}
		if check(20) {
			pos.Set("alertModifier", f.next())
		}
		if check(21) {
			pos.Set("alertData", f.next())
		}
	case "UEX":
		decodeSerialData(pos, f)
	default:
		if check(19) {
			pos.Set("mode", kit.Int(f.next()))
		}
		if check(20) {
			pos.Set("reason", kit.Int(f.next()))
		}
		if check(21) {
			pos.Set(model.KeyIndex, kit.Int(f.next()))
		}
	}

	if check(22) {
		f.i++ // reserved
	}
	if check(23) && typ != "UEX" {
		assignMask := kit.IntRadix(f.next(), 16)
		for i := 0; i <= 30; i++ {
			if kit.Check(int64(assignMask), i) {
				pos.Set(model.PrefixIo+itoa(i+1), f.next())
			}
		}
	}

	for assign := 1; f.more(); assign++ {
		if value := f.next(); value != "" {
			pos.Set("assign"+itoa(assign), value)
		}
	}
	return pos
}

func (d *Decoder) decodeZip(m *ingest.Message, buf *kit.Buf) any {
	buf.ReadUnsignedByte()  // header
	buf.ReadUnsignedShort() // length
	if buf.ReadUnsignedByte() != 0x10 {
		return nil
	}
	ds := m.Session(hex.EncodeToString(buf.ReadBytes(5))[:9])
	if ds == nil {
		return nil
	}
	buf.ReadUnsignedByte()  // model
	buf.ReadUnsignedShort() // software version

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID
	pos.SetTime(kit.NewDateBuilder(nil).
		SetDate(buf.ReadUnsignedByte(), buf.ReadUnsignedByte(), buf.ReadUnsignedByte()).
		SetTime(buf.ReadUnsignedByte(), buf.ReadUnsignedByte(), buf.ReadUnsignedByte()).
		Date())

	buf.ReadUnsignedShort() // lac
	buf.ReadUnsignedByte()  // cid

	pos.SetLatitude(float64(buf.ReadUnsignedByte()) + float64(kit.ReadBCD(buf, 6))/1000000.0)
	pos.SetLongitude(float64(buf.ReadUnsignedByte()) + float64(kit.ReadBCD(buf, 6))/1000000.0)
	pos.Speed = float64(buf.ReadUnsignedShort()) + float64(kit.ReadBCD(buf, 2))/100.0
	pos.Course = float64(buf.ReadUnsignedShort()) + float64(kit.ReadBCD(buf, 2))/100.0

	flags := int64(buf.ReadUnsignedByte())
	pos.Valid = kit.Check(flags, 7)
	if kit.Check(flags, 6) {
		pos.SetLatitude(-pos.Latitude)
	}
	if kit.Check(flags, 5) {
		pos.SetLongitude(-pos.Longitude)
	}

	pos.Set(model.KeyOdometer, buf.ReadUnsignedInt())
	pos.Set(model.KeyPower, float64(buf.ReadUnsignedByte())+float64(kit.ReadBCD(buf, 2))/100.0)

	io := int64(buf.ReadUnsignedByte())
	pos.Set(model.KeyIgnition, kit.Check(io, 0))
	for i := 1; i <= 3; i++ {
		pos.Set(model.PrefixIn+itoa(i), kit.Check(io, i))
	}
	for i := 1; i <= 2; i++ {
		pos.Set(model.PrefixOut+itoa(i), kit.Check(io, i+3))
	}
	pos.Set(model.KeyEvent, buf.ReadUnsignedByte())

	switch d.hbm(m, ds.DeviceID) {
	case 1:
		pos.Set(model.KeyHours, buf.ReadUnsignedInt())
		pos.Set(model.KeyBattery, buf.ReadUnsignedShort())
		if buf.ReadUnsignedByte() == 0 {
			pos.Set(model.KeyArchive, true)
		}
	case 2:
		cid := buf.ReadUnsignedShort()
		mcc := buf.ReadUnsignedShort()
		mnc := buf.ReadUnsignedByte()
		rssi := buf.ReadUnsignedShort()
		lac := buf.ReadUnsignedShort()
		pos.Network = model.NetworkOf(model.NewCellTowerRSSI(mcc, mnc, lac, int64(cid), rssi))
		buf.ReadUnsignedByte() // timing advance
	}

	buf.ReadUnsignedByte() // footer
	return pos
}

// readCoordinate is SIR's coordinate reading: two's complement first; if set with that
// value is out of range, the reader goes back and reads signed magnitude (which the
// upstream decoder always does).
func readCoordinate(buf *kit.Buf, set func(float64)) {
	start := buf.ReaderIndex()
	if trySet(set, float64(buf.ReadInt())/1000000.0) {
		return
	}
	buf.SetReaderIndex(start)
	set(float64(kit.ReadSignedMagnitudeInt(buf)) / 1000000.0)
}

func trySet(set func(float64), v float64) (ok bool) {
	defer func() {
		if r := recover(); r != nil {
			if r != model.ErrLatitudeRange && r != model.ErrLongitudeRange {
				panic(r)
			}
			ok = false
		}
	}()
	set(v)
	return true
}

func (d *Decoder) decodeBinary(m *ingest.Message, buf *kit.Buf) any {
	typ := buf.ReadUnsignedByte()
	buf.ReadUnsignedShort() // length

	ds := m.Session(hex.EncodeToString(buf.ReadBytes(5)))
	if ds == nil {
		return nil
	}
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	mask := int64(buf.ReadUnsignedMedium())
	if kit.Check(mask, 1) {
		buf.ReadUnsignedByte() // model
	}
	if kit.Check(mask, 2) {
		pos.Set(model.KeyVersionFw, kit.Format("%d.%d.%d",
			buf.ReadUnsignedByte(), buf.ReadUnsignedByte(), buf.ReadUnsignedByte()))
	}
	if kit.Check(mask, 3) && buf.ReadUnsignedByte() == 0 {
		pos.Set(model.KeyArchive, true)
	}
	if kit.Check(mask, 4) && kit.Check(mask, 5) {
		pos.SetTime(kit.NewDateBuilder(nil).
			SetDate(buf.ReadUnsignedByte(), buf.ReadUnsignedByte(), buf.ReadUnsignedByte()).
			SetTime(buf.ReadUnsignedByte(), buf.ReadUnsignedByte(), buf.ReadUnsignedByte()).
			Date())
	}
	if kit.Check(mask, 6) {
		buf.ReadUnsignedInt() // cell
	}
	if kit.Check(mask, 7) {
		buf.ReadUnsignedShort() // mcc
	}
	if kit.Check(mask, 8) {
		buf.ReadUnsignedShort() // mnc
	}
	if kit.Check(mask, 9) {
		buf.ReadUnsignedShort() // lac
	}
	if kit.Check(mask, 10) {
		pos.Set(model.KeyRssi, buf.ReadUnsignedByte())
	}
	if kit.Check(mask, 11) {
		readCoordinate(buf, pos.SetLatitude)
	}
	if kit.Check(mask, 12) {
		readCoordinate(buf, pos.SetLongitude)
	}
	if kit.Check(mask, 13) {
		pos.Speed = units.KnotsFromKph(float64(buf.ReadUnsignedShort()) / 100.0)
	}
	if kit.Check(mask, 14) {
		pos.Course = float64(buf.ReadUnsignedShort()) / 100.0
	}
	if kit.Check(mask, 15) {
		pos.Set(model.KeySatellites, buf.ReadUnsignedByte())
	}
	if kit.Check(mask, 16) {
		pos.Valid = buf.ReadUnsignedByte() > 0
	}
	if kit.Check(mask, 17) {
		input := buf.ReadUnsignedByte()
		pos.Set(model.KeyIgnition, kit.Check(int64(input), 0))
		pos.Set(model.KeyInput, input)
	}
	if kit.Check(mask, 18) {
		pos.Set(model.KeyOutput, buf.ReadUnsignedByte())
	}
	alertID := 0
	if kit.Check(mask, 19) {
		alertID = buf.ReadUnsignedByte()
		if typ == 0x82 {
			pos.AddAlarm(decodeAlert(alertID))
		}
	}
	if kit.Check(mask, 20) {
		buf.ReadUnsignedShort() // alert modifier
	}
	if kit.Check(mask, 21) && alertID == 59 {
		pos.Set(model.KeyDriverUniqueID, hex.EncodeToString(buf.ReadBytes(8)))
	}
	return pos
}

func decodeTravelReport(m *ingest.Message, values []string) any {
	ds := m.Session(values[1])
	if ds == nil {
		return nil
	}
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID
	m.LastLocation(pos, nil)
	pos.Set(model.KeyDriverUniqueID, values[len(values)-1])
	return pos
}

func (d *Decoder) decodeCrashReport(m *ingest.Message, buf *kit.Buf) any {
	if buf.GetByte(buf.ReaderIndex()+3) != ';' {
		return nil
	}
	values := kit.Split(buf.ReadString(23), ";")
	ds := m.Session(values[1])
	if ds == nil {
		return nil
	}
	current := kit.Int(values[2])
	total := kit.Int(values[3])

	if d.crash == nil {
		d.crash = kit.Empty()
	}
	d.crash.WriteBytes(buf.ReadBytes(buf.ReadableBytes() - 3))
	if current != total {
		return nil
	}

	crash := d.crash
	d.crash = nil
	crashTime := kit.NewDateBuilder(nil).
		SetDate(crash.ReadUnsignedByte(), crash.ReadUnsignedByte(), crash.ReadUnsignedByte()).
		SetTime(crash.ReadUnsignedByte(), crash.ReadUnsignedByte(), crash.ReadUnsignedByte()).
		Date()

	var positions []*model.Position
	for _, offset := range []time.Duration{-3 * time.Second, -2 * time.Second, -time.Second, time.Second} {
		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID
		pos.Valid = true
		pos.SetTime(crashTime.Add(offset))
		pos.SetLatitude(float64(crash.ReadIntLE()) / 10000000.0)
		pos.SetLongitude(float64(crash.ReadIntLE()) / 10000000.0)
		pos.Speed = units.KnotsFromKph(float64(crash.ReadUnsignedShort()) / 100.0)
		pos.Course = float64(crash.ReadUnsignedShort()) / 100.0

		var value strings.Builder
		value.WriteString("[")
		for i := 0; i < 100; i++ {
			if value.Len() > 1 {
				value.WriteString(",")
			}
			value.WriteString(kit.Format("[%d,%d,%d]", crash.ReadShortLE(), crash.ReadShortLE(), crash.ReadShortLE()))
		}
		value.WriteString("]")
		pos.Set(model.KeyGSensor, value.String())
		positions = append(positions, pos)
	}
	return positions
}

// Encoder is SuntechProtocolEncoder: universal commands once the connection's decoder has
// seen a universal message, else legacy commands with the prefix of the last message.
type Encoder struct {
	Decoder *Decoder // the connection's decoder; nil without a channel
}

func (e *Encoder) Encode(m *ingest.Message, c *model.Command) any {
	universal := false
	prefix := "SA200"
	if e.Decoder != nil {
		universal = e.Decoder.universal
		if e.Decoder.hasPrefix && kit.Len(e.Decoder.prefix) > 5 {
			prefix = kit.Substring(e.Decoder.prefix, 0, kit.Len(e.Decoder.prefix)-3)
		}
	}
	uniqueID := m.UniqueID(c.DeviceID)
	format := func(f string, keys ...string) any {
		return kit.FormatCommand(c, uniqueID, f, keys...)
	}
	enabled := func() bool {
		v, _ := c.Attributes.Get(model.CommandKeyData)
		if v == nil {
			panic(nilPointer) // Java calls equals on the missing value
		}
		return v == "1"
	}
	if universal {
		switch c.Type {
		case model.CommandRebootDevice:
			return format("CMD;%s;03;03\r", model.CommandKeyUniqueID)
		case model.CommandPositionSingle:
			return format("CMD;%s;03;01\r", model.CommandKeyUniqueID)
		case model.CommandOutputControl:
			codes := map[int]string{1: "02", 2: "04", 3: "10"}
			if enabled() {
				codes = map[int]string{1: "01", 2: "03", 3: "09"}
			}
			if code, ok := codes[kit.CommandInt(c, model.CommandKeyIndex)]; ok {
				return format("CMD;%s;04;"+code+"\r", model.CommandKeyUniqueID)
			}
			return nil
		case model.CommandEngineStop:
			return format("CMD;%s;04;01\r", model.CommandKeyUniqueID)
		case model.CommandEngineResume:
			return format("CMD;%s;04;02\r", model.CommandKeyUniqueID)
		case model.CommandAlarmArm:
			return format("CMD;%s;04;03\r", model.CommandKeyUniqueID)
		case model.CommandAlarmDisarm:
			return format("CMD;%s;04;04\r", model.CommandKeyUniqueID)
		}
		return nil
	}
	// The prefix is part of the format, as in Java (a '%' in it is a format specifier).
	switch c.Type {
	case model.CommandRebootDevice:
		return format(prefix+"CMD;%s;02;Reboot\r", model.CommandKeyUniqueID)
	case model.CommandPositionSingle:
		return format(prefix+"CMD;%s;02;StatusReq\r", model.CommandKeyUniqueID)
	case model.CommandOutputControl:
		if enabled() {
			return format(prefix+"CMD;%s;02;Enable%s\r", model.CommandKeyUniqueID, model.CommandKeyIndex)
		}
		return format(prefix+"CMD;%s;02;Disable%s\r", model.CommandKeyUniqueID, model.CommandKeyIndex)
	case model.CommandEngineStop:
		return format(prefix+"CMD;%s;02;Enable1\r", model.CommandKeyUniqueID)
	case model.CommandEngineResume:
		return format(prefix+"CMD;%s;02;Disable1\r", model.CommandKeyUniqueID)
	case model.CommandAlarmArm:
		return format(prefix+"CMD;%s;02;Enable2\r", model.CommandKeyUniqueID)
	case model.CommandAlarmDisarm:
		return format(prefix+"CMD;%s;02;Disable2\r", model.CommandKeyUniqueID)
	}
	return nil
}

var nilPointer = &kit.Fault{Msg: "NullPointerException"}
