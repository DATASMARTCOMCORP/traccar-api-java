package kit

import (
	"bufio"
	"compress/gzip"
	"encoding/hex"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"
)

// testdata/helper-vectors.tsv.gz holds the results of Traccar's Checksum, DataConverter,
// BitBuffer, ObdDecoder and BcdUtil for random inputs.
func TestHelpersJavaVectors(t *testing.T) {
	f, err := os.Open("testdata/helper-vectors.tsv.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	algorithms := map[string]*CRC{
		"CRC8_EGTS": CRC8_EGTS, "CRC8_ROHC": CRC8_ROHC, "CRC8_DALLAS": CRC8_DALLAS,
		"CRC16_IBM": CRC16_IBM, "CRC16_X25": CRC16_X25, "CRC16_MODBUS": CRC16_MODBUS,
		"CRC16_CCITT_FALSE": CRC16_CCITT_FALSE, "CRC16_KERMIT": CRC16_KERMIT, "CRC16_XMODEM": CRC16_XMODEM,
		"CRC32_STANDARD": CRC32_STANDARD, "CRC32_MPEG2": CRC32_MPEG2,
	}
	sc := bufio.NewScanner(zr)
	n := 0
	for sc.Scan() {
		parts := strings.SplitN(sc.Text(), "\t", 3)
		kind, in, want := parts[0], parts[1], parts[2]
		data, _ := hex.DecodeString(in)
		var got string
		switch {
		case strings.HasPrefix(kind, "CRC8"):
			got = strconv.Itoa(CRC8(algorithms[kind], data))
		case strings.HasPrefix(kind, "CRC16"):
			got = strconv.Itoa(CRC16(algorithms[kind], data))
		case strings.HasPrefix(kind, "CRC32"):
			got = strconv.Itoa(int(CRC32(algorithms[kind], data)))
		case kind == "xor":
			got = strconv.Itoa(XOR(data))
		case kind == "sum":
			got = strconv.Itoa(Sum(data))
		case kind == "modulo256":
			got = strconv.Itoa(Modulo256(data))
		case kind == "ip":
			got = strconv.Itoa(IP(data))
		case kind == "xorString":
			got = strconv.Itoa(XORString(string(data)))
		case kind == "nmea":
			got = NMEA(string(data))
		case kind == "sumString":
			got = SumString(string(data))
		case kind == "luhn":
			v, _ := strconv.ParseInt(in, 10, 64)
			got = strconv.FormatInt(Luhn(v), 10)
		case kind == "base64":
			got = hex.EncodeToString(ParseBase64(string(data)))
		case kind == "bits":
			got = readBits(data, want)
		case kind == "obdCode":
			v, _ := strconv.Atoi(in)
			got = ObdDecodeCode(v)
		case kind == "bcdCoord":
			got = strconv.FormatUint(math.Float64bits(ReadBCDCoordinate(NewBuf(data))), 16)
		case strings.HasPrefix(kind, "bcd"):
			digits, _ := strconv.Atoi(kind[3:])
			got = strconv.Itoa(ReadBCD(NewBuf(data), digits))
		default:
			t.Fatalf("unknown kind %s", kind)
		}
		n++
		if got != want {
			t.Errorf("%s(%s) = %s, Java %s", kind, in, got, want)
		}
	}
	if n < 9000 {
		t.Fatalf("only %d vectors", n)
	}
}

// readBits replays the reads Java made ("s5=-3 u7=100 ...").
func readBits(data []byte, javaReads string) string {
	b := NewBitBuffer(NewBuf(data))
	var out []string
	var err error
	func() {
		defer Recover(&err)
		for _, op := range strings.Fields(javaReads) {
			if op == "error" {
				Throw("error")
			}
			spec, _, _ := strings.Cut(op, "=")
			length, _ := strconv.Atoi(spec[1:])
			if spec[0] == 's' {
				out = append(out, fmt.Sprintf("s%d=%d", length, b.ReadSigned(length)))
			} else {
				out = append(out, fmt.Sprintf("u%d=%d", length, b.ReadUnsigned(length)))
			}
		}
	}()
	if err != nil {
		out = append(out, "error")
	}
	return strings.Join(out, " ")
}
