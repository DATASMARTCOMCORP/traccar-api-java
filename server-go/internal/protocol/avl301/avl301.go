// Package avl301 is the AVL-301 protocol (Avl301Protocol): '$'-framed binary messages
// with a length byte over TCP.
package avl301

import (
	"strconv"
	"strings"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "avl301",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.NewLengthFramer(ingest.MaxFrameLength, 2, 1, -3, 0),
				Decoder: ingest.DecoderFunc(decode),
			}
		}}},
	}
}

const (
	msgLogin        = 'L'
	msgStatus       = 'H'
	msgGpsLbsStatus = '$'
)

func readImei(buf *kit.Buf) string {
	var imei strings.Builder
	imei.WriteString(strconv.Itoa(buf.ReadUnsignedByte() & 0x0F))
	for i := 0; i < 7; i++ {
		b := buf.ReadUnsignedByte()
		imei.WriteString(strconv.Itoa((b & 0xF0) >> 4))
		imei.WriteString(strconv.Itoa(b & 0x0F))
	}
	return imei.String()
}

func sendResponse(m *ingest.Message, typ int) {
	if !m.HasChannel() {
		return
	}
	response := kit.Empty()
	response.WriteByte8('$')
	response.WriteByte8(typ)
	response.WriteByte8('#')
	response.WriteByte8('\r')
	response.WriteByte8('\n')
	m.Send(response)
}

// cellFromLacCid is CellTower.fromLacCid: the country and network come from the
// geolocation.mcc and geolocation.mnc keys (0 when unset; a bad value throws).
func cellFromLacCid(m *ingest.Message, lac int, cid int64) model.CellTower {
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

func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	buf.Skip(1) // header
	typ := buf.ReadUnsignedByte()
	buf.ReadUnsignedByte() // length

	switch typ {
	case msgLogin:
		if m.Session(readImei(buf)) == nil {
			sendResponse(m, typ)
		}
	case msgStatus:
		sendResponse(m, typ)
	case msgGpsLbsStatus:
		ds := m.Session()
		if ds == nil {
			return nil
		}
		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID

		date := kit.NewDateBuilder(nil).
			SetDate(buf.ReadUnsignedByte(), buf.ReadUnsignedByte(), buf.ReadUnsignedByte()).
			SetTime(buf.ReadUnsignedByte(), buf.ReadUnsignedByte(), buf.ReadUnsignedByte())
		pos.SetTime(date.Date())

		gpsLength := buf.ReadUnsignedByte() // gps len and sat
		pos.Set(model.KeySatellites, gpsLength&0xf)
		pos.Set(model.KeySatellitesVisible, buf.ReadUnsignedByte()) // satellites

		latitude := float64(buf.ReadUnsignedInt()) / 600000.0
		longitude := float64(buf.ReadUnsignedInt()) / 600000.0
		pos.Speed = float64(buf.ReadUnsignedByte())

		union := buf.ReadUnsignedShort() // course and flags
		pos.Course = float64(union & 0x03FF)
		pos.Valid = union&0x1000 != 0
		if union&0x0400 != 0 {
			latitude = -latitude
		}
		if union&0x0800 != 0 {
			longitude = -longitude
		}
		pos.SetLatitude(latitude)
		pos.SetLongitude(longitude)

		if union&0x4000 != 0 {
			pos.Set("acc", union&0x8000 != 0)
		}

		lac := buf.ReadUnsignedShort()
		cid := buf.ReadUnsignedMedium()
		pos.Network = model.NetworkOf(cellFromLacCid(m, lac, int64(cid)))

		pos.AddAlarm(model.AlarmGeneral)
		flags := buf.ReadUnsignedByte()
		pos.Set("acc", flags&0x2 != 0)

		// parse other flags
		pos.Set(model.KeyPower, buf.ReadUnsignedByte())
		pos.Set(model.KeyRssi, buf.ReadUnsignedByte())
		return pos
	}
	return nil
}
