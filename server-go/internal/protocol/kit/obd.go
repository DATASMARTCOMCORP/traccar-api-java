package kit

import "github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"

// ObdDecoder. An entry is a key and a value (nil: Java's null entry, nothing set).

// ObdEntry is an ObdDecoder result: Position.add(entry) skips it when Key is "".
type ObdEntry struct {
	Key   string
	Value any
}

// Add is Position.add(entry): a null entry or value adds nothing.
func (e ObdEntry) Add(pos *model.Position) {
	if e.Key != "" && e.Value != nil {
		pos.Attributes.Set(e.Key, e.Value)
	}
}

// ObdDecode is ObdDecoder.decode(mode, value).
func ObdDecode(mode int, value string) ObdEntry {
	switch mode {
	case 0x01, 0x02:
		return ObdDecodeData(IntRadix(Substring(value, 0, 2), 16), LongRadix(SubstringFrom(value, 2), 16), true)
	case 0x03:
		return ObdDecodeCodes(value)
	}
	return ObdEntry{}
}

// ObdDecodeCodes is ObdDecoder.decodeCodes: the codes of each 4-digit hex group.
func ObdDecodeCodes(value string) ObdEntry {
	codes := ""
	for i := 0; i < Len(value)/4; i++ {
		codes += " " + ObdDecodeCode(IntRadix(Substring(value, i*4, (i+1)*4), 16))
	}
	if codes == "" {
		return ObdEntry{}
	}
	return ObdEntry{model.KeyDtcs, Trim(codes)}
}

// ObdDecodeCode is ObdDecoder.decodeCode: "P0123", "C...", "B...", "U...".
func ObdDecodeCode(value int) string {
	prefix := 'P'
	switch FromInt(value, 14) {
	case 1:
		prefix = 'C'
	case 2:
		prefix = 'B'
	case 3:
		prefix = 'U'
	}
	return Format("%c%04X", prefix, value&0x3FFF)
}

// ObdDecodeData is ObdDecoder.decodeData(pid, value, convert): values are Longs.
func ObdDecodeData(pid int, value int64, convert bool) ObdEntry {
	conv := func(c, raw int64) int64 {
		if convert {
			return c
		}
		return raw
	}
	switch pid {
	case 0x04:
		return ObdEntry{model.KeyEngineLoad, conv(value*100/255, value)}
	case 0x05:
		return ObdEntry{model.KeyCoolantTemp, conv(value-40, value)}
	case 0x0B:
		return ObdEntry{"mapIntake", value}
	case 0x0C:
		return ObdEntry{model.KeyRpm, conv(value/4, value)}
	case 0x0D:
		return ObdEntry{model.KeyObdSpeed, value}
	case 0x0F:
		return ObdEntry{"intakeTemp", conv(value-40, value)}
	case 0x11:
		return ObdEntry{model.KeyThrottle, conv(value*100/255, value)}
	case 0x21:
		return ObdEntry{"milDistance", value}
	case 0x2F:
		return ObdEntry{model.KeyFuel, conv(value*100/255, value)}
	case 0x31:
		return ObdEntry{"clearedDistance", value}
	}
	return ObdEntry{}
}
