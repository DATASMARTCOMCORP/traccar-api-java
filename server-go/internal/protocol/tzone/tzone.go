// Package tzone is the Tzone protocol (TzoneProtocol): binary frames with GPS, LBS and
// status blocks over TCP.
package tzone

import (
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/config"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one TCP server. tzone.ack enables the
// acknowledgements.
func Protocol(cfg *config.Config) (ingest.Protocol, error) {
	ack := cfg.Bool("tzone.ack", false)
	return ingest.Protocol{
		Name: "tzone",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.NewLengthFramer(ingest.MaxFrameLength, 2, 2, 2, 0),
				Decoder: &Decoder{Ack: ack},
			}
		}}},
	}, nil
}

// Decoder is TzoneProtocolDecoder; Ack is <protocol>.ack.
type Decoder struct {
	Ack bool
}

func sendResponse(m *ingest.Message, index int) {
	ack := kit.Format("@ACK,%d#", index)
	t := "@UTC time:" + m.Now.UTC().Format("2006-01-02 15:04:05")
	m.Send(kit.NewBuf(kit.NettyBytes(ack + t)))
}

func decodeAlarm(value int) string {
	switch value {
	case 0x01:
		return model.AlarmSos
	case 0x10:
		return model.AlarmLowBattery
	case 0x11:
		return model.AlarmOverspeed
	case 0x14:
		return model.AlarmBraking
	case 0x15:
		return model.AlarmAcceleration
	case 0x30:
		return model.AlarmParking
	case 0x42:
		return model.AlarmGeofenceExit
	case 0x43:
		return model.AlarmGeofenceEnter
	case 0xA0:
		return model.AlarmTemperature
	case 0xA3:
		return model.AlarmVibration
	case 0xB0:
		return model.AlarmPowerOn
	case 0xB1:
		return model.AlarmPowerOff
	}
	return ""
}

func readDate(buf *kit.Buf) time.Time {
	b := kit.NewDateBuilder(nil)
	b.SetDate(buf.ReadUnsignedByte(), buf.ReadUnsignedByte(), buf.ReadUnsignedByte())
	b.SetTime(buf.ReadUnsignedByte(), buf.ReadUnsignedByte(), buf.ReadUnsignedByte())
	return b.Date()
}

func decodeGps(pos *model.Position, buf *kit.Buf, hardware int) bool {
	blockLength := buf.ReadUnsignedShort()
	blockEnd := buf.ReaderIndex() + blockLength

	if hardware == 0x40A {
		if blockLength < 19 {
			return false
		}
		status := int64(buf.ReadUnsignedByte())
		pos.Valid = true
		pos.FixTime = model.NewTime(readDate(buf))

		latitude := float64(buf.ReadUnsignedInt()) / 1000000.0
		longitude := float64(buf.ReadUnsignedInt()) / 1000000.0
		if kit.Check(status, 0) {
			pos.SetLatitude(latitude)
		} else {
			pos.SetLatitude(-latitude)
		}
		if kit.Check(status, 1) {
			pos.SetLongitude(-longitude)
		} else {
			pos.SetLongitude(longitude)
		}

		pos.Course = float64(buf.ReadUnsignedShort())
		pos.Speed = float64(buf.ReadUnsignedShort()) / 10.0

		buf.SetReaderIndex(blockEnd)
		return true
	}

	if blockLength < 22 {
		return false
	}

	if hardware == 0x413 {
		buf.ReadUnsignedByte() // status
	} else {
		pos.Set(model.KeySatellites, buf.ReadUnsignedByte())
	}

	if hardware == 0x413 {
		pos.FixTime = model.NewTime(readDate(buf))
	}

	var lat, lon float64
	if hardware == 0x10A || hardware == 0x10B {
		lat = float64(buf.ReadUnsignedInt()) / 600000.0
		lon = float64(buf.ReadUnsignedInt()) / 600000.0
	} else {
		lat = float64(buf.ReadUnsignedInt()) / 100000.0 / 60.0
		lon = float64(buf.ReadUnsignedInt()) / 100000.0 / 60.0
	}

	if hardware == 0x413 {
		pos.Set(model.KeyHdop, float64(buf.ReadUnsignedShort())/10.0)

		pos.Altitude = float64(buf.ReadUnsignedShort())
		pos.Course = float64(buf.ReadUnsignedShort())
		pos.Speed = units.KnotsFromKph(float64(buf.ReadUnsignedShort()) / 10.0)

		pos.Set(model.KeySatellites, buf.ReadUnsignedByte())
	} else {
		pos.FixTime = model.NewTime(readDate(buf))

		pos.Speed = float64(buf.ReadUnsignedShort()) / 100.0

		pos.Set(model.KeyOdometer, buf.ReadUnsignedMedium())

		flags := int64(buf.ReadUnsignedShort())
		pos.Course = float64(kit.To(flags, 9))
		if !kit.Check(flags, 10) {
			lat = -lat
		}
		pos.SetLatitude(lat)
		if kit.Check(flags, 9) {
			lon = -lon
		}
		pos.SetLongitude(lon)
		pos.Valid = kit.Check(flags, 11)
	}

	buf.SetReaderIndex(blockEnd)
	return true
}

