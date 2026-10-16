// Package aplicom is the Aplicom protocol (AplicomProtocol): binary D, E, F and H
// messages over TCP (GPS, tachograph, FMS and histogram data).
package aplicom

import (
	"strconv"
	"strings"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "aplicom",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{Framer: Framer(), Decoder: ingest.DecoderFunc(decode)}
		}}},
	}
}

// Framer is AplicomFrameDecoder: alive messages (digits) are skipped, and the length
// follows a header whose size depends on the version flags.
func Framer() ingest.Framer {
	return &ingest.BaseFramer{Decode: func(_ *ingest.Message, buf *kit.Buf) any {
		// Skip Alive message
		for buf.IsReadable() && isDigit(int(buf.GetByte(buf.ReaderIndex()))) {
			buf.ReadSignedByte()
		}
		if buf.ReadableBytes() < 11 {
			return nil
		}
		version := buf.GetUnsignedByte(buf.ReaderIndex() + 1)
		offset := 1 + 1 + 3
		if version&0x80 != 0 {
			offset += 4
		}
		length := buf.GetUnsignedShort(buf.ReaderIndex() + offset)
		offset += 2
		if version&0x40 != 0 {
			offset += 3
		}
		length += offset // add header
		if buf.ReadableBytes() >= length {
			return buf.ReadSlice(length)
		}
		return nil
	}}
}

func isDigit(c int) bool { return c >= '0' && c <= '9' }

const (
	imeiBaseTC65V20  = 0x1437207000000
	imeiBaseTC65V28  = 358244010000000
	imeiBaseTC65iV11 = 0x14143B4000000
)

func validateImei(imei int64) bool { return kit.Luhn(imei/10) == imei%10 }

func imeiFromUnitID(unitID int64) int64 {
	if unitID == 0 {
		return 0
	}
	// Try TC65i
	if imei := imeiBaseTC65iV11 + unitID; validateImei(imei) {
		return imei
	}
	// Try TC65 v2.8
	if imei := imeiBaseTC65V28 + ((unitID + 0xA8180) & 0xFFFFFF); validateImei(imei) {
		return imei
	}
	// Try TC65 v2.0
	if imei := imeiBaseTC65V20 + unitID; validateImei(imei) {
		return imei
	}
	return unitID
}

const (
	defaultSelectorD = 0x0002fC
	defaultSelectorE = 0x007ffc
	defaultSelectorF = 0x0007fd
	eventData        = 119
)

func seconds(v int64) *time.Time {
	t := time.UnixMilli(v * 1000)
	return &t
}

func itoa(i int) string { return strconv.Itoa(i) }

func decodeEventData(pos *model.Position, buf *kit.Buf, event int) {
	switch event {
	case 2, 40:
		buf.ReadUnsignedByte()
	case 9:
		buf.ReadUnsignedMedium()
	case 31, 32:
		buf.ReadUnsignedShort()
	case 38:
		buf.Skip(4 * 9)
	case 113:
		buf.ReadUnsignedInt()
		buf.ReadUnsignedByte()
	case 119:
		pos.Set("eventData", buf.Slice(buf.ReaderIndex(), min(buf.ReadableBytes(), 1024)).HexDump())
	case 121, 142:
		buf.ReadLong()
	case 130:
		buf.ReadUnsignedInt() // incorrect
	case 188:
		decodeEB(pos, buf)
	}
}

