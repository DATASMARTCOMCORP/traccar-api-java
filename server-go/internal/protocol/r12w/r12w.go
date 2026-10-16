// Package r12w is the R12W protocol (R12wProtocol): "$HX" text lines over TCP.
package r12w

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "r12w",
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

func decode(m *ingest.Message, msg any) any {
	values := kit.Split(msg.(string), ",")
	typ, id := values[1], values[2]
	if m.Session(id) == nil {
		return nil
	}
	if typ == "0001" && m.HasChannel() {
		sentence := kit.Format("$HX,%s,%s,%s,#", "1001", id, values[3]+",OK")
		sentence += kit.Format(",%02x,\r\n", kit.XORString(sentence))
		m.Send(sentence)
	}
	return nil
}
