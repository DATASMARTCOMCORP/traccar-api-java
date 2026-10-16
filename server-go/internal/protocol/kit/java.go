package kit

import (
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/javatime"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
)

// Java parsing that throws: Integer.parseInt and friends fail the decoder with a Fault.

// Int is Integer.parseInt.
func Int(s string) int {
	v, err := model.ParseInt(s)
	if err != nil {
		throwNumber(s)
	}
	return int(v)
}

// IntRadix is Integer.parseInt(s, radix).
func IntRadix(s string, radix int) int {
	v, err := model.ParseIntRadix(s, radix)
	if err != nil {
		throwNumber(s)
	}
	return int(v)
}

// Long is Long.parseLong.
func Long(s string) int64 {
	v, err := model.ParseLong(s)
	if err != nil {
		throwNumber(s)
	}
	return v
}

// LongRadix is Long.parseLong(s, radix).
func LongRadix(s string, radix int) int64 {
	v, err := model.ParseLongRadix(s, radix)
	if err != nil {
		throwNumber(s)
	}
	return v
}

// Double is Double.parseDouble.
func Double(s string) float64 {
	v, err := model.ParseDouble(s)
	if err != nil {
		throwNumber(s)
	}
	return v
}

func throwNumber(s string) {
	Throw("NumberFormatException: For input string: %q", s)
}

// Split is String.split(sep) for a literal separator: trailing empty strings are dropped
// (so ";;" gives none), and a string without the separator is itself.
func Split(s, sep string) []string {
	parts := strings.Split(s, sep)
	if len(parts) == 1 {
		return parts
	}
	n := len(parts)
	for n > 0 && parts[n-1] == "" {
		n--
	}
	return parts[:n]
}

// SplitRegex is String.split(regex) (limit 0) for a Java regex.
func SplitRegex(s, regex string) []string {
	return SplitRegexN(s, regex, 0)
}

var splitPatterns sync.Map // Java regex -> *regexp.Regexp

// SplitRegexN is String.split(regex, limit).
func SplitRegexN(s, regex string, limit int) []string {
	var re *regexp.Regexp
	if v, ok := splitPatterns.Load(regex); ok {
		re = v.(*regexp.Regexp)
	} else {
		re = regexp.MustCompile(translateRegex(regex))
		splitPatterns.Store(regex, re)
	}
	if s == "" {
		return []string{""}
	}
	var parts []string
	index := 0
	for _, m := range re.FindAllStringIndex(s, -1) {
		if limit > 0 && len(parts) == limit-1 {
			break
		}
		if m[1] == 0 { // a zero-width match at the start yields no leading empty string
			continue
		}
		parts = append(parts, s[index:m[0]])
		index = m[1]
	}
	if index == 0 && len(parts) == 0 {
		return []string{s}
	}
	parts = append(parts, s[index:])
	if limit == 0 {
		n := len(parts)
		for n > 0 && parts[n-1] == "" {
			n--
		}
		parts = parts[:n]
	}
	return parts
}

// DateFormat is a java.time DateTimeFormatter.ofPattern(pattern).withZone(zone) for
// parsing, with the SMART resolver: fields of fixed widths (yyyy, yy as 20yy, MM, dd, HH,
// mm, ss, SSS), quoted and punctuation literals.
type DateFormat struct {
	pattern string
	zone    *time.Location
	fields  []dateField
}

type dateField struct {
	letter  byte // 0 for a literal
	width   int
	literal string
}

// NewDateFormat compiles a pattern; zone nil is UTC.
func NewDateFormat(pattern string, zone *time.Location) *DateFormat {
	f := &DateFormat{pattern: pattern, zone: zone}
	if f.zone == nil {
		f.zone = time.UTC
	}
	for i := 0; i < len(pattern); {
		c := pattern[i]
		switch {
		case c == '\'':
			end := strings.IndexByte(pattern[i+1:], '\'')
			if end < 0 {
				panic("unterminated quote in " + pattern)
			}
			f.fields = append(f.fields, dateField{literal: pattern[i+1 : i+1+end]})
			i += end + 2
		case strings.IndexByte("yMdHmsS", c) >= 0:
			j := i
			for j < len(pattern) && pattern[j] == c {
				j++
			}
			f.fields = append(f.fields, dateField{letter: c, width: j - i})
			i = j
		case c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z':
			panic("unsupported pattern letter in " + pattern)
		default:
			f.fields = append(f.fields, dateField{literal: string(c)})
			i++
		}
	}
	return f
}

// Parse is DateUtil.parse(formatter, s): a failure throws (DateTimeParseException).
func (f *DateFormat) Parse(s string) time.Time {
	t, ok := f.parse(s)
	if !ok {
		Throw("DateTimeParseException: Text '%s' could not be parsed", s)
	}
	return t
}

