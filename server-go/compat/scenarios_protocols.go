package main

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"slices"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/oracle"
)

// protocolsSuite is Step 7: the decoder vectors of the Java tests of every ported TCP/UDP
// protocol, sent over real connections (unknown devices are registered, so every unique
// id has a device), and queued commands delivered when their device connects. Replies
// are compared byte for byte, then every table.
//
// The SIR Suntech vectors are left out: Go keeps SIR's two's-complement fix, which the
// stock jar lost (MIGRATION.md §8); the oracle tests check them against SIR's decoder.
func protocolsSuite() suite {
	config := map[string]string{
		"server.buffering.threshold": "0",
		"database.registerUnknown":   "true",
	}
	seed := []Step{
		postJSON("", "/api/users", `{"name":"Admin","email":"admin@example.com","password":"secret"}`).save("admin", "id"),
		login("admin", "admin@example.com", "secret"),
		postJSON("admin", "/api/devices", `{"name":"Suntech","uniqueId":"907131077"}`).save("st", "id"),
		postJSON("admin", "/api/devices", `{"name":"Universal","uniqueId":"0560001616"}`).save("stu", "id"),
		postJSON("admin", "/api/devices", `{"name":"Intellitrac","uniqueId":"359316075744331"}`).save("it", "id"),
		postJSON("admin", "/api/devices", `{"name":"Atrack","uniqueId":"358683064932578"}`).save("at", "id"),
	}
	// Each command is queued while its device is away, then sent through the protocol's
	// encoder (or as custom data) when the device reports. One per report: with several,
	// Java stores their queuedCommandSent events in identity-hash order.
	st := tcp("suntech", text("ST300STT;907131077;04;706;20190227;23:59:34;cc719;-12.963490;-038.499587;000.067;000.00;7;1;57095;12.50;000000;1;0337;000207;0.0;1;0;012E717F010000;1\r"))
	stu := tcp("suntech", text("STT;0560001616;BFFFFF;56;1.0.15;1;20200219;20:52:25;00008D6C;334;20;0925;24;+20.741764;-103.430364;0.00;0.00;19;1;00000001;00000000;2;1;1765;00008003;0.0;12.14;136598\r"))
	it := tcp("intellitrac", text("359316075744331,20201008181424,12.014662,57.826301,0,76,24,10,997,3,0,0.000,4.208,20201008181424,0\r\n"))
	queue := func(device, command string) Step {
		return postJSON("admin", "/api/commands/send", `{"deviceId":${`+device+`},`+command+`}`)
	}
	commands := Scenario{Name: "queued-commands", Steps: []Step{
		login("admin", "admin@example.com", "secret"),
		// Legacy Suntech: the prefix of the device's last message.
		queue("st", `"type":"rebootDevice"`), st,
		queue("st", `"type":"outputControl","attributes":{"index":2,"data":"1"}`), st,
		queue("st", `"type":"outputControl","attributes":{"index":3,"data":"0"}`), st,
		queue("st", `"type":"custom","attributes":{"data":"ST300CMD;907131077;02;Preset\\r"}`), st,
		queue("st", `"type":"engineStop"`), st,
		// Universal Suntech, and an output it can't encode (nothing sent).
		queue("stu", `"type":"engineStop"`), stu,
		queue("stu", `"type":"positionSingle"`), stu,
		queue("stu", `"type":"outputControl","attributes":{"index":5,"data":"0"}`), stu,
		queue("stu", `"type":"alarmArm"`), stu,
		// Intellitrac declares no data commands: custom text is sent unescaped, others fail.
		queue("it", `"type":"custom","attributes":{"data":"$ST+GETPOSITION\\r\\n"}`), it,
		queue("it", `"type":"engineStop"`), it,
		// Atrack: custom through its encoder; on UDP the device stays connected, so the next
		// command goes out live.
		queue("at", `"type":"custom","attributes":{"data":"AT$INFO=?"}`),
		udp("atrack", atrackText("@P,CA4B,0,2,358683064932578,1533634000,1533634000,1533634000,121562641,25082649,72,0,1638,15,0,0,1,0,,2000,2000,,%CI%MV%BV,143,0\r\n")),
		queue("at", `"type":"custom","attributes":{"data":"AT$FORM=?"}`),
		queue("at", `"type":"engineStop"`),
		tcp("atrack", atrackText("@P,CA4B,0,1,358683064932578,1533633976,1533633975,1533633975,121562641,25082649,72,0,1638,15,0,0,1,0,,2000,2000,,%CI%MV%BV,143,0\r\n")),
		get("admin", "/api/commands/send?deviceId=${at}"),
		get("admin", "/api/devices"),
	}}
	return suite{seed: seed, strictSeed: true, config: config, scenarios: append(vectorScenarios(), commands, mqttScenario())}
}

