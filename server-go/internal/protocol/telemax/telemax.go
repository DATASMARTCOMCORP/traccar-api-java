// Package telemax is the Telemax protocol (TelemaxProtocol): hexadecimal text lines with
// a batch of positions over TCP, after a "%" identification line.
package telemax

import (
	"strings"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "telemax",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.NewLineFramer(ingest.MaxFrameLength),
				Text:    true,
				TextOut: true,
				Decoder: ingest.DecoderFunc(decode),
			}
		}}},
	}
}

// reader reads consecutive fixed-length fields of a sentence.
type reader struct {
	sentence string
	index    int
}

func (r *reader) read(length int) string {
	value := kit.Substring(r.sentence, r.index, r.index+length)
	r.index += length
	return value
}

func decode(m *ingest.Message, msg any) any {
	sentence := msg.(string)

	if strings.HasPrefix(sentence, "%") {
		length := kit.Int(kit.Substring(sentence, 1, 3))
		m.Session(kit.Substring(sentence, 3, 3+length))
		return nil
	}

	ds := m.Session()
	if ds == nil {
		return nil
	}

	r := &reader{sentence: sentence}
	if r.read(1) != "Y" {
		return nil
	}

	r.read(8)                           // command id
	r.read(6)                           // password
	r.read(kit.IntRadix(r.read(2), 16)) // unit id
	r.read(2)                           // frame count
	r.read(2)                           // data format
	interval := kit.IntRadix(r.read(4), 16)
	r.read(2) // info flags
	r.read(2) // version
	count := kit.IntRadix(r.read(2), 16)

	dateFormat := kit.NewDateFormat("yyMMddHHmmss", m.Zone())
	var t time.Time
	positions := []*model.Position{}
	for i := 0; i < count; i++ {
		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID

		speed := kit.IntRadix(r.read(2), 16)
		pos.Valid = kit.Check(int64(speed), 7)
		pos.Speed = float64(kit.ToInt(speed, 7))

		pos.SetLongitude(float64(kit.IntRadix(r.read(6), 16)-5400000) / 30000.0)
		pos.SetLatitude(float64(kit.IntRadix(r.read(6), 16)-5400000) / 30000.0)

		if i == 0 || i == count-1 {
			t = dateFormat.Parse(r.read(12))
			pos.Set(model.KeyStatus, r.read(8))
		} else {
			t = t.Add(time.Duration(interval) * time.Second)
		}
		pos.SetTime(t)

		positions = append(positions, pos)
	}

	return positions
}