func (f *DateFormat) parse(s string) (time.Time, bool) {
	year, month, day, hour, minute, second, nanos := -1, -1, -1, 0, 0, 0, 0
	pos := 0
	for _, fd := range f.fields {
		if fd.letter == 0 {
			if !strings.HasPrefix(s[pos:], fd.literal) {
				return time.Time{}, false
			}
			pos += len(fd.literal)
			continue
		}
		if pos+fd.width > len(s) {
			return time.Time{}, false
		}
		v := 0
		for _, c := range []byte(s[pos : pos+fd.width]) {
			if c < '0' || c > '9' {
				return time.Time{}, false
			}
			v = v*10 + int(c-'0')
		}
		pos += fd.width
		switch fd.letter {
		case 'y':
			if fd.width == 2 {
				v += 2000
			}
			year = v
		case 'M':
			month = v
		case 'd':
			day = v
		case 'H':
			hour = v
		case 'm':
			minute = v
		case 's':
			second = v
		case 'S':
			for w := fd.width; w < 9; w++ {
				v *= 10
			}
			nanos = v
		}
	}
	if pos != len(s) || year < 0 || month < 0 || day < 0 {
		return time.Time{}, false
	}
	if month < 1 || month > 12 || day < 1 || day > 31 || minute > 59 || second > 59 {
		return time.Time{}, false
	}
	if last := daysIn(year, month); day > last {
		day = last // SMART: the 31st of a 30-day month is its last day
	}
	extraDay := 0
	if hour == 24 && minute == 0 && second == 0 && nanos == 0 {
		hour, extraDay = 0, 1
	} else if hour > 23 {
		return time.Time{}, false
	}
	w := time.Date(year, time.Month(month), day+extraDay, hour, minute, second, nanos, time.UTC)
	if f.zone == time.UTC {
		return w, true
	}
	return javatime.AtZone(w.Year(), w.Month(), w.Day(), hour, minute, second, nanos, f.zone), true
}

func daysIn(year, month int) int {
	return time.Date(year, time.Month(month)+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

// Java strings index UTF-16 chars, Go strings bytes. For ASCII text (nearly all protocol
// text) they agree; these helpers keep Java's indexes when the text isn't ASCII (bytes
// that US_ASCII decoding turned into U+FFFD, or UTF-8 text).

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// Len is String.length(): UTF-16 chars.
func Len(s string) int {
	if isASCII(s) {
		return len(s)
	}
	return len(utf16.Encode([]rune(s)))
}

// Substring is String.substring(begin, end) in UTF-16 chars; out of range panics
// (StringIndexOutOfBoundsException).
func Substring(s string, begin, end int) string {
	if isASCII(s) {
		if begin < 0 || end > len(s) || begin > end {
			Throw("StringIndexOutOfBoundsException: begin %d, end %d, length %d", begin, end, len(s))
		}
		return s[begin:end]
	}
	u := utf16.Encode([]rune(s))
	if begin < 0 || end > len(u) || begin > end {
		Throw("StringIndexOutOfBoundsException: begin %d, end %d, length %d", begin, end, len(u))
	}
	return string(utf16.Decode(u[begin:end]))
}

// SubstringFrom is String.substring(begin).
func SubstringFrom(s string, begin int) string { return Substring(s, begin, Len(s)) }

// CharAt is String.charAt(i): a UTF-16 char; out of range panics.
func CharAt(s string, i int) rune {
	if isASCII(s) {
		if i < 0 || i >= len(s) {
			Throw("StringIndexOutOfBoundsException: index %d, length %d", i, len(s))
		}
		return rune(s[i])
	}
	u := utf16.Encode([]rune(s))
	if i < 0 || i >= len(u) {
		Throw("StringIndexOutOfBoundsException: index %d, length %d", i, len(u))
	}
	return rune(u[i])
}

// IndexOf is String.indexOf(sub) in UTF-16 chars.
func IndexOf(s, sub string) int {
	i := strings.Index(s, sub)
	if i <= 0 || isASCII(s[:i]) {
		return i
	}
	return Len(s[:i])
}

// Trim is String.trim: chars up to ' ' are removed from both ends.
func Trim(s string) string {
	return strings.TrimFunc(s, func(r rune) bool { return r <= ' ' })
}

// IndexOfFrom is String.indexOf(sub, from) in UTF-16 chars (from below 0 is 0).
func IndexOfFrom(s, sub string, from int) int {
	if from < 0 {
		from = 0
	}
	if isASCII(s) {
		if from > len(s) {
			return -1
		}
		i := strings.Index(s[from:], sub)
		if i < 0 {
			return -1
		}
		return from + i
	}
	u := utf16.Encode([]rune(s))
	if from > len(u) {
		return -1
	}
	rest := string(utf16.Decode(u[from:]))
	i := IndexOf(rest, sub)
	if i < 0 {
		return -1
	}
	return from + i
}

// ParseBase64 is DataConverter.parseBase64 (commons-codec Base64.decodeBase64): standard
// and URL-safe alphabets, padding optional, anything else skipped; trailing bits that
// don't fill a byte are dropped.
func ParseBase64(s string) []byte {
	var out []byte
	acc, bits := 0, 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		var v int
		switch {
		case c >= 'A' && c <= 'Z':
			v = int(c - 'A')
		case c >= 'a' && c <= 'z':
			v = int(c-'a') + 26
		case c >= '0' && c <= '9':
			v = int(c-'0') + 52
		case c == '+' || c == '-':
			v = 62
		case c == '/' || c == '_':
			v = 63
		case c == '=':
			i = len(s) // padding ends the data
			continue
		default:
			continue
		}
		acc = acc<<6 | v
		bits += 6
		if bits >= 8 {
			bits -= 8
			out = append(out, byte(acc>>bits))
			acc &= 1<<bits - 1
		}
	}
	if out == nil {
		out = []byte{}
	}
	return out
}
