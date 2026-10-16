// Package pricol is the Pricol protocol (PricolProtocol): fixed 64-byte binary reports
// over TCP and UDP.
package pricol

import (
	"strconv"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: a TCP server (64-byte frames) and a UDP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "pricol",
		Servers: []ingest.Server{
			{NewPipeline: func() ingest.Pipeline {
				return ingest.Pipeline{Framer: ingest.FixedFramer{Length: 64}, Decoder: ingest.DecoderFunc(decode)}
			}},
			{Datagram: true, NewPipeline: func() ingest.Pipeline {
				return ingest.Pipeline{Decoder: ingest.DecoderFunc(decode)}
			}},
		},
	}
}

func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	buf.ReadUnsignedByte() // header

	ds := m.Session(buf.ReadString(7))
	if ds == nil {
		return nil
	}

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	pos.Set("eventType", buf.ReadUnsignedByte())
	pos.Set("packetVersion", buf.ReadUnsignedByte())
	pos.Set(model.KeyStatus, buf.ReadUnsignedByte())
	pos.Set(model.KeyRssi, buf.ReadUnsignedByte())
	pos.Set(model.KeyGps, buf.ReadUnsignedByte())

	pos.SetTime(kit.NewDateBuilder(nil).
		SetDateReverse(buf.ReadUnsignedByte(), buf.ReadUnsignedByte(), buf.ReadUnsignedByte()).
		SetTime(buf.ReadUnsignedByte(), buf.ReadUnsignedByte(), buf.ReadUnsignedByte()).Date())

	pos.Valid = true

	lat := float64(buf.GetUnsignedShort(buf.ReaderIndex()) / 100)
	lat += float64(buf.ReadUnsignedShort()%100*10000+buf.ReadUnsignedShort()) / 600000.0
	if buf.ReadUnsignedByte() == 'S' {
		lat = -lat
	}
	pos.SetLatitude(lat)

	lon := float64(buf.GetUnsignedMedium(buf.ReaderIndex()) / 100)
	lon += float64(buf.ReadUnsignedMedium()%100*10000+buf.ReadUnsignedShort()) / 600000.0
	if buf.ReadUnsignedByte() == 'W' {
		lon = -lon
	}
	pos.SetLongitude(lon)

	pos.Speed = units.KnotsFromKph(float64(buf.ReadUnsignedByte()))

	pos.Set(model.KeyInput, buf.ReadUnsignedShort())
	pos.Set(model.KeyOutput, buf.ReadUnsignedByte())

	pos.Set("analogAlerts", buf.ReadUnsignedByte())
	pos.Set("customAlertTypes", buf.ReadUnsignedShort())

	for i := 1; i <= 5; i++ {
		pos.Set(model.PrefixAdc+strconv.Itoa(i), buf.ReadUnsignedShort())
	}

	pos.Set(model.KeyOdometer, buf.ReadUnsignedMedium())
	pos.Set(model.KeyRpm, buf.ReadUnsignedShort())

	m.Send(kit.NewBuf(kit.NettyBytes("ACK")))

	return pos
}