// decodeCanData reads the CAN ids from the message buffer, not from the values, as the
// Java decoder does.
func decodeCanData(buf *kit.Buf, pos *model.Position) {
	buf.ReadUnsignedMedium() // packet identifier
	pos.Set(model.KeyVersionFw, buf.ReadUnsignedByte())
	count := buf.ReadUnsignedByte()
	buf.ReadUnsignedByte()  // batch count
	buf.ReadUnsignedShort() // selector bit
	buf.ReadUnsignedInt()   // timestamp
	buf.Skip(8)

	values := make([]*kit.Buf, 0, count)
	for i := 0; i < count; i++ {
		values = append(values, buf.ReadSlice(8))
	}

	for _, value := range values {
		switch buf.ReadInt() {
		case 0x20D:
			pos.Set(model.KeyRpm, value.ReadShortLE())
			pos.Set("dieselTemperature", float64(value.ReadShortLE())/10.0)
			pos.Set("batteryVoltage", float64(value.ReadShortLE())/100.0)
			pos.Set("supplyAirTempDep1", float64(value.ReadShortLE())/10.0)
		case 0x30D:
			pos.Set("activeAlarm", value.HexDump())
		case 0x40C:
			pos.Set("airTempDep1", float64(value.ReadShortLE())/10.0)
			pos.Set("airTempDep2", float64(value.ReadShortLE())/10.0)
		case 0x40D:
			pos.Set("coldUnitState", value.HexDump())
		case 0x50C:
			pos.Set("defrostTempDep1", float64(value.ReadShortLE())/10.0)
			pos.Set("defrostTempDep2", float64(value.ReadShortLE())/10.0)
		case 0x50D:
			pos.Set("condenserPressure", float64(value.ReadShortLE())/10.0)
			pos.Set("suctionPressure", float64(value.ReadShortLE())/10.0)
		case 0x58C:
			value.ReadSignedByte()
			value.ReadShort() // index
			switch value.ReadSignedByte() {
			case 0x01:
				pos.Set("setpointZone1", float64(value.ReadIntLE())/10.0)
			case 0x02:
				pos.Set("setpointZone2", float64(value.ReadIntLE())/10.0)
			case 0x05:
				pos.Set("unitType", int(value.ReadIntLE()))
			case 0x13:
				pos.Set("dieselHours", int(value.ReadIntLE()/60/60))
			case 0x14:
				pos.Set("electricHours", int(value.ReadIntLE()/60/60))
			case 0x17:
				pos.Set("serviceIndicator", int(value.ReadIntLE()))
			case 0x18:
				pos.Set("softwareVersion", float64(value.ReadIntLE())/100.0)
			}
		default:
			// Java logs "Aplicom CAN decoding error" and goes on.
		}
	}
}

func decodeD(m *ingest.Message, pos *model.Position, buf *kit.Buf, selector, event int) {
	if selector&0x0008 != 0 {
		pos.Valid = buf.ReadUnsignedByte()&0x40 != 0
	} else {
		m.LastLocation(pos, nil)
	}

	if selector&0x0004 != 0 {
		pos.DeviceTime = model.NewTime(time.UnixMilli(buf.ReadUnsignedInt() * 1000))
	}

	if selector&0x0008 != 0 {
		pos.FixTime = model.NewTime(time.UnixMilli(buf.ReadUnsignedInt() * 1000))
		if pos.DeviceTime.IsZero() {
			pos.DeviceTime = pos.FixTime
		}
		pos.SetLatitude(float64(buf.ReadInt()) / 1000000.0)
		pos.SetLongitude(float64(buf.ReadInt()) / 1000000.0)
		pos.Set(model.KeySatellitesVisible, buf.ReadUnsignedByte())
	}

	if selector&0x0010 != 0 {
		pos.Speed = units.KnotsFromKph(float64(buf.ReadUnsignedByte()))
		pos.Set("maximumSpeed", buf.ReadUnsignedByte())
		pos.Course = float64(buf.ReadUnsignedByte()) * 2.0
	}

	if selector&0x0040 != 0 {
		pos.Set(model.KeyInput, buf.ReadUnsignedByte())
	}

	if selector&0x0020 != 0 {
		for i := 1; i <= 4; i++ {
			pos.Set(model.PrefixAdc+itoa(i), buf.ReadUnsignedShort())
		}
	}

	if selector&0x8000 != 0 {
		pos.Set(model.KeyPower, float64(buf.ReadUnsignedShort())/1000.0)
		pos.Set(model.KeyBattery, float64(buf.ReadUnsignedShort())/1000.0)
	}

	// Pulse rate 1
	if selector&0x10000 != 0 {
		buf.ReadUnsignedShort()
		buf.ReadUnsignedInt()
	}

	// Pulse rate 2
	if selector&0x20000 != 0 {
		buf.ReadUnsignedShort()
		buf.ReadUnsignedInt()
	}

	if selector&0x0080 != 0 {
		pos.Set("trip1", buf.ReadUnsignedInt())
	}

	if selector&0x0100 != 0 {
		pos.Set("trip2", buf.ReadUnsignedInt())
	}

	if selector&0x0040 != 0 {
		pos.Set(model.KeyOutput, buf.ReadUnsignedByte())
	}

	if selector&0x0200 != 0 {
		// Java concatenates the two numbers as strings.
		high := int64(buf.ReadUnsignedShort()) << 32
		pos.Set(model.KeyDriverUniqueID, strconv.FormatInt(high, 10)+strconv.FormatInt(buf.ReadUnsignedInt(), 10))
	}

	if selector&0x0400 != 0 {
		buf.ReadUnsignedByte() // Keypad
	}

	if selector&0x0800 != 0 {
		pos.Altitude = float64(buf.ReadShort())
	}

	if selector&0x2000 != 0 {
		buf.ReadUnsignedShort() // snapshot counter
	}

	if selector&0x4000 != 0 {
		buf.Skip(8) // state flags
	}

	if selector&0x80000 != 0 {
		buf.Skip(11) // cell info
	}

	if selector&0x1000 != 0 {
		decodeEventData(pos, buf, event)
	}

	if m.Config().Bool(m.Protocol()+".can", false) &&
		buf.IsReadable() && selector&0x1000 != 0 && event == eventData {
		decodeCanData(buf, pos)
	}
}

