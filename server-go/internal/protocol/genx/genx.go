// Package genx is the GenX protocol (GenxProtocol): comma-separated text lines whose
// columns are configured (genx.reportColumns).
package genx

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/config"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one TCP server of lines.
func Protocol(cfg *config.Config) (ingest.Protocol, error) {
	columns, err := ReadColumns(cfg, "genx")
	if err != nil {
		return ingest.Protocol{}, err
	}
	return ingest.Protocol{
		Name: "genx",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.NewLineFramer(ingest.MaxFrameLength),
				Text:    true,
				Decoder: &Decoder{ReportColumns: columns},
			}
		}}},
	}, nil
}

// ReadColumns reads <protocol>.reportColumns ("1,2,3,4" by default).
func ReadColumns(cfg *config.Config, protocol string) (columns []int, err error) {
	defer kit.Recover(&err)
	return ParseColumns(cfg.String(protocol+".reportColumns", "1,2,3,4")), nil
}

// ParseColumns is setReportColumns: comma-separated column ids (throws on a bad number).
func ParseColumns(format string) []int {
	parts := kit.Split(format, ",")
	columns := make([]int, len(parts))
	for i, p := range parts {
		columns[i] = kit.Int(p)
	}
	return columns
}

// Decoder is GenxProtocolDecoder.
type Decoder struct {
	ReportColumns []int
}

const dateFormat = "MM/dd/yyyy HH:mm:ss" // in the server's zone

func (d *Decoder) Decode(m *ingest.Message, msg any) any {
	values := kit.Split(msg.(string), ",")

	pos := m.NewPosition()
	pos.Valid = true

	for i := 0; i < min(len(values), len(d.ReportColumns)); i++ {
		switch d.ReportColumns[i] {
		case 1, 28:
			if ds := m.Session(values[i]); ds != nil {
				pos.DeviceID = ds.DeviceID
			}
		case 2:
			pos.SetTime(kit.NewDateFormat(dateFormat, m.Zone()).Parse(values[i]))
		case 3:
			pos.SetLatitude(kit.Double(values[i]))
		case 4:
			pos.SetLongitude(kit.Double(values[i]))
		case 11:
			pos.Set(model.KeyIgnition, values[i] == "ON")
		case 13:
			pos.Speed = units.KnotsFromKph(float64(kit.Int(values[i])))
		case 17:
			pos.Course = float64(kit.Int(values[i]))
		case 23:
			pos.Set(model.KeyOdometer, kit.Double(values[i])*1000)
		case 27:
			pos.Altitude = units.MetersFromFeet(float64(kit.Int(values[i])))
		case 46:
			pos.Set(model.KeySatellites, kit.Int(values[i]))
		}
	}

	if pos.DeviceID != 0 {
		return pos
	}
	return nil
}
