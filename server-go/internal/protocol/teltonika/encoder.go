package teltonika

import (
	"regexp"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

func encodeContent(content []byte) *kit.Buf {
	buf := kit.Empty()
	buf.WriteInt(0)
	buf.WriteInt(int64(len(content) + 8))
	buf.WriteByte8(codec12)
	buf.WriteByte8(1) // quantity
	buf.WriteByte8(5) // type
	buf.WriteInt(int64(len(content)))
	buf.WriteBytes(content)
	buf.WriteByte8(1) // quantity
	buf.WriteInt(int64(kit.CRC16(kit.CRC16_IBM, buf.Bytes()[8:])))
	return buf
}

var hexData = regexp.MustCompile(`^(?:[0-9a-fA-F]{2})+$`)

// encode is TeltonikaProtocolEncoder: codec 12 commands.
func encode(_ *ingest.Message, c *model.Command) any {
	switch c.Type {
	case model.CommandEngineStop:
		return encodeContent(kit.ASCIIBytes("setdigout 1"))
	case model.CommandEngineResume:
		return encodeContent(kit.ASCIIBytes("setdigout 0"))
	case model.CommandCustom:
		data := kit.CommandString(c, model.CommandKeyData)
		if data == nil {
			kit.Throw("NullPointerException: no data")
		}
		if hexData.MatchString(*data) {
			return encodeContent(kit.ParseHex(*data))
		}
		return encodeContent(kit.ASCIIBytes(*data))
	}
	return nil
}