func readTrimmed(buf *kit.Buf, n int) string { return kit.Trim(buf.ReadString(n)) }

func decodeE(m *ingest.Message, pos *model.Position, buf *kit.Buf, selector int) {
	if selector&0x0008 != 0 {
		pos.Set("tachographEvent", buf.ReadUnsignedShort())
	}

	if selector&0x0004 != 0 {
		m.LastLocation(pos, seconds(buf.ReadUnsignedInt()))
	} else {
		m.LastLocation(pos, nil)
	}

	if selector&0x0010 != 0 {
		var b strings.Builder
		for _, unit := range []string{"s ", "m ", "h ", "M ", "D ", "Y "} {
			b.WriteString(itoa(buf.ReadUnsignedByte()) + unit)
		}
		b.WriteString(itoa(int(buf.ReadSignedByte())) + "m ")
		b.WriteString(itoa(int(buf.ReadSignedByte())) + "h")
		pos.Set("tachographTime", b.String())
	}

	pos.Set("workState", buf.ReadUnsignedByte())
	pos.Set("driver1State", buf.ReadUnsignedByte())
	pos.Set("driver2State", buf.ReadUnsignedByte())

	if selector&0x0020 != 0 {
		pos.Set("tachographStatus", buf.ReadUnsignedByte())
	}

	if selector&0x0040 != 0 {
		pos.Set(model.KeyObdSpeed, float64(buf.ReadUnsignedShort())/256.0)
	}

	if selector&0x0080 != 0 {
		pos.Set(model.KeyObdOdometer, buf.ReadUnsignedInt()*5)
	}

	if selector&0x0100 != 0 {
		pos.Set(model.KeyOdometerTrip, buf.ReadUnsignedInt()*5)
	}

	if selector&0x8000 != 0 {
		pos.Set("kFactor", model.JavaToString(float64(buf.ReadUnsignedShort())/1000.0)+" pulses/m")
	}

	if selector&0x0200 != 0 {
		pos.Set(model.KeyRpm, float64(buf.ReadUnsignedShort())*0.125)
	}

	if selector&0x0400 != 0 {
		pos.Set("extraInfo", buf.ReadUnsignedShort())
	}

	if selector&0x0800 != 0 {
		pos.Set(model.KeyVin, readTrimmed(buf, 18))
	}

	if selector&0x2000 != 0 {
		buf.ReadUnsignedByte() // card 1 type
		buf.ReadUnsignedByte() // card 1 country code
		pos.Set("card1", readTrimmed(buf, 20))
	}

	if selector&0x4000 != 0 {
		buf.ReadUnsignedByte() // card 2 type
		buf.ReadUnsignedByte() // card 2 country code
		pos.Set("card2", readTrimmed(buf, 20))
	}

	if selector&0x10000 != 0 {
		count := buf.ReadUnsignedByte()
		for i := 1; i <= count; i++ {
			pos.Set("driver"+itoa(i), readTrimmed(buf, 22))
			pos.Set("driverTime"+itoa(i), buf.ReadUnsignedInt())
		}
	}
}