func decodeCards(pos *model.Position, buf *kit.Buf) {
	index := 1
	for range 4 {
		blockLength := buf.ReadUnsignedShort()
		blockEnd := buf.ReaderIndex() + blockLength

		if blockLength > 0 {
			count := buf.ReadUnsignedByte()
			for range count {
				length := buf.ReadUnsignedByte()
				odd := length%2 != 0
				if odd {
					length++
				}
				num := buf.ReadSlice(length / 2).HexDump()
				if odd {
					num = kit.SubstringFrom(num, 1)
				}
				pos.Set(kit.Format("card%d", index), num)
			}
		}

		buf.SetReaderIndex(blockEnd)
	}
}

func decodePassengers(pos *model.Position, buf *kit.Buf) {
	blockLength := buf.ReadUnsignedShort()
	blockEnd := buf.ReaderIndex() + blockLength

	if blockLength > 0 {
		pos.Set("passengersOn", buf.ReadUnsignedMedium())
		pos.Set("passengersOff", buf.ReadUnsignedMedium())
	}

	buf.SetReaderIndex(blockEnd)
}

func decodeTags(pos *model.Position, buf *kit.Buf, hardware int) {
	blockLength := buf.ReadUnsignedShort()
	blockEnd := buf.ReaderIndex() + blockLength

	if blockLength > 0 {
		typ := buf.ReadUnsignedByte()

		if hardware != 0x153 || typ >= 2 {
			count := buf.ReadUnsignedByte()
			tagLength := buf.ReadUnsignedByte()

			for i := 1; i <= count; i++ {
				tagEnd := buf.ReaderIndex() + tagLength

				buf.ReadUnsignedByte()    // status
				buf.ReadUnsignedShortLE() // battery voltage
				pos.Set(kit.Format("%s%d", model.PrefixTemp, i), float64(buf.ReadShortLE()&0x3fff)/10.0)
				buf.ReadUnsignedByte() // humidity
				buf.ReadUnsignedByte() // rssi

				buf.SetReaderIndex(tagEnd)
			}
		} else if typ == 1 {
			pos.Set(model.KeyCard, buf.ReadUTF8(blockEnd-buf.ReaderIndex()))
		}
	}

	buf.SetReaderIndex(blockEnd)
}

func setTemperature(pos *model.Position, buf *kit.Buf) {
	temperature := int64(buf.ReadUnsignedShort())
	if !kit.Check(temperature, 15) {
		value := float64(kit.To(temperature, 14)) / 10.0
		if kit.Check(temperature, 14) {
			value = -value
		}
		pos.Set(model.PrefixTemp+"1", value)
	}
	humidity := int64(buf.ReadUnsignedShort())
	if !kit.Check(humidity, 15) {
		pos.Set(model.KeyHumidity, float64(kit.To(humidity, 15))/10.0)
	}
}

