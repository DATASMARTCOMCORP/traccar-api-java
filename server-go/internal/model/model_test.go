package model

import (
	"encoding/json"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The expected values in this file were produced by the Java 6.14.5 server (JDK 21)
// during Step 1: PUT /api/devices with each input, then reading the response and the
// stored tc_devices.attributes text.

func TestTimeUnmarshalJackson(t *testing.T) {
	tests := []struct{ in, want string }{
		{`"2030-10-14T19:11:35.225+00:00"`, "2030-10-14T19:11:35.225+00:00"},
		{`"2030-10-14T19:11:35Z"`, "2030-10-14T19:11:35.000+00:00"},
		{`"2030-10-14T19:11:35.225Z"`, "2030-10-14T19:11:35.225+00:00"},
		{`"2030-10-14T19:11:35"`, "2030-10-14T19:11:35.000+00:00"},
		{`"2030-10-14"`, "2030-10-14T00:00:00.000+00:00"},
		{`1900000000000`, "2030-03-17T17:46:40.000+00:00"},
		{`"1900000000000"`, "2030-03-17T17:46:40.000+00:00"},
		{`"2030-10-14T19:11:35.1+0300"`, "2030-10-14T16:11:35.100+00:00"},
		{`"2030-10-14T19:11:35.123456Z"`, "2030-10-14T19:11:35.123+00:00"},
		{`"Mon, 14 Oct 2030 19:11:35 GMT"`, "2030-10-14T19:11:35.000+00:00"},
		{`"2030-10-14T19:11Z"`, "2030-10-14T19:11:00.000+00:00"},
		{`"2030-10-14T19:11:35+03"`, "2030-10-14T16:11:35.000+00:00"},
		{`"2030-10-14T19:11:35.225-03:00"`, "2030-10-14T22:11:35.225+00:00"},
		{`"2030-10-14T19:11:35.2251Z"`, "2030-10-14T19:11:35.225+00:00"},
		{`""`, "null"},
		{`null`, "null"},
	}
	for _, tt := range tests {
		var tm Time
		if err := json.Unmarshal([]byte(tt.in), &tm); err != nil {
			t.Errorf("%s: %v", tt.in, err)
			continue
		}
		got, _ := tm.MarshalJSON()
		want := tt.want
		if want != "null" {
			want = `"` + want + `"`
		}
		if string(got) != want {
			t.Errorf("%s: got %s, want %s", tt.in, got, want)
		}
	}
	// Java answers 400 for these.
	for _, in := range []string{`"2030-10-14 19:11:35"`, `"garbage"`, `1.9e12`, `"2030-10-14T19"`, `true`} {
		var tm Time
		if err := json.Unmarshal([]byte(in), &tm); err == nil {
			t.Errorf("%s: want error, got %v", in, tm.Time)
		}
	}
}

func TestNewTimeTruncatesToMillis(t *testing.T) {
	tm := NewTime(time.Date(2026, 10, 14, 19, 11, 35, 225_999_999, time.UTC))
	if got, _ := tm.MarshalJSON(); string(got) != `"2026-10-14T19:11:35.225+00:00"` {
		t.Errorf("got %s", got)
	}
	if !NewTime(time.Time{}).IsZero() {
		t.Error("zero time must stay null")
	}
}

func TestJavaDouble(t *testing.T) {
	tests := []struct {
		f    float64
		want string
	}{
		{87, "87.0"}, {0, "0.0"}, {math.Copysign(0, -1), "-0.0"}, {1.5, "1.5"}, {100, "100.0"},
		{1e-5, "1.0E-5"}, {12345678.9, "1.23456789E7"}, {1.5e300, "1.5E300"},
		{0.001, "0.001"}, {9999999.0, "9999999.0"}, {1e7, "1.0E7"}, {-23.5505, "-23.5505"},
		{436.87233892373064, "436.87233892373064"}, {1.23456789e7, "1.23456789E7"},
		{2147483648.0, "2.147483648E9"},
		{math.SmallestNonzeroFloat64, "4.9E-324"}, // Java's documented MIN_VALUE text
		{math.MaxFloat64, "1.7976931348623157E308"},
		{1e23, "1.0E23"}, {2e-3, "0.002"}, {0.00099, "9.9E-4"},
		{float64(float32(0.1)), "0.10000000149011612"}, // Float widened, as decoders store it
	}
	for _, tt := range tests {
		if got := javaDouble(tt.f); got != tt.want {
			t.Errorf("javaDouble(%v) = %s, want %s", tt.f, got, tt.want)
		}
	}
	if got := string(appendDouble(nil, math.Inf(1))); got != `"Infinity"` {
		t.Errorf("+Inf = %s", got)
	}
}

// TestJavaDoubleGolden compares against Double.toString output from a real JDK for
// 20,000 values (testdata/Doubles.java): random bit patterns, decoder-style decimals,
// widened floats and edge cases.
func TestJavaDoubleGolden(t *testing.T) {
	data, err := os.ReadFile("testdata/java_doubles.txt")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for line := range strings.Lines(string(data)) {
		if strings.HasPrefix(line, "#") {
			continue
		}
		bits, want, _ := strings.Cut(strings.TrimSpace(line), " ")
		u, err := strconv.ParseUint(bits, 16, 64)
		if err != nil {
			t.Fatal(err)
		}
		f := math.Float64frombits(u)
		if got := javaDouble(f); got != want {
			t.Errorf("javaDouble(%v) = %s, Java: %s", f, got, want)
		}
		n++
	}
	if n < 20000 {
		t.Errorf("only %d golden values", n)
	}
}

func TestAttributesRoundTripJava(t *testing.T) {
	tests := []struct{ in, stored string }{
		// Request attributes → text Java stored in tc_devices.attributes.
		{`{"n1":1e2,"n2":1.50,"n3":-0,"n4":0.00001,"n5":12345678.9,"n7":99999999999999999999,"n8":-0.0,"n9":2147483648,"n10":0.001,"n11":1e-3,"n12":9999999.0,"n13":10000000.0,"n14":1E+2,"arr":[1,2.0,"x",{"k":3.0}],"obj":{"z":1,"a":2.5},"s":"çé😀</x>","b":false}`,
			`{"n1":100.0,"n2":1.5,"n3":0,"n4":1.0E-5,"n5":1.23456789E7,"n7":99999999999999999999,"n8":-0.0,"n9":2147483648,"n10":0.001,"n11":0.001,"n12":9999999.0,"n13":1.0E7,"n14":100.0,"arr":[1,2.0,"x",{"k":3.0}],"obj":{"z":1,"a":2.5},"s":"çé😀</x>","b":false}`},
		{`{"a":87.0,"b":87,"c":"x","d":1.5e300,"e":true,"f":null,"g":12345678901234}`,
			`{"a":87.0,"b":87,"c":"x","d":1.5E300,"e":true,"f":null,"g":12345678901234}`},
		{`{"inf":1e400}`, `{"inf":"Infinity"}`},
		// Escapes: uppercase hex, short forms, DEL and U+2028 raw; duplicate key keeps its
		// first position and its last value.
		{`{"s":"a\u0001b\u001fc\bd\fe\nf\rg\th\u007fi j/k\\l\"m&n<o>p","dup":1,"x":2,"dup":3}`,
			"{\"s\":\"a\\u0001b\\u001Fc\\bd\\fe\\nf\\rg\\th\x7fi j/k\\\\l\\\"m&n<o>p\",\"dup\":3,\"x\":2}"},
		{`{}`, `{}`},
		{`null`, `{}`},
		// Stored by the osmand decoder in Step 1.
		{`{"batteryLevel":87.0,"hdop":1.5,"distance":0.0,"totalDistance":0.0,"motion":false}`,
			`{"batteryLevel":87.0,"hdop":1.5,"distance":0.0,"totalDistance":0.0,"motion":false}`},
	}
	for _, tt := range tests {
		a, err := ParseAttributes(tt.in)
		if err != nil {
			t.Errorf("%s: %v", tt.in, err)
			continue
		}
		if got := a.String(); got != tt.stored {
			t.Errorf("round trip:\n got %s\nwant %s", got, tt.stored)
		}
		// Reading the stored text must reproduce it exactly.
		b, _ := ParseAttributes(tt.stored)
		if got := b.String(); got != tt.stored {
			t.Errorf("stored text not stable:\n got %s\nwant %s", got, tt.stored)
		}
	}
	for _, bad := range []string{`[1]`, `"x"`, `{"a":}`, `{} {}`} {
		if _, err := ParseAttributes(bad); err == nil {
			t.Errorf("%s: want error", bad)
		}
	}
}

func TestAttributesSetGetDelete(t *testing.T) {
	var a Attributes
	a.Set("speed", 10)
	a.Set("ratio", float32(0.1))
	a.Set("name", "x")
	a.Set("speed", 87.0) // keeps first position
	a.Delete("name")
	a.Set("name", "y") // re-added at the end
	if got := a.String(); got != `{"speed":87.0,"ratio":0.10000000149011612,"name":"y"}` {
		t.Errorf("got %s", got)
	}
	if v, ok := a.Get("ratio"); !ok || v.(float64) != float64(float32(0.1)) {
		t.Errorf("Get(ratio) = %v, %v", v, ok)
	}
	var keys []string
	for k := range a.All() {
		keys = append(keys, k)
	}
	if strings.Join(keys, ",") != "speed,ratio,name" || a.Len() != 3 {
		t.Errorf("keys %v", keys)
	}
	var nilAttrs *Attributes
	if nilAttrs.String() != "{}" || nilAttrs.Len() != 0 {
		t.Error("nil attributes should behave as empty")
	}
	var bad Attributes
	bad.Set("x", struct{}{})
	if _, err := bad.MarshalJSON(); err == nil {
		t.Error("unsupported value types must be an error, not guessed")
	}
}

func TestAppendStringInvalidUTF8(t *testing.T) {
	if got := string(appendString(nil, "a\xffb")); got != "\"a�b\"" {
		t.Errorf("got %q", got)
	}
}

func TestNetworkRoundTrip(t *testing.T) {
	// Shape of a network stored by the Java suntech decoder in Step 1 (radioType and
	// considerIp from the constructor defaults, null fields omitted).
	stored := `{"radioType":"gsm","considerIp":false,"cellTowers":[{"cellId":30062,"locationAreaCode":2,"mobileCountryCode":724,"mobileNetworkCode":5221,"signalStrength":-65}]}`
	var n Network
	if err := json.Unmarshal([]byte(stored), &n); err != nil {
		t.Fatal(err)
	}
	if got, _ := n.MarshalJSON(); string(got) != stored {
		t.Errorf("got  %s\nwant %s", got, stored)
	}

	// Absent keys take Java's defaults; positive signal strengths are negated.
	var m Network
	if err := json.Unmarshal([]byte(`{"cellTowers":[{"cellId":1,"signalStrength":70}],"wifiAccessPoints":[{"macAddress":"aa:bb","channel":6}]}`), &m); err != nil {
		t.Fatal(err)
	}
	want := `{"radioType":"gsm","considerIp":false,"cellTowers":[{"cellId":1,"signalStrength":-70}],"wifiAccessPoints":[{"macAddress":"aa:bb","channel":6}]}`
	if got, _ := m.MarshalJSON(); string(got) != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}

	// Explicit nulls stay null (and are omitted).
	var x Network
	if err := json.Unmarshal([]byte(`{"radioType":null,"considerIp":null}`), &x); err != nil {
		t.Fatal(err)
	}
	if got, _ := x.MarshalJSON(); string(got) != `{}` {
		t.Errorf("got %s", got)
	}
}