func decodeH(m *ingest.Message, pos *model.Position, buf *kit.Buf, selector int) {
	if selector&0x0004 != 0 {
		m.LastLocation(pos, seconds(buf.ReadUnsignedInt()))
	} else {
		m.LastLocation(pos, nil)
	}

	if selector&0x0040 != 0 {
		buf.ReadUnsignedInt() // reset time
	}

	if selector&0x2000 != 0 {
		buf.ReadUnsignedShort() // snapshot counter
	}

	index := 1
	for buf.ReadableBytes() > 0 {
		h := "h" + itoa(index)
		pos.Set(h+"Index", buf.ReadUnsignedByte())

		buf.ReadUnsignedShort() // length

		n := buf.ReadUnsignedByte()
		mm := buf.ReadUnsignedByte()

		pos.Set(h+"XLength", n)
		pos.Set(h+"YLength", mm)

		if selector&0x0008 != 0 {
			pos.Set(h+"XType", buf.ReadUnsignedByte())
			pos.Set(h+"YType", buf.ReadUnsignedByte())
			pos.Set(h+"Parameters", buf.ReadUnsignedByte())
		}

		percentageFormat := selector&0x0020 != 0

		var data strings.Builder
		for i := 0; i < n*mm; i++ {
			if percentageFormat {
				data.WriteString(model.JavaToString(float64(buf.ReadUnsignedByte())*0.5) + "% ")
			} else {
				data.WriteString(itoa(buf.ReadUnsignedShort()) + " ")
			}
		}
		pos.Set(h+"Data", kit.Trim(data.String()))

		pos.Set(h+"Total", buf.ReadUnsignedInt())

		if selector&0x0010 != 0 {
			k := buf.ReadUnsignedByte()
			limits := func(count int) string {
				var b strings.Builder
				for i := 1; i < count; i++ {
					if k == 1 {
						b.WriteString(itoa(int(buf.ReadSignedByte())) + " ")
					} else if k == 2 {
						b.WriteString(itoa(buf.ReadShort()) + " ")
					}
				}
				return kit.Trim(b.String())
			}
			pos.Set(h+"XLimits", limits(n))
			pos.Set(h+"YLimits", limits(mm))
		}

		index++
	}
}

