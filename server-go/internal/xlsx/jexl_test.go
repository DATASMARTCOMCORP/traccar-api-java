package xlsx

import (
	"testing"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
)

func TestEval(t *testing.T) {
	var attrs model.Attributes
	attrs.Set("ignition", true)
	attrs.Set("name", `a "b"`)
	ctx := map[string]any{
		"speedUnit":     "kmh",
		"bracketsRegex": `[\{\}"]`,
		"p": Bean{"speed": 10.0, "valid": true, "address": nil, "latitude": -23.55, "longitude": -46.63,
			"fixTime": time.Date(2026, 12, 29, 1, 2, 3, 0, time.UTC), "attributes": Attributes{&attrs}, "duration": int64(3600000)},
		"names":    map[int64]any{int64(7): "Depot"},
		"dateTool": DateTool,
		"util":     Util,
		"zone":     time.UTC,
		"empty":    "",
	}
	for _, c := range []struct {
		expr string
		want any
	}{
		{`speedUnit.equals("kmh") ? "".format("%.1f km/h", p.speed * 1.852) : "".format("%.1f kn", p.speed)`, "18.5 km/h"},
		{`p.valid`, true},
		{`p.duration/86400000.0`, 3600000 / 86400000.0},
		{`p.duration > 0 ? p.duration : ""`, int64(3600000)},
		{`p.getAddress() == null ? "".format("%1$f°, %2$f°", p.latitude, p.longitude) : p.address`, "-23.550000°, -46.630000°"},
		{`p.attributes.toString().replaceAll(",", " ").replaceAll(bracketsRegex, "")`, `ignition=true  name=a b`},
		{`names[7]`, "Depot"},
		{`names[p.missing]`, nil},
		{`p.missing.deeper`, nil},
		{`empty ? 1 : 2`, 2},
		{`dateTool.format("YYYY-MM-dd HH:mm:ss", p.fixTime, locale, zone)`, "2027-12-29 01:02:03"},
		{`util.hyperlink("u", "t")`, Hyperlink{"u", "t"}},
	} {
		e, err := parseExpr(c.expr)
		if err != nil {
			t.Fatalf("%s: %v", c.expr, err)
		}
		got, err := eval(e, ctx)
		if err != nil {
			t.Fatalf("%s: %v", c.expr, err)
		}
		if got != c.want {
			t.Errorf("%s = %#v, want %#v", c.expr, got, c.want)
		}
	}
}