func (d *Decoder) Decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	buf.Skip(2)             // header
	buf.ReadUnsignedShort() // length
	if buf.ReadUnsignedShort() != 0x2424 {
		return nil
	}
	hardware := buf.ReadUnsignedShort()
	firmware := buf.ReadUnsignedInt()

	imei := kit.SubstringFrom(buf.ReadSlice(8).HexDump(), 1)
	ds := m.Session(imei)
	if ds == nil {
		return nil
	}

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	pos.Set(model.KeyVersionHw, hardware)
	pos.Set(model.KeyVersionFw, firmware)

	deviceTime := readDate(buf)
	pos.DeviceTime = model.NewTime(deviceTime)

	// GPS info
	if hardware == 0x406 || !decodeGps(pos, buf, hardware) {
		m.LastLocation(pos, &deviceTime)
	}

	// LBS info
	blockLength := buf.ReadUnsignedShort()
	blockEnd := buf.ReaderIndex() + blockLength

	if blockLength > 0 {
		switch hardware {
		case 0x10A, 0x10B, 0x406:
			lac := buf.ReadUnsignedShort()
			pos.Network = model.NetworkOf(m.CellTowerFromLacCid(lac, int64(buf.ReadUnsignedShort())))
		case 0x407:
			network := model.NewNetwork()
			count := buf.ReadUnsignedByte()
			for range count {
				buf.ReadUnsignedByte() // signal information
				mcc := kit.ReadBCD(buf, 4)
				mnc := kit.ReadBCD(buf, 4) % 1000
				lac := buf.ReadUnsignedShort()
				network.AddCellTower(model.NewCellTower(mcc, mnc, lac, buf.ReadUnsignedInt()))
			}
			pos.Network = network
		case 0x40A:
			network := model.NewNetwork()
			count := buf.ReadUnsignedByte()
			for range count {
				signalInfo := buf.ReadUnsignedByte()
				typ := signalInfo >> 5
				cellEnd := buf.ReaderIndex() + (signalInfo & 0x1F)

				mcc := kit.ReadBCD(buf, 4)
				mnc := kit.ReadBCD(buf, 4) % 1000
				lac := buf.ReadUnsignedShort()
				tower := model.NewCellTower(mcc, mnc, lac, buf.ReadUnsignedInt())

				switch typ {
				case 0b000:
					tower.SetSignalStrength(-buf.ReadUnsignedByte())
				case 0b110:
					buf.ReadUnsignedShort() // pci
					buf.Skip(3)             // earfcn
					buf.ReadUnsignedByte()  // rsrp
					buf.ReadUnsignedByte()  // rsrq
					tower.SetSignalStrength(-buf.ReadUnsignedByte())
				}

				buf.SetReaderIndex(cellEnd)
				network.AddCellTower(tower)
			}
			pos.Network = network
		}
	}

	buf.SetReaderIndex(blockEnd)

	// Status info
	blockLength = buf.ReadUnsignedShort()
	blockEnd = buf.ReaderIndex() + blockLength

	if hardware == 0x40A {
		if blockLength >= 14 {
			pos.AddAlarm(decodeAlarm(buf.ReadUnsignedByte()))
			pos.Set("terminalInfo", buf.ReadUnsignedByte())
			pos.Set(model.KeyRssi, buf.ReadUnsignedByte())
			pos.Set("gsmStatus", buf.ReadUnsignedByte())
			pos.Set(model.KeyBattery, float64(buf.ReadUnsignedShort())/100.0)
			setTemperature(pos, buf)
		}
	} else {
		if hardware == 0x407 || blockLength >= 13 {
			pos.AddAlarm(decodeAlarm(buf.ReadUnsignedByte()))
			pos.Set("terminalInfo", buf.ReadUnsignedByte())

			if hardware != 0x407 {
				status := int64(buf.ReadUnsignedByte())
				pos.Set(model.PrefixOut+"1", kit.Check(status, 0))
				pos.Set(model.PrefixOut+"2", kit.Check(status, 1))
				status = int64(buf.ReadUnsignedByte())
				pos.Set(model.PrefixIn+"1", kit.Check(status, 4))
				if kit.Check(status, 0) {
					pos.AddAlarm(model.AlarmSos)
				}
			}

			pos.Set(model.KeyRssi, buf.ReadUnsignedByte())
			pos.Set("gsmStatus", buf.ReadUnsignedByte())
			pos.Set(model.KeyBattery, float64(buf.ReadUnsignedShort())/100.0)

			if hardware != 0x407 {
				pos.Set(model.KeyPower, buf.ReadUnsignedShort())
				pos.Set(model.PrefixAdc+"1", buf.ReadUnsignedShort())
				pos.Set(model.PrefixAdc+"2", buf.ReadUnsignedShort())
			} else {
				setTemperature(pos, buf)
				pos.Set("lightSensor", buf.ReadUnsignedByte() == 0)
			}
		}

		if blockLength >= 15 {
			pos.Set(model.PrefixTemp+"1", buf.ReadUnsignedShort())
		}
	}

	buf.SetReaderIndex(blockEnd)

	if hardware == 0x10B {
		decodeCards(pos, buf)
		buf.Skip(buf.ReadUnsignedShort()) // temperature
		buf.Skip(buf.ReadUnsignedShort()) // lock
		decodePassengers(pos, buf)
	}

	if hardware == 0x153 || hardware == 0x406 {
		decodeTags(pos, buf, hardware)
	}

	if hardware == 0x40A {
		blockLength = buf.ReadUnsignedShort()
		blockEnd = buf.ReaderIndex() + blockLength

		if blockLength > 0 {
			network := pos.Network
			if network == nil {
				network = model.NewNetwork()
			}
			for buf.ReaderIndex() < blockEnd {
				mac := kit.Format("%02x:%02x:%02x:%02x:%02x:%02x",
					buf.ReadUnsignedByte(), buf.ReadUnsignedByte(), buf.ReadUnsignedByte(),
					buf.ReadUnsignedByte(), buf.ReadUnsignedByte(), buf.ReadUnsignedByte())
				network.AddWifiAccessPoint(model.NewWifiAccessPoint(mac, -buf.ReadUnsignedByte()))
			}
			pos.Network = network
		}

		buf.SetReaderIndex(blockEnd)
	}

	if d.Ack {
		sendResponse(m, buf.GetUnsignedShort(buf.WriterIndex()-6))
	}

	return pos
}
