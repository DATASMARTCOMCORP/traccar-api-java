// Package skypatrol is the Skypatrol protocol (SkypatrolProtocol): binary UDP reports
// whose fields are selected by a mask.
package skypatrol

import (
	"log/slog"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/config"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

const name = "skypatrol"

// Protocol is the protocol's definition: one UDP server. skypatrol.mask is the mask of
// reports that don't carry one.
func Protocol(cfg *config.Config) (ingest.Protocol, error) {
	mask, err := cfg.Int(name+".mask", 0)
	if err != nil {
		return ingest.Protocol{}, err
	}
	d := &Decoder{DefaultMask: int64(mask)}
	return ingest.Protocol{
		Name: name,
		Servers: []ingest.Server{{Datagram: true, NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{Decoder: d}
		}}},
	}, nil
}

// Decoder is SkypatrolProtocolDecoder.
type Decoder struct {
	DefaultMask int64
}

func convertCoordinate(coordinate int64) float64 {
	sign := 1
	if coordinate > 0x7fffffff {
		sign = -1
		coordinate = 0xffffffff - coordinate
	}
	degrees := coordinate / 1000000
	minutes := float64(coordinate%1000000) / 10000.0
	return float64(sign) * (float64(degrees) + minutes/60)
}

func (d *Decoder) Decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	apiNumber := buf.ReadUnsignedShort()
	commandType := buf.ReadUnsignedByte()
	messageType := kit.FromInt(buf.ReadUnsignedByte(), 4)
	mask := d.DefaultMask
	if buf.ReadUnsignedByte() == 4 {
		mask = buf.ReadUnsignedInt()
	}

	// Binary position report
	if !(apiNumber == 5 && commandType == 2 && messageType == 1 && kit.Check(mask, 0)) {
		return nil
	}

	pos := m.NewPosition()

	if kit.Check(mask, 1) {
		pos.Set(model.KeyStatus, buf.ReadUnsignedInt())
	}

	var id string
	switch {
	case kit.Check(mask, 23):
		id = kit.Trim(buf.GetString(buf.ReaderIndex(), 8))
		buf.Skip(8)
	case kit.Check(mask, 2):
		id = kit.Trim(buf.GetString(buf.ReaderIndex(), 22))
		buf.Skip(22)
	default:
		slog.Warn("No device id field")
		return nil
	}
	ds := m.Session(id)
	if ds == nil {
		return nil
	}
	pos.DeviceID = ds.DeviceID

	if kit.Check(mask, 3) {
		pos.Set(model.PrefixIo+"1", buf.ReadUnsignedShort())
	}
	if kit.Check(mask, 4) {
		pos.Set(model.PrefixAdc+"1", buf.ReadUnsignedShort())
	}
	if kit.Check(mask, 5) {
		pos.Set(model.PrefixAdc+"2", buf.ReadUnsignedShort())
	}
	if kit.Check(mask, 7) {
		buf.ReadUnsignedByte() // function category
	}

	dateBuilder := kit.NewDateBuilder(nil)
	if kit.Check(mask, 8) {
		dateBuilder.SetDateReverse(buf.ReadUnsignedByte(), buf.ReadUnsignedByte(), buf.ReadUnsignedByte())
	}
	if kit.Check(mask, 9) {
		pos.Valid = buf.ReadUnsignedByte() == 1 // gps status
	}
	if kit.Check(mask, 10) {
		pos.SetLatitude(convertCoordinate(buf.ReadUnsignedInt()))
	}
	if kit.Check(mask, 11) {
		pos.SetLongitude(convertCoordinate(buf.ReadUnsignedInt()))
	}
	if kit.Check(mask, 12) {
		pos.Speed = float64(buf.ReadUnsignedShort()) / 10.0
	}
	if kit.Check(mask, 13) {
		pos.Course = float64(buf.ReadUnsignedShort()) / 10.0
	}
	if kit.Check(mask, 14) {
		dateBuilder.SetTime(buf.ReadUnsignedByte(), buf.ReadUnsignedByte(), buf.ReadUnsignedByte())
	}
	pos.SetTime(dateBuilder.Date())

	if kit.Check(mask, 15) {
		pos.Altitude = float64(buf.ReadMedium())
	}
	if kit.Check(mask, 16) {
		pos.Set(model.KeySatellites, buf.ReadUnsignedByte())
	}
	if kit.Check(mask, 17) {
		pos.Set(model.KeyBattery, buf.ReadUnsignedShort())
	}
	if kit.Check(mask, 20) {
		pos.Set(model.KeyOdometerTrip, buf.ReadUnsignedInt())
	}
	if kit.Check(mask, 21) {
		pos.Set(model.KeyOdometer, buf.ReadUnsignedInt())
	}
	if kit.Check(mask, 22) {
		buf.Skip(6) // time of message generation
	}
	if kit.Check(mask, 24) {
		pos.Set(model.KeyPower, float64(buf.ReadUnsignedShort())/1000.0)
	}
	if kit.Check(mask, 25) {
		buf.Skip(18) // gps overspeed
	}
	if kit.Check(mask, 26) {
		buf.Skip(54) // cell information
	}
	if kit.Check(mask, 28) {
		pos.Set(model.KeyIndex, buf.ReadUnsignedShort())
	}

	return pos
}
