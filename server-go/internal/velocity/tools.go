package velocity

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// EscapeTool is velocity-tools' EscapeTool ($esc).
type EscapeTool struct{}

func (EscapeTool) Property(string) (any, bool) { return nil, false }

func (EscapeTool) Method(name string, args []any) (any, bool, error) {
	if name == "html" && len(args) == 1 {
		if args[0] == nil {
			return nil, true, nil
		}
		return EscapeHTML(toString(args[0])), true, nil
	}
	return nil, false, nil
}

// EscapeHTML is commons-text StringEscapeUtils.escapeHtml4.
func EscapeHTML(s string) string {
	var b strings.Builder
	for _, r := range s {
		if e, ok := htmlEntities[r]; ok {
			b.WriteString(e)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// DateTool is velocity-tools' DateTool ($dateTool), unconfigured: the JVM's zone.
type DateTool struct {
	Zone *time.Location
	// Now is the clock of getDate (time.Now when nil).
	Now func() time.Time
}

func (DateTool) Property(string) (any, bool) { return nil, false }

func (t DateTool) Method(name string, args []any) (any, bool, error) {
	switch {
	case name == "getDate" && len(args) == 0:
		now := time.Now
		if t.Now != nil {
			now = t.Now
		}
		return NewDate(now()), true, nil
	case name == "format" && (len(args) == 2 || len(args) == 4):
		pattern, ok := args[0].(string)
		if !ok {
			return nil, true, nil
		}
		ms, ok := toDate(args[1])
		if !ok {
			return nil, true, nil
		}
		zone := t.Zone
		if len(args) == 4 {
			if z, ok := args[3].(TimeZone); ok && z.Location != nil {
				zone = z.Location
			}
		}
		if zone == nil {
			zone = time.Local
		}
		s, err := FormatDate(pattern, time.UnixMilli(ms).In(zone))
		if err != nil {
			return nil, true, nil // DateTool returns null for a bad format
		}
		return s, true, nil
	}
	return nil, false, nil
}

// toDate is DateTool.toDate: a Date, or a number of milliseconds.
func toDate(v any) (int64, bool) {
	switch x := v.(type) {
	case *Date:
		return x.Millis, true
	case int:
		return int64(x), true
	case int64:
		return x, true
	case float64:
		return int64(x), true
	}
	return 0, false
}

// FormatDate is SimpleDateFormat.format in English for the numeric fields (y M d H h
// k K m s S), am/pm (a), day and month names (E, MMM, MMMM) and quoted text.
func FormatDate(pattern string, t time.Time) (string, error) {
	var b strings.Builder
	for i := 0; i < len(pattern); {
		c := pattern[i]
		if c == '\'' {
			end := strings.IndexByte(pattern[i+1:], '\'')
			if end < 0 {
				return "", fmt.Errorf("unterminated quote")
			}
			if end == 0 {
				b.WriteByte('\'')
			} else {
				b.WriteString(pattern[i+1 : i+1+end])
			}
			i += end + 2
			continue
		}
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') {
			b.WriteByte(c)
			i++
			continue
		}
		j := i
		for j < len(pattern) && pattern[j] == c {
			j++
		}
		n := j - i
		i = j
		num := func(v int) {
			s := strconv.Itoa(v)
			for k := len(s); k < n; k++ {
				b.WriteByte('0')
			}
			b.WriteString(s)
		}
		switch c {
		case 'y':
			if n == 2 {
				num(t.Year() % 100)
			} else {
				num(t.Year())
			}
		case 'M':
			switch {
			case n >= 4:
				b.WriteString(t.Month().String())
			case n == 3:
				b.WriteString(t.Month().String()[:3])
			default:
				num(int(t.Month()))
			}
		case 'd':
			num(t.Day())
		case 'H':
			num(t.Hour())
		case 'k':
			h := t.Hour()
			if h == 0 {
				h = 24
			}
			num(h)
		case 'h':
			h := t.Hour() % 12
			if h == 0 {
				h = 12
			}
			num(h)
		case 'K':
			num(t.Hour() % 12)
		case 'm':
			num(t.Minute())
		case 's':
			num(t.Second())
		case 'S':
			num(t.Nanosecond() / 1e6)
		case 'a':
			if t.Hour() < 12 {
				b.WriteString("AM")
			} else {
				b.WriteString("PM")
			}
		case 'E':
			if n >= 4 {
				b.WriteString(t.Weekday().String())
			} else {
				b.WriteString(t.Weekday().String()[:3])
			}
		default:
			return "", fmt.Errorf("unsupported pattern letter %c", c)
		}
	}
	return b.String(), nil
}

// NumberTool is velocity-tools' NumberTool ($numberTool), in the en locale.
type NumberTool struct{}

func (NumberTool) Property(string) (any, bool) { return nil, false }

func (NumberTool) Method(name string, args []any) (any, bool, error) {
	if name == "format" && len(args) == 2 {
		pattern, ok := args[0].(string)
		if !ok || !isNumber(args[1]) {
			return nil, true, nil
		}
		s, err := FormatDecimal(pattern, args[1])
		if err != nil {
			return nil, true, nil
		}
		return s, true, nil
	}
	return nil, false, nil
}

// FormatDecimal is DecimalFormat(pattern, en symbols).format(number) for patterns of a
// prefix, integer digits (0 # and grouping commas), an optional fraction (0 and #) and a
// suffix, rounding HALF_EVEN on the exact value.
func FormatDecimal(pattern string, v any) (string, error) {
	if i := strings.IndexByte(pattern, ';'); i >= 0 {
		pattern = pattern[:i] // the negative subpattern isn't supported
	}
	start := strings.IndexAny(pattern, "0#,.")
	if start < 0 {
		return "", fmt.Errorf("no digits in %q", pattern)
	}
	end := start
	for end < len(pattern) && strings.IndexByte("0#,.", pattern[end]) >= 0 {
		end++
	}
	prefix, body, suffix := unquote(pattern[:start]), pattern[start:end], unquote(pattern[end:])
	intPart, fracPart, _ := strings.Cut(body, ".")
	minInt := strings.Count(intPart, "0")
	grouping := 0
	if k := strings.LastIndexByte(intPart, ','); k >= 0 {
		grouping = len(intPart) - k - 1
	}
	minFrac := strings.Count(fracPart, "0")
	maxFrac := len(fracPart)

	var neg bool
	var digits string
	switch x := v.(type) {
	case float64:
		if math.IsNaN(x) {
			return "NaN", nil
		}
		neg = math.Signbit(x)
		if math.IsInf(x, 0) {
			if neg {
				return "-" + prefix + "∞" + suffix, nil
			}
			return prefix + "∞" + suffix, nil
		}
		digits = strconv.FormatFloat(math.Abs(x), 'f', maxFrac, 64)
	default:
		n, _ := toLong(v)
		neg = n < 0
		if neg {
			digits = strconv.FormatUint(uint64(-n), 10)
		} else {
			digits = strconv.FormatInt(n, 10)
		}
	}
	ip, fp, _ := strings.Cut(digits, ".")
	fp = strings.TrimRight(fp, "0")
	for len(fp) < minFrac {
		fp += "0"
	}
	ip = strings.TrimLeft(ip, "0")
	for len(ip) < minInt {
		ip = "0" + ip
	}
	if grouping > 0 && len(ip) > grouping {
		var g strings.Builder
		first := len(ip) % grouping
		if first > 0 {
			g.WriteString(ip[:first])
		}
		for k := first; k < len(ip); k += grouping {
			if g.Len() > 0 {
				g.WriteByte(',')
			}
			g.WriteString(ip[k : k+grouping])
		}
		ip = g.String()
	}
	out := ip
	if fp != "" {
		out += "." + fp
	}
	if neg {
		return "-" + prefix + out + suffix, nil
	}
	return prefix + out + suffix, nil
}

// unquote drops DecimalFormat's quotes around literal text (” is a quote).
func unquote(s string) string {
	if !strings.Contains(s, "'") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\'' {
			if i+1 < len(s) && s[i+1] == '\'' {
				b.WriteByte('\'')
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