func decodeEB(pos *model.Position, buf *kit.Buf) {
	if buf.ReadSignedByte() != 'E' || buf.ReadSignedByte() != 'B' {
		return
	}

	pos.Set(model.KeyVersionFw, buf.ReadUnsignedByte())
	pos.Set(model.KeyEvent, buf.ReadUnsignedShort())
	pos.Set("dataValidity", buf.ReadUnsignedByte())
	pos.Set("towed", buf.ReadUnsignedByte())
	buf.ReadUnsignedShort() // length

	for buf.ReadableBytes() > 0 {
		buf.ReadUnsignedByte() // towed position
		typ := buf.ReadUnsignedByte()
		length := buf.ReadUnsignedByte()
		end := buf.ReaderIndex() + length

		switch typ {
		case 0x01:
			pos.Set("brakeFlags", buf.ReadSlice(length).HexDump())
		case 0x02:
			pos.Set("wheelSpeed", float64(buf.ReadUnsignedShort())/256.0)
			pos.Set("wheelSpeedDifference", float64(buf.ReadUnsignedShort())/256.0-125.0)
			pos.Set("lateralAcceleration", float64(buf.ReadUnsignedByte())/10.0-12.5)
			pos.Set("vehicleSpeed", float64(buf.ReadUnsignedShort())/256.0)
		case 0x03:
			pos.Set(model.KeyAxleWeight, buf.ReadUnsignedShort()*2)
		case 0x04:
			pos.Set("tirePressure", buf.ReadUnsignedByte()*10)
			pos.Set("pneumaticPressure", buf.ReadUnsignedByte()*5)
		case 0x05:
			pos.Set("brakeLining", float64(buf.ReadUnsignedByte())*0.4)
			pos.Set("brakeTemperature", buf.ReadUnsignedByte()*10)
		case 0x06:
			pos.Set(model.KeyOdometer, buf.ReadUnsignedInt()*5)
			pos.Set(model.KeyOdometerTrip, buf.ReadUnsignedInt()*5)
			pos.Set(model.KeyOdometerService, (buf.ReadUnsignedInt()-2105540607)*5)
		case 0x0A:
			pos.Set("absStatusCounter", buf.ReadUnsignedShort())
			pos.Set("atvbStatusCounter", buf.ReadUnsignedShort())
			pos.Set("vdcActiveCounter", buf.ReadUnsignedShort())
		case 0x0B:
			pos.Set("brakeMinMaxData", buf.ReadSlice(length).HexDump())
		case 0x0C:
			pos.Set("missingPgn", buf.ReadSlice(length).HexDump())
		case 0x0D:
			buf.ReadUnsignedByte()
			pos.Set("towedDetectionStatus", buf.ReadUnsignedInt())
			buf.Skip(17) // vin
		}

		buf.SetReaderIndex(end)
	}
}

