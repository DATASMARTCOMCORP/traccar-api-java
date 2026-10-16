// Package hyn600 is the HYN600 protocol (Hyn600Protocol): binary "+RPT:" reports framed
// by a length, with masked location, cell, event and BLE sections.
package hyn600

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
		Name: "hyn600",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  Framer(),
				Decoder: ingest.DecoderFunc(decode),
			}
		}}},
	}
}

// Framer is the protocol's LengthFieldBasedFrameDecoder(1024, 5, 2, -7, 0).
func Framer() ingest.Framer { return ingest.NewLengthFramer(ingest.MaxFrameLength, 5, 2, -7, 0) }

func decodeAlarm(alarmType, state int) string {
	switch alarmType {
	case 3:
		if state == 1 {
			return model.AlarmLowBattery
		}
	case 7:
		return model.AlarmAccident
	case 14:
		switch state {
		case 1:
			return model.AlarmPowerCut
		case 2:
			return model.AlarmPowerRestored
		case 3:
			return model.AlarmLowPower
		}
	case 15:
		return model.AlarmTow
	case 16, 41:
		return model.AlarmOverspeed
	case 19:
		switch state {
		case 1:
			return model.AlarmBraking
		case 2:
			return model.AlarmAcceleration
		case 3:
			return model.AlarmCornering
		}
	case 21, 40:
		if kit.Check(int64(state), 0) {
			return model.AlarmGeofenceEnter
		}
		return model.AlarmGeofenceExit
	case 32:
		return model.AlarmJamming
	case 33:
		return model.AlarmTampering
	}
	return ""
}

func check(v int64, i int) bool { return kit.Check(v, i) }

func readDate(buf *kit.Buf) time.Time {
	b := kit.NewDateBuilder(nil)
	b.SetYear(buf.ReadUnsignedShort())
	b.SetMonth(buf.ReadUnsignedByte())
	b.SetDay(buf.ReadUnsignedByte())
	b.SetHour(buf.ReadUnsignedByte())
	b.SetMinute(buf.ReadUnsignedByte())
	b.SetSecond(buf.ReadUnsignedByte())
	return b.Date()
}

// skipSatellites reads a satellite mask and the counts it announces.
func skipSatellites(buf *kit.Buf) {
	mask := int64(buf.ReadUnsignedByte())
	for i := 0; i < 4; i++ {
		if check(mask, i) {
			buf.ReadUnsignedByte()
		}
	}
}

