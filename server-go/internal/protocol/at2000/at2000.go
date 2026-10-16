// Package at2000 is the AT2000 protocol (At2000Protocol): 16-byte blocks over TCP; after
// the device id message the track records are AES-CBC encrypted, one cipher stream per
// connection.
package at2000

import (
	"crypto/aes"
	"crypto/cipher"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "at2000",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{Framer: Framer(), Decoder: &Decoder{}}
		}}},
	}
}

const (
	blockLength = 16
	ackLength   = 496

	msgAcknowledgement = 0x00
	msgDeviceID        = 0x01
	msgTrackRequest    = 0x88
	msgTrackResponse   = 0x89
)

func getUnsignedMediumLE(buf *kit.Buf, i int) int {
	return buf.GetUnsignedByte(i) | buf.GetUnsignedByte(i+1)<<8 | buf.GetUnsignedByte(i+2)<<16
}

// framer is At2000FrameDecoder: frames are whole blocks, the length is in the header (one
// byte later in the first frame), and received data is acknowledged once per frame or
// every 496 bytes.
type framer struct {
	started      bool
	current      *kit.Buf
	acknowledged int
}

// Framer returns a new At2000FrameDecoder.
func Framer() ingest.Framer {
	f := &framer{}
	return &ingest.BaseFramer{Decode: f.decode}
}

func sendResponse(m *ingest.Message) {
	if !m.HasChannel() {
		return
	}
	response := kit.Empty()
	response.WriteByte8(msgAcknowledgement)
	response.WriteMedium(1)
	response.WriteByte8(0x00) // success
	response.WriteZero(2*blockLength - response.WriterIndex())
	m.Send(response)
}

func (f *framer) decode(m *ingest.Message, buf *kit.Buf) any {
	if buf.ReadableBytes() < 5 {
		return nil
	}

	var length int
	if !f.started {
		f.started = true
		length = getUnsignedMediumLE(buf, buf.ReaderIndex()+2)
	} else {
		length = getUnsignedMediumLE(buf, buf.ReaderIndex()+1)
	}

	length += blockLength
	if length%blockLength != 0 {
		length = (length/blockLength + 1) * blockLength
	}

	if (buf.ReadableBytes() >= length || buf.ReadableBytes()%ackLength == 0) &&
		(buf != f.current || buf.ReadableBytes() > f.acknowledged) {
		sendResponse(m)
		f.current = buf
		f.acknowledged = buf.ReadableBytes()
	}

	if buf.ReadableBytes() >= length {
		return buf.ReadSlice(length)
	}
	return nil
}

var key = kit.ParseHex("000102030405060708090a0b0c0d0e0f")

// stream is Cipher.update of AES/CBC/NoPadding: blocks chain across calls and a partial
// block waits for the rest.
type stream struct {
	mode    cipher.BlockMode
	pending []byte
}

func (s *stream) update(data []byte) []byte {
	s.pending = append(s.pending, data...)
	n := len(s.pending) / blockLength * blockLength
	out := make([]byte, n)
	s.mode.CryptBlocks(out, s.pending[:n])
	s.pending = append([]byte(nil), s.pending[n:]...)
	return out
}

// Decoder is At2000ProtocolDecoder: it holds the connection's cipher.
type Decoder struct {
	cipher *stream
}

func sendRequest(m *ingest.Message) {
	if !m.HasChannel() {
		return
	}
	response := kit.Empty()
	response.WriteByte8(msgTrackRequest)
	response.WriteMedium(0)
	response.WriteZero(blockLength - response.WriterIndex())
	m.Send(response)
}

func (d *Decoder) Decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	if buf.GetUnsignedByte(buf.ReaderIndex()) == 0x01 {
		buf.ReadUnsignedByte() // codec id
	}

	typ := buf.ReadUnsignedByte()
	buf.ReadUnsignedMediumLE() // length
	buf.Skip(blockLength - 1 - 3)

	switch typ {
	case msgDeviceID:
		imei := buf.ReadString(15)
		if m.Session(imei) != nil {
			iv := buf.ReadBytes(blockLength)
			block, err := aes.NewCipher(key)
			if err != nil {
				panic(err)
			}
			d.cipher = &stream{mode: cipher.NewCBCDecrypter(block, iv)}
			d.cipher.update(buf.ReadBytes(blockLength))
		}
		sendRequest(m)
	case msgTrackResponse:
		ds := m.Session()
		if ds == nil {
			return nil
		}
		if buf.Capacity() <= blockLength {
			return nil // empty message
		}
		data := buf.ReadBytes(buf.Capacity() - blockLength)
		return decodeTrack(m, ds, kit.NewBuf(d.cipher.update(data)))
	}
	return nil
}

func decodeTrack(m *ingest.Message, ds *ingest.DeviceSession, buf *kit.Buf) []*model.Position {
	positions := []*model.Position{}
	for buf.ReadableBytes() >= 63 {
		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID

		buf.ReadUnsignedShortLE() // index
		buf.ReadUnsignedShortLE() // reserved

		pos.Valid = true
		pos.SetTime(time.UnixMilli(buf.ReadLongLE() * 1000))
		pos.SetLatitude(float64(buf.ReadFloatLE()))
		pos.SetLongitude(float64(buf.ReadFloatLE()))
		pos.Altitude = float64(buf.ReadFloatLE())
		pos.Speed = units.KnotsFromKph(float64(buf.ReadFloatLE()))
		pos.Course = float64(buf.ReadFloatLE())

		buf.ReadUnsignedIntLE()   // geozone event
		buf.ReadUnsignedIntLE()   // io events
		buf.ReadUnsignedIntLE()   // geozone value
		buf.ReadUnsignedIntLE()   // io values
		buf.ReadUnsignedShortLE() // operator

		pos.Set(model.PrefixAdc+"1", buf.ReadUnsignedShortLE())
		pos.Set(model.PrefixAdc+"2", buf.ReadUnsignedShortLE())
		pos.Set(model.KeyPower, float64(buf.ReadUnsignedShortLE())/1000.0)

		buf.ReadUnsignedShortLE() // cid
		pos.Set(model.KeyRssi, buf.ReadUnsignedByte())
		buf.ReadUnsignedByte() // current profile

		pos.Set(model.KeyBattery, buf.ReadUnsignedByte())
		pos.Set(model.PrefixTemp+"1", buf.ReadUnsignedByte())
		pos.Set(model.KeySatellites, buf.ReadUnsignedByte())

		positions = append(positions, pos)
	}
	return positions
}