// vectorScenarios makes one scenario per Java test class: each decoder input on its own
// connection, framed as the device would send it.
func vectorScenarios() []Scenario {
	type source struct {
		test, protocol string
		frame          func(oracle.Record) ([]byte, bool)
		udp            bool
	}
	sources := []source{
		{"SuntechProtocolDecoderTest", "suntech", frameSuntech, false},
		{"IntellitracProtocolDecoderTest", "intellitrac", frameLine("\r\n"), false},
		{"AtrackProtocolDecoderTest", "atrack", frameAtrack, false},
		{"AtrackProtocolDecoderTest", "atrack", unframed, true},
	}
	var out []Scenario
	for _, s := range sources {
		records, err := oracle.Records(s.test)
		if err != nil {
			panic(err)
		}
		var msgs [][]byte
		for _, r := range records {
			if r.Kind != "decode" || r.Method == "testDecodeTwosComplement" || r.Method == "testDecodeBrazilianCoordinates" {
				continue
			}
			if m, ok := s.frame(r); ok {
				msgs = append(msgs, m)
			}
		}
		transport := "tcp"
		if s.udp {
			transport = "udp"
		}
		// Steps run on one side, then the other: short steps keep the servers' clocks
		// (server times, last updates) within the comparison's tolerance.
		var steps []Step
		for chunk := range slices.Chunk(msgs, 8) {
			steps = append(steps, Step{Device: &Traffic{Protocol: s.protocol, Transport: transport, Messages: chunk,
				Separate: true, ReplyIdle: 250 * time.Millisecond}})
		}
		steps = append(steps, get("admin", "/api/devices"))
		out = append(out, Scenario{Name: s.protocol + "-" + transport + "-vectors", Steps: steps})
	}
	return out
}

func input(r oracle.Record) []byte {
	if r.Input.Type == "string" {
		return []byte(r.Input.Text)
	}
	b, err := hex.DecodeString(r.Input.Hex)
	if err != nil {
		panic(fmt.Sprintf("%s:%d: %v", r.Method, r.Line, err))
	}
	return b
}

func unframed(r oracle.Record) ([]byte, bool) { return input(r), true }

func frameLine(ending string) func(oracle.Record) ([]byte, bool) {
	return func(r oracle.Record) ([]byte, bool) { return append(input(r), ending...), true }
}

// frameSuntech ends text messages with '\r' (SuntechFrameDecoder's delimiter); binary
// and zip frames carry their length.
func frameSuntech(r oracle.Record) ([]byte, bool) {
	b := input(r)
	if len(b) > 1 && (b[0] == 0x02 || b[1] == 0) {
		return b, true
	}
	return append(b, '\r'), true
}

// frameAtrack gives text reports their real length (AtrackFrameDecoder trusts it) and
// ends other text ($INFO, command replies) with CRLF; binary frames and keep-alives carry
// their length.
func frameAtrack(r oracle.Record) ([]byte, bool) {
	b := input(r)
	switch {
	case len(b) > 2 && b[0] == '@' && b[2] == ',':
		return atrackText(string(b)), true
	case len(b) > 1 && (b[0] == '@' || b[0] == 0xfe && b[1] == 0x02):
		return b, true
	case !bytes.HasSuffix(b, []byte("\r\n")):
		return append(b, "\r\n"...), true
	}
	return b, true
}