func decodeReport(m *ingest.Message, ds *ingest.DeviceSession, buf *kit.Buf) []*model.Position {
	buf.ReadUnsignedShort() // protocol version
	alarmType := buf.ReadUnsignedByte()
	alarmState := buf.ReadUnsignedByte()
	mask := buf.ReadUnsignedInt()

	if check(mask, 0) {
		buf.ReadUnsignedByte() // frame count
		buf.ReadUnsignedByte() // frame id
	}
	if check(mask, 1) {
		buf.ReadUnsignedByte() // network type
	}

	battery, batteryLevel := 0.0, 0
	if check(mask, 2) {
		battery = float64(buf.ReadUnsignedShort()) / 1000.0
		batteryLevel = buf.ReadUnsignedByte()
	}

	positions := []*model.Position{}

	if check(mask, 3) {
		locationMask := int64(buf.ReadUnsignedShort())
		count := buf.ReadUnsignedByte()
		for i := 0; i < count; i++ {
			pos := m.NewPosition()
			pos.DeviceID = ds.DeviceID
			if check(locationMask, 0) {
				pos.Valid = buf.ReadUnsignedByte() > 0
			}
			if check(locationMask, 1) {
				pos.Set(model.KeyHdop, buf.ReadUnsignedByte())
			}
			if check(locationMask, 2) {
				pos.Speed = units.KnotsFromKph(float64(buf.ReadUnsignedShort()))
			}
			if check(locationMask, 3) {
				pos.Course = float64(buf.ReadUnsignedShort())
			}
			if check(locationMask, 4) {
				pos.Altitude = float64(buf.ReadShort())
			}
			if check(locationMask, 5) {
				pos.SetLatitude(float64(buf.ReadInt()) / 1000000.0)
			}
			if check(locationMask, 6) {
				pos.SetLongitude(float64(buf.ReadInt()) / 1000000.0)
			}
			if check(locationMask, 7) {
				pos.SetTime(readDate(buf))
			}
			if check(locationMask, 8) {
				skipSatellites(buf)
			}
			positions = append(positions, pos)
		}
	}

	if len(positions) == 0 {
		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID
		m.LastLocation(pos, nil)
		positions = append(positions, pos)
	}

	pos := positions[len(positions)-1]
	pos.AddAlarm(decodeAlarm(alarmType, alarmState))

	if check(mask, 2) {
		pos.Set(model.KeyBattery, battery)
		pos.Set(model.KeyBatteryLevel, batteryLevel) // a Short in Java
	}

	if check(mask, 4) {
		mcc := buf.ReadUnsignedShort()
		mnc := buf.ReadUnsignedShort()
		lac := buf.ReadUnsignedShort()
		cid := buf.ReadUnsignedInt()
		rssi := buf.ReadUnsignedByte()
		pos.Network = model.NetworkOf(model.NewCellTowerRSSI(mcc, mnc, lac, cid, rssi))
	}

	if check(mask, 7) {
		buf.ReadUnsignedByte()  // upgrade code
		buf.ReadUnsignedShort() // reserved
	}

	if check(mask, 8) {
		eventMask := buf.ReadUnsignedInt()
		if check(eventMask, 0) {
			pos.Set(model.KeyPower, float64(buf.ReadUnsignedShort())/100.0)
		}
		if check(eventMask, 1) {
			adcIndex := buf.ReadUnsignedByte()
			pos.Set(model.PrefixAdc+strconv.Itoa(adcIndex), float64(buf.ReadUnsignedShort())/1000.0)
		}
		if check(eventMask, 2) {
			status := buf.ReadUnsignedByte()
			pos.Set(model.KeyIgnition, status >= 0x20)
			pos.Set(model.KeyMotion, status&0xf >= 0x2)
		}
		if check(eventMask, 3) {
			pos.Set(model.KeyInput, buf.ReadUnsignedByte())
		}
		if check(eventMask, 4) {
			pos.Set(model.KeyOutput, buf.ReadUnsignedByte())
		}
		if check(eventMask, 5) {
			pos.Set(model.KeyOdometerTrip, buf.ReadUnsignedShort()*100)
			pos.Set(model.KeyOdometer, buf.ReadUnsignedInt()*100)
		}
		if check(eventMask, 6) {
			skipSatellites(buf)
		}
		if check(eventMask, 7) {
			buf.ReadUnsignedInt() // all geo status
		}
		if check(eventMask, 8) {
			buf.Skip(buf.ReadUnsignedByte()) // id data
		}
		if check(eventMask, 9) {
			sensorCount := buf.ReadUnsignedByte()
			for i := 0; i < sensorCount; i++ {
				buf.Skip(8) // sensor id
				dataMask := int64(buf.ReadUnsignedByte())
				if check(dataMask, 0) {
					buf.ReadUnsignedShort() // temperature
				}
				if check(dataMask, 1) {
					buf.ReadUnsignedByte() // humidity
				}
			}
		}
		if check(eventMask, 10) {
			buf.Skip(3) // trip hours
			buf.Skip(5) // total hours
		}
		if check(eventMask, 11) {
			buf.Skip(9)
		}
		if check(eventMask, 12) {
			buf.Skip(8)
		}
	}

	if check(mask, 16) {
		bleMask := buf.ReadUnsignedInt()
		if check(bleMask, 0) {
			buf.ReadUnsignedByte() // ble relay number
			buf.ReadUnsignedByte() // ble relay id
			buf.ReadUnsignedByte() // ble relay alarm type
			relayMask := int64(buf.ReadUnsignedByte())
			if check(relayMask, 0) {
				buf.Skip(6) // ble relay mac
			}
			if check(relayMask, 1) {
				buf.ReadUnsignedShort() // ble relay rssi
			}
			if check(relayMask, 2) {
				buf.ReadUnsignedByte() // reserved
			}
			if check(relayMask, 3) {
				buf.ReadUnsignedByte() // ble relay status
			}
			if check(relayMask, 4) {
				buf.ReadUnsignedByte() // ble relay error
			}
		}
		if check(bleMask, 1) {
			buf.ReadUnsignedShort() // ble beacon number
			buf.ReadUnsignedByte()  // ble beacon model
			beaconMask := int64(buf.ReadUnsignedByte())
			if check(beaconMask, 0) {
				buf.Skip(6) // beacon mac
			}
			if check(beaconMask, 1) {
				buf.ReadUnsignedShort() // beacon rssi
			}
			if check(beaconMask, 2) {
				buf.ReadUnsignedShort() // beacon battery
			}
		}
	}

	pos.DeviceTime = model.NewTime(readDate(buf))

	buf.ReadUnsignedShort() // index
	buf.ReadUnsignedByte()  // tail

	return positions
}

func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	header := buf.ReadUTF8(5)
	buf.ReadUnsignedShort() // length

	imei := kit.ReadDecimalDigits(buf, 7) + strconv.Itoa(buf.ReadUnsignedByte())
	ds := m.Session(imei)
	if ds == nil {
		return nil
	}

	buf.ReadUnsignedByte() // device id

	if strings.HasSuffix(header, "RPT:") {
		return decodeReport(m, ds, buf)
	}
	return nil
}