func decodeF(m *ingest.Message, pos *model.Position, buf *kit.Buf, selector int) {
	var deviceTime *time.Time
	if selector&0x0004 != 0 {
		deviceTime = seconds(buf.ReadUnsignedInt())
	}
	m.LastLocation(pos, deviceTime)

	buf.ReadUnsignedByte() // data validity

	if selector&0x0008 != 0 {
		pos.Set(model.KeyRpm, buf.ReadUnsignedShort())
		pos.Set("rpmMax", buf.ReadUnsignedShort())
		pos.Set("rpmMin", buf.ReadUnsignedShort())
	}

	if selector&0x0010 != 0 {
		pos.Set(model.KeyEngineTemp, buf.ReadShort())
		pos.Set("engineTempMax", buf.ReadShort())
		pos.Set("engineTempMin", buf.ReadShort())
	}

	if selector&0x0020 != 0 {
		pos.Set(model.KeyHours, units.MsFromHours(buf.ReadUnsignedInt()))
		pos.Set("serviceDistance", int(buf.ReadInt()))
		pos.Set("driverActivity", buf.ReadUnsignedByte())
		pos.Set(model.KeyThrottle, buf.ReadUnsignedByte())
		pos.Set(model.KeyFuel, buf.ReadUnsignedByte())
	}

	if selector&0x0040 != 0 {
		pos.Set(model.KeyFuelUsed, buf.ReadUnsignedInt())
	}

	if selector&0x0080 != 0 {
		pos.Set(model.KeyOdometer, buf.ReadUnsignedInt())
	}

	if selector&0x0100 != 0 {
		pos.Set(model.KeyObdSpeed, buf.ReadUnsignedByte())
		pos.Set("speedMax", buf.ReadUnsignedByte())
		pos.Set("speedMin", buf.ReadUnsignedByte())
		pos.Set("hardBraking", buf.ReadUnsignedByte())
	}

	if selector&0x0200 != 0 {
		pos.Set("tachographSpeed", buf.ReadUnsignedByte())
		pos.Set("driver1State", buf.ReadUnsignedByte())
		pos.Set("driver2State", buf.ReadUnsignedByte())
		pos.Set("tachographStatus", buf.ReadUnsignedByte())
		pos.Set("overspeedCount", buf.ReadUnsignedByte())
	}

	if selector&0x0800 != 0 {
		pos.Set(model.KeyHours, float64(buf.ReadUnsignedInt())/20.0)
		pos.Set(model.KeyRpm, float64(buf.ReadUnsignedShort())*0.125)
		pos.Set(model.KeyObdSpeed, float64(buf.ReadUnsignedShort())/256.0)
		pos.Set(model.KeyFuelUsed, float64(buf.ReadUnsignedInt())*0.5)
		pos.Set(model.KeyFuel, float64(buf.ReadUnsignedByte())*0.4)
	}

	if selector&0x1000 != 0 {
		pos.Set("ambientTemperature", float64(buf.ReadUnsignedShort())*0.03125-273)
		buf.ReadUnsignedShort() // fuel rate
		pos.Set("fuelEconomy", float64(buf.ReadUnsignedShort())/512.0)
		pos.Set(model.KeyFuelConsumption, float64(buf.ReadUnsignedInt())/1000.0)
		buf.ReadUnsignedByte() // pto drive engagement
	}

	if selector&0x2000 != 0 {
		buf.Skip(buf.ReadUnsignedByte()) // driver identification
	}

	if selector&0x4000 != 0 {
		pos.Set("torque", buf.ReadUnsignedByte())
		pos.Set("brakePressure1", buf.ReadUnsignedByte()*8)
		pos.Set("brakePressure2", buf.ReadUnsignedByte()*8)
		pos.Set("grossWeight", buf.ReadUnsignedShort()*10)
		pos.Set("exhaustFluid", float64(buf.ReadUnsignedByte())*0.4)
		buf.ReadUnsignedByte() // retarder torque mode
		pos.Set("retarderTorque", buf.ReadUnsignedByte())
		pos.Set("retarderSelection", float64(buf.ReadUnsignedByte())*0.4)
		buf.Skip(8 * 4) // tell tale status blocks 1 to 4
	}

	if selector&0x8000 != 0 {
		pos.Set("parkingBrakeStatus", buf.ReadUnsignedByte())
		pos.Set("doorStatus", buf.ReadUnsignedByte())
		buf.Skip(8) // status per door
		pos.Set("alternatorStatus", buf.ReadUnsignedByte())
		pos.Set("selectedGear", buf.ReadUnsignedByte())
		pos.Set("currentGear", buf.ReadUnsignedByte())
		buf.Skip(4 * 2) // air suspension pressure
	}

	if selector&0x0400 != 0 {
		count := buf.ReadUnsignedByte()
		for i := 0; i < count; i++ {
			pos.Set("axle"+itoa(i), buf.ReadUnsignedShort())
		}
	}
}

func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	protocol := buf.ReadSignedByte()
	version := buf.ReadUnsignedByte()

	var imei string
	if version&0x80 != 0 {
		imei = strconv.FormatInt(buf.ReadUnsignedInt()<<(3*8)|int64(buf.ReadUnsignedMedium()), 10)
	} else {
		imei = strconv.FormatInt(imeiFromUnitID(int64(buf.ReadUnsignedMedium())), 10)
	}

	buf.ReadUnsignedShort() // length

	selector := defaultSelectorD
	if protocol == 'E' {
		selector = defaultSelectorE
	} else if protocol == 'F' {
		selector = defaultSelectorF
	}
	if version&0x40 != 0 {
		selector = buf.ReadUnsignedMedium()
	}

	pos := m.NewPosition()
	ds := m.Session(imei)
	if ds == nil {
		return nil
	}
	pos.DeviceID = ds.DeviceID

	event := buf.ReadUnsignedByte()
	pos.Set(model.KeyEvent, event)
	pos.Set("eventInfo", buf.ReadUnsignedByte())

	switch protocol {
	case 'D':
		decodeD(m, pos, buf, selector, event)
	case 'E':
		decodeE(m, pos, buf, selector)
	case 'H':
		decodeH(m, pos, buf, selector)
	case 'F':
		decodeF(m, pos, buf, selector)
	default:
		return nil
	}
	return pos
}
