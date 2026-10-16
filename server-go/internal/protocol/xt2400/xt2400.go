// Package xt2400 is the Xt2400 protocol (Xt2400Protocol): UDP binary messages whose
// fields follow a configured format (":wycfg pcr[...]" lines in xt2400.config).
package xt2400

import (
	"regexp"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/config"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one UDP server. The decoder reads
// xt2400.config.
func Protocol(cfg *config.Config) (ingest.Protocol, error) {
	configString, hasConfig := cfg.Lookup("xt2400.config")
	return ingest.Protocol{
		Name: "xt2400",
		Servers: []ingest.Server{{Datagram: true, NewPipeline: func() ingest.Pipeline {
			d := &Decoder{}
			if hasConfig {
				d.SetConfig(configString)
			}
			return ingest.Pipeline{Decoder: d}
		}}},
	}, nil
}

var tagLengths = map[int]int{0x95: 24, 0xD0: 21}

func addTagLength(length int, tags ...int) {
	for _, tag := range tags {
		tagLengths[tag] = length
	}
}

func init() {
	addTagLength(1,
		0x01, 0x02, 0x04, 0x0b, 0x0c, 0x0d, 0x12, 0x13,
		0x16, 0x17, 0x1c, 0x1f, 0x23, 0x2c, 0x2d, 0x30,
		0x31, 0x32, 0x33, 0x34, 0x35, 0x36, 0x37, 0x38,
		0x39, 0x3a, 0x3b, 0x3c, 0x3d, 0x3e, 0x40, 0x41,
		0x53, 0x66, 0x69, 0x6a, 0x93, 0x94, 0x96)
	addTagLength(2,
		0x05, 0x09, 0x0a, 0x14, 0x15, 0x1d, 0x1e, 0x24,
		0x26, 0x42, 0x43, 0x44, 0x45, 0x46, 0x47, 0x48,
		0x49, 0x57, 0x58, 0x59, 0x5a, 0x6b, 0x6f, 0x7A,
		0x7B, 0x7C, 0x7d, 0x7E, 0x7F, 0x80, 0x81, 0x82,
		0x83, 0x84, 0x85, 0x86, 0xc8)
	addTagLength(4,
		0x03, 0x06, 0x07, 0x08, 0x0e, 0x0f, 0x10, 0x11,
		0x18, 0x19, 0x1a, 0x1b, 0x20, 0x21, 0x22, 0x2e,
		0x2f, 0x4a, 0x4b, 0x4c, 0x4d, 0x4e, 0x4f, 0x50,
		0x51, 0x52, 0x54, 0x55, 0x56, 0x5b, 0x5c, 0x5d,
		0x5e, 0x5f, 0x60, 0x61, 0x62, 0x68, 0x6e, 0x71,
		0x72, 0x74, 0x75, 0x76, 0x77, 0x78, 0x79, 0x87,
		0x88, 0x89, 0x8a, 0x8b, 0x8c, 0x8d)
}

func tagLength(tag int) int {
	length, ok := tagLengths[tag]
	if !ok {
		kit.Throw("IllegalArgumentException: Unknown tag: 0x%02X", tag)
	}
	return length
}

// Decoder is Xt2400ProtocolDecoder: formats maps a message's first byte to its field tags.
type Decoder struct {
	formats map[int][]byte
}

var configLine = regexp.MustCompile(`:wycfg pcr\[\d+] ([0-9a-fA-F]{2})[0-9a-fA-F]{2}([0-9a-fA-F]+)`)

// SetConfig is setConfig: it adds (or replaces) the formats of a configuration.
func (d *Decoder) SetConfig(configString string) {
	if d.formats == nil {
		d.formats = map[int][]byte{}
	}
	for _, match := range configLine.FindAllStringSubmatch(configString, -1) {
		d.formats[kit.IntRadix(match[1], 16)] = kit.ParseHex(match[2])
	}
}

// Decode decodes one message.
func (d *Decoder) Decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	var format []byte
	if len(d.formats) > 1 {
		format = d.formats[buf.GetUnsignedByte(buf.ReaderIndex())]
	} else {
		for _, f := range d.formats {
			format = f
		}
	}

	if format == nil {
		return nil
	}

	pos := m.NewPosition()

	for _, b := range format {
		switch tag := int(b); tag {
		case 0x03:
			ds := m.Session(kit.Format("%d", buf.ReadUnsignedInt()))
			if ds == nil {
				return nil
			}
			pos.DeviceID = ds.DeviceID
		case 0x04:
			pos.Set(model.KeyEvent, buf.ReadUnsignedByte())
		case 0x05:
			pos.Set(model.KeyIndex, buf.ReadUnsignedShort())
		case 0x06:
			pos.SetTime(time.UnixMilli(buf.ReadUnsignedInt() * 1000))
		case 0x07:
			pos.SetLatitude(float64(buf.ReadInt()) / 1000000.0)
		case 0x08:
			pos.SetLongitude(float64(buf.ReadInt()) / 1000000.0)
		case 0x09:
			pos.Altitude = float64(buf.ReadShort()) / 10.0
		case 0x0a:
			pos.Course = float64(buf.ReadShort()) / 10.0
		case 0x0b:
			pos.Speed = units.KnotsFromKph(float64(buf.ReadUnsignedByte()))
		case 0x10:
			pos.Set(model.KeyOdometerTrip, buf.ReadUnsignedInt())
		case 0x12:
			pos.Set(model.KeyHdop, float64(buf.ReadUnsignedByte())/10.0)
		case 0x13:
			pos.Set(model.KeySatellites, buf.ReadUnsignedByte())
		case 0x14:
			pos.Set(model.KeyRssi, buf.ReadShort()) // a Short, stored as an Integer
		case 0x16:
			pos.Set(model.KeyBattery, float64(buf.ReadUnsignedByte())/10.0)
		case 0x17:
			pos.Set(model.KeyPower, float64(buf.ReadUnsignedByte())/10.0)
		case 0x57:
			pos.Set(model.KeyObdSpeed, buf.ReadUnsignedShort())
		case 0x65:
			pos.Set(model.KeyVin, buf.ReadString(17))
		case 0x6C:
			buf.ReadUnsignedByte() // mil
			ecuCount := buf.ReadUnsignedByte()
			for i := 0; i < ecuCount; i++ {
				buf.ReadUnsignedByte() // ecu id
				buf.Skip(buf.ReadUnsignedByte() * 6)
			}
		case 0x73:
			pos.Set(model.KeyVersionFw, kit.Trim(buf.ReadString(16)))
		default:
			buf.Skip(tagLength(tag))
		}
	}

	if pos.Latitude != 0 && pos.Longitude != 0 {
		pos.Valid = true
	} else {
		var deviceTime *time.Time
		if !pos.DeviceTime.IsZero() {
			deviceTime = &pos.DeviceTime.Time
		}
		m.LastLocation(pos, deviceTime)
	}

	return pos
}