// mqttScenario covers MqttDecoder, MqttEncoder and BaseMqttProtocolDecoder on the wire:
// MQTT 3.1.1 and 5 sessions, every QoS, messages Traccar ignores, a publish before
// CONNECT, and malformed packets (after which the connection ignores everything).
func mqttScenario() Scenario {
	iotm := hexBytes("020208009188752DE71203000109002000AD590500309635F3")
	pui := []byte(`{ "id": "015262001044848", "ts": "2023-06-01T03:09:51.362Z", "rpt": "hf", "location": { "lat": 33.91233, "lon": -84.20784 }, "bear": 70, "spd": 2482, "ign": "on" }`)
	step := func(protocol string, msgs ...[]byte) Step {
		return Step{Device: &Traffic{Protocol: protocol, Transport: "tcp", Messages: msgs, ReplyIdle: 300 * time.Millisecond}}
	}
	return Scenario{Name: "mqtt", Steps: []Step{
		step("iotm", mqttConnect("866000000000001", 4, 0), mqttSubscribe(7, 4), mqttPublish("dev/data", 1, 5, iotm, 4),
			mqttPublish("dev/data", 0, 0, iotm, 4), mqttPublish("dev/data", 2, 6, iotm, 4), []byte{0xC0, 0x00},
			[]byte{0x40, 0x02, 0x00, 0x05}, []byte{0xE0, 0x00}),
		// MQTT 5: properties in CONNECT, PUBLISH and the replies.
		step("iotm", mqttConnect("866000000000002", 5, 0), mqttSubscribe(9, 5), mqttPublish("dev/data", 1, 1, iotm, 5)),
		// Publish before CONNECT: no session, no reply.
		step("iotm", mqttPublish("dev/data", 1, 3, iotm, 4), mqttConnect("866000000000003", 4, 0), mqttPublish("dev/data", 1, 4, iotm, 4)),
		// Malformed: a wildcard topic, then a valid message that is ignored.
		step("iotm", mqttConnect("866000000000004", 4, 0), mqttPublish("dev/#", 1, 5, iotm, 4), mqttPublish("dev/data", 1, 6, iotm, 4)),
		// Malformed: unknown protocol level; a reserved connect flag; message type 0.
		step("iotm", mqttConnect("866000000000005", 6, 0), mqttPublish("dev/data", 1, 6, iotm, 4)),
		step("iotm", mqttConnect("866000000000006", 4, 0x01)),
		step("iotm", []byte{0x00, 0x00}, mqttConnect("866000000000007", 4, 0)),
		// Several messages in one write, and one split over two writes.
		step("iotm", append(mqttConnect("866000000000008", 4, 0), mqttPublish("dev/data", 1, 8, iotm, 4)...)),
		step("iotm", mqttConnect("866000000000009", 4, 0), mqttPublish("dev/data", 1, 9, iotm, 4)[:10],
			mqttPublish("dev/data", 1, 9, iotm, 4)[10:]),
		step("pui", mqttConnect("015262001044848", 4, 0), mqttPublish("pui/data", 1, 2, pui, 4),
			mqttPublish("pui/data", 0, 0, []byte(`{"rpt":"other"}`), 4), mqttPublish("pui/data", 1, 3, []byte(`not json`), 4)),
		get("admin", "/api/devices"),
	}}
}

func mqttFrame(header byte, body []byte) []byte {
	out := []byte{header}
	n := len(body)
	for {
		digit := byte(n % 128)
		n /= 128
		if n > 0 {
			digit |= 0x80
		}
		out = append(out, digit)
		if n == 0 {
			break
		}
	}
	return append(out, body...)
}

func mqttString(s string) []byte { return append([]byte{byte(len(s) >> 8), byte(len(s))}, s...) }

func mqttConnect(clientID string, level, flags byte) []byte {
	body := append(mqttString("MQTT"), level, flags|0x02, 0, 60)
	if level == 5 {
		body = append(body, 5, 0x11, 0, 0, 0, 30) // session expiry interval
	}
	return mqttFrame(0x10, append(body, mqttString(clientID)...))
}

func mqttSubscribe(packetID int, level byte) []byte {
	body := []byte{byte(packetID >> 8), byte(packetID)}
	if level == 5 {
		body = append(body, 0)
	}
	body = append(append(body, mqttString("cmd/#")...), 1)
	return mqttFrame(0x82, body)
}

func mqttPublish(topic string, qos, packetID int, payload []byte, level byte) []byte {
	body := mqttString(topic)
	if qos > 0 {
		body = append(body, byte(packetID>>8), byte(packetID))
	}
	if level == 5 {
		body = append(body, 2, 0x01, 1) // payload format indicator
	}
	return mqttFrame(byte(0x30|qos<<1), append(body, payload...))
}
