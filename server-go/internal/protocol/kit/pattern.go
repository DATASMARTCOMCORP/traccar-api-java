package kit

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// PatternBuilder is Traccar's PatternBuilder: it assembles a regular expression from
// fragments. Compile translates the few Java-only constructs Traccar uses into RE2, whose
// leftmost-first matching picks the same groups as Java's backtracking engine.
type PatternBuilder struct {
	fragments []string
}

// NewPattern starts a pattern.
func NewPattern() *PatternBuilder { return &PatternBuilder{} }

var (
	trailingBar  = regexp.MustCompile(`\|$`)
	leadingBar   = regexp.MustCompile(`^\|`)
	textSpecial  = regexp.MustCompile(`([\\.\[{()*+?^$|])`)
	twoHexDigits = regexp.MustCompile(`([0-9A-Fa-f]{2})`)
	numberDot    = regexp.MustCompile(`([.])`)
	javaOctal    = regexp.MustCompile(`^\\0([0-7]{1,3})`)
)

func (p *PatternBuilder) Optional() *PatternBuilder { return p.OptionalN(1) }

// OptionalN wraps the last count fragments in an optional group.
func (p *PatternBuilder) OptionalN(count int) *PatternBuilder {
	i := len(p.fragments) - count
	p.fragments = append(p.fragments[:i], append([]string{"(?:"}, p.fragments[i:]...)...)
	p.fragments = append(p.fragments, ")?")
	return p
}

func (p *PatternBuilder) Expression(s string) *PatternBuilder {
	p.fragments = append(p.fragments, trailingBar.ReplaceAllString(s, `\|`))
	return p
}

func (p *PatternBuilder) Text(s string) *PatternBuilder {
	p.fragments = append(p.fragments, textSpecial.ReplaceAllString(s, `\$1`))
	return p
}

func (p *PatternBuilder) Number(s string) *PatternBuilder {
	s = strings.NewReplacer("dddd", "d{4}").Replace(s)
	s = strings.ReplaceAll(s, "ddd", "d{3}")
	s = strings.ReplaceAll(s, "dd", "d{2}")
	s = strings.ReplaceAll(s, "xxxx", "x{4}")
	s = strings.ReplaceAll(s, "xxx", "x{3}")
	s = strings.ReplaceAll(s, "xx", "x{2}")
	s = strings.ReplaceAll(s, "d", `\d`)
	s = strings.ReplaceAll(s, "x", "[0-9a-fA-F]")
	s = numberDot.ReplaceAllString(s, `\$1`)
	s = trailingBar.ReplaceAllString(s, `\|`)
	s = leadingBar.ReplaceAllString(s, `\|`)
	p.fragments = append(p.fragments, s)
	return p
}

func (p *PatternBuilder) Any() *PatternBuilder { p.fragments = append(p.fragments, ".*"); return p }

func (p *PatternBuilder) Binary(s string) *PatternBuilder {
	p.fragments = append(p.fragments, twoHexDigits.ReplaceAllString(s, `\$1`))
	return p
}

func (p *PatternBuilder) Or() *PatternBuilder         { p.fragments = append(p.fragments, "|"); return p }
func (p *PatternBuilder) GroupBegin() *PatternBuilder { return p.Expression("(?:") }
func (p *PatternBuilder) GroupEnd() *PatternBuilder   { return p.Expression(")") }
func (p *PatternBuilder) GroupEndWith(s string) *PatternBuilder {
	return p.Expression(")" + s)
}

func (p *PatternBuilder) String() string { return strings.Join(p.fragments, "") }

// Pattern is a compiled pattern (Pattern.compile(..., DOTALL)): unanchored for Find,
// anchored for Matches.
type Pattern struct {
	find, full *regexp.Regexp
	source     string
}

// Compile is PatternBuilder.compile.
func (p *PatternBuilder) Compile() *Pattern { return CompilePattern(p.String()) }

// CompilePattern compiles a Java regular expression (with DOTALL), panicking on syntax Go
// can't express: patterns are fixed in decoders, so that is a porting bug.
func CompilePattern(java string) *Pattern {
	re := translateRegex(java)
	return &Pattern{
		find:   regexp.MustCompile(`(?s)` + re),
		full:   regexp.MustCompile(`(?s)^(?:` + re + `)$`),
		source: java,
	}
}

// javaClasses are Java's POSIX classes and \s, \S, \h as RE2 class contents.
var javaClasses = map[string]string{
	`\p{XDigit}`: `0-9A-Fa-f`, `\p{Print}`: `\x20-\x7E`, `\p{Alpha}`: `A-Za-z`, `\p{Digit}`: `0-9`,
	`\p{Alnum}`: `A-Za-z0-9`, `\p{Upper}`: `A-Z`, `\p{Lower}`: `a-z`, `\p{Space}`: `\t\n\x0B\f\r `,
	`\s`: `\t\n\x0B\f\r `, `\h`: ` \t\xA0\x{1680}\x{180e}\x{2000}-\x{200a}\x{202f}\x{205f}\x{3000}`,
}

// translateRegex rewrites Java-only syntax into RE2: POSIX classes, \s (Java's includes
// \x0B), \S, \h, and octal escapes (\0n).
func translateRegex(java string) string {
	var b strings.Builder
	inClass := false
	for i := 0; i < len(java); i++ {
		c := java[i]
		if c == '\\' && i+1 < len(java) {
			rest := java[i:]
			matched := false
			for k, v := range javaClasses {
				if strings.HasPrefix(rest, k) {
					if inClass {
						b.WriteString(v)
					} else {
						b.WriteString("[" + v + "]")
					}
					i += len(k) - 1
					matched = true
					break
				}
			}
			if matched {
				continue
			}
			if strings.HasPrefix(rest, `\S`) {
				if inClass {
					Throw("regex: \\S inside a class is not supported: %s", java)
				}
				b.WriteString(`[^\t\n\x0B\f\r ]`)
				i++
				continue
			}
			if m := javaOctal.FindString(rest); m != "" && strings.HasPrefix(rest, m) {
				v, _ := strconv.ParseInt(m[2:], 8, 32)
				b.WriteString(`\x{` + strconv.FormatInt(v, 16) + `}`)
				i += len(m) - 1
				continue
			}
			b.WriteString(java[i : i+2])
			i++
			continue
		}
		switch {
		case c == '[' && !inClass:
			inClass = true
			b.WriteByte(c)
			// A ']' right after '[' or '[^' is a literal.
			if i+1 < len(java) && java[i+1] == '^' {
				b.WriteByte('^')
				i++
			}
			if i+1 < len(java) && java[i+1] == ']' {
				b.WriteString(`\]`)
				i++
			}
			continue
		case c == ']' && inClass:
			inClass = false
		}
		b.WriteByte(c)
	}
	return b.String()
}

// MatchString is pattern.matcher(s).matches().
func (p *Pattern) MatchString(s string) bool { return p.full.MatchString(s) }

// Parser is Traccar's Parser: it walks the groups of a match in order.
type Parser struct {
	p        *Pattern
	input    string
	groups   []int
	position int
}

// NewParser is new Parser(pattern, input).
func NewParser(p *Pattern, input string) *Parser { return &Parser{p: p, input: input} }

// Matches is matcher.matches(): the whole input must match.
func (p *Parser) Matches() bool {
	p.position = 1
	p.groups = p.p.full.FindStringSubmatchIndex(p.input)
	return p.groups != nil
}

// Find is matcher.find(): the next match after the previous one.
func (p *Parser) Find() bool {
	p.position = 1
	from := 0
	if p.groups != nil {
		from = p.groups[1]
		if p.groups[0] == p.groups[1] {
			from++
		}
	}
	if from > len(p.input) {
		p.groups = nil
		return false
	}
	loc := p.p.find.FindStringSubmatchIndex(p.input[from:])
	if loc == nil {
		p.groups = nil
		return false
	}
	for i := range loc {
		if loc[i] >= 0 {
			loc[i] += from
		}
	}
	p.groups = loc
	return true
}

// group returns group i and whether it took part in the match (Java: null when not).
func (p *Parser) group(i int) (string, bool) {
	if p.groups == nil || 2*i+1 >= len(p.groups) {
		Throw("No group %d", i)
	}
	a, b := p.groups[2*i], p.groups[2*i+1]
	if a < 0 {
		return "", false
	}
	return p.input[a:b], true
}

// Group is matcher.group(i) ("" for a group that didn't take part).
func (p *Parser) Group(i int) string { v, _ := p.group(i); return v }

// GroupCount is the number of groups in the pattern.
func (p *Parser) GroupCount() int { return p.p.find.NumSubexp() }

func (p *Parser) Skip(n int) { p.position += n }

func (p *Parser) HasNext() bool { return p.HasNextN(1) }

// HasNextN reports whether the next n groups all have values; if not, it skips them.
func (p *Parser) HasNextN(n int) bool {
	for i := p.position; i < p.position+n; i++ {
		if v, ok := p.group(i); !ok || v == "" {
			p.position += n
			return false
		}
	}
	return true
}

// HasNextAny reports whether any of the next n groups has a value; if none, it skips them.
func (p *Parser) HasNextAny(n int) bool {
	for i := p.position; i < p.position+n; i++ {
		if v, ok := p.group(i); ok && v != "" {
			return true
		}
	}
	p.position += n
	return false
}

// Next is next(): the group's text ("" where Java returns null; see NextNullable).
func (p *Parser) Next() string {
	v, _ := p.group(p.position)
	p.position++
	return v
}

// NextNullable is next() with Java's null as nil.
func (p *Parser) NextNullable() *string {
	v, ok := p.group(p.position)
	p.position++
	if !ok {
		return nil
	}
	return &v
}

// The number readers follow Java: nextInt() and the others return null (nil here) when
// the group didn't match, else parse it as Java does, failing like it. The Must forms
// are the values unboxed, where a missing group fails (NullPointerException), and the
// Default forms take the default instead.

func (p *Parser) nextNumber() (string, bool) {
	if p.HasNext() {
		return p.Next(), true
	}
	return "", false
}

func (p *Parser) NextInt() *int {
	if s, ok := p.nextNumber(); ok {
		v := Int(s)
		return &v
	}
	return nil
}

func (p *Parser) NextHexInt() *int {
	if s, ok := p.nextNumber(); ok {
		v := IntRadix(s, 16)
		return &v
	}
	return nil
}

func (p *Parser) NextBinInt() *int {
	if s, ok := p.nextNumber(); ok {
		v := IntRadix(s, 2)
		return &v
	}
	return nil
}

func (p *Parser) NextLong() *int64 {
	if s, ok := p.nextNumber(); ok {
		v := Long(s)
		return &v
	}
	return nil
}

func (p *Parser) NextHexLong() *int64 {
	if s, ok := p.nextNumber(); ok {
		v := LongRadix(s, 16)
		return &v
	}
	return nil
}

func (p *Parser) NextDouble() *float64 {
	if s, ok := p.nextNumber(); ok {
		v := Double(s)
		return &v
	}
	return nil
}

func must[T any](v *T) T {
	if v == nil {
		Throw("NullPointerException: the group didn't match")
	}
	return *v
}

func orDefault[T any](v *T, def T) T {
	if v == nil {
		return def
	}
	return *v
}

func (p *Parser) MustInt() int               { return must(p.NextInt()) }
func (p *Parser) MustHexInt() int            { return must(p.NextHexInt()) }
func (p *Parser) MustLong() int64            { return must(p.NextLong()) }
func (p *Parser) MustHexLong() int64         { return must(p.NextHexLong()) }
func (p *Parser) MustDouble() float64        { return must(p.NextDouble()) }
func (p *Parser) NextIntDefault(def int) int { return orDefault(p.NextInt(), def) }
func (p *Parser) NextHexIntDefault(def int) int {
	return orDefault(p.NextHexInt(), def)
}
func (p *Parser) NextBinIntDefault(def int) int { return orDefault(p.NextBinInt(), def) }
func (p *Parser) NextLongDefault(def int64) int64 {
	return orDefault(p.NextLong(), def)
}
func (p *Parser) NextDoubleDefault(def float64) float64 {
	return orDefault(p.NextDouble(), def)
}

// NextLongRadix is nextLong(radix, defaultValue).
func (p *Parser) NextLongRadix(radix int, def int64) int64 {
	if s, ok := p.nextNumber(); ok {
		return LongRadix(s, radix)
	}
	return def
}

// CoordinateFormat is Parser.CoordinateFormat.
type CoordinateFormat int

const (
	DegDeg CoordinateFormat = iota
	DegDegHem
	DegHem
	DegMinMin
	DegMinHem
	DegMinMinHem
	HemDegMinMin
	HemDeg
	HemDegMin
	HemDegMinHem
)

// concat is next() as Java concatenates it: an unmatched group is "null".
func (p *Parser) concat() string {
	if v := p.NextNullable(); v != nil {
		return *v
	}
	return "null"
}

// NextCoordinate is nextCoordinate(format).
func (p *Parser) NextCoordinate(format CoordinateFormat) float64 {
	var coordinate float64
	var hemisphere *string
	hem := func() { v := p.Next(); hemisphere = &v }
	switch format {
	case DegDeg:
		coordinate = Double(p.concat() + "." + p.concat())
	case DegDegHem:
		coordinate = Double(p.concat() + "." + p.concat())
		hem()
	case DegHem:
		coordinate = p.NextDoubleDefault(0)
		hem()
	case DegMinMin:
		coordinate = float64(p.NextIntDefault(0))
		coordinate += Double(p.concat()+"."+p.concat()) / 60
	case DegMinMinHem:
		coordinate = float64(p.NextIntDefault(0))
		coordinate += Double(p.concat()+"."+p.concat()) / 60
		hem()
	case HemDeg:
		hem()
		coordinate = p.NextDoubleDefault(0)
	case HemDegMin:
		hem()
		coordinate = float64(p.NextIntDefault(0))
		coordinate += p.NextDoubleDefault(0) / 60
	case HemDegMinHem:
		hem()
		coordinate = float64(p.NextIntDefault(0))
		coordinate += p.NextDoubleDefault(0) / 60
		if p.HasNext() {
			hem()
		}
	case HemDegMinMin:
		hem()
		coordinate = float64(p.NextIntDefault(0))
		coordinate += Double(p.concat()+"."+p.concat()) / 60
	default: // DegMinHem
		coordinate = float64(p.NextIntDefault(0))
		coordinate += p.NextDoubleDefault(0) / 60
		hem()
	}
	if hemisphere != nil && (*hemisphere == "S" || *hemisphere == "W" || *hemisphere == "-") {
		coordinate = -math.Abs(coordinate)
	}
	return coordinate
}

// DateTimeFormat is Parser.DateTimeFormat.
type DateTimeFormat int

const (
	HMS DateTimeFormat = iota
	SMH
	HMS_YMD
	HMS_DMY
	SMH_YMD
	SMH_DMY
	DMY_HMS
	DMY_HMSS
	YMD_HMS
	YMD_HMSS
)

// NextDateTime is nextDateTime(format, timeZone): zone nil is UTC; now is the current
// time for formats without a date.
func (p *Parser) NextDateTime(format DateTimeFormat, zone *time.Location, now time.Time) time.Time {
	var year, month, day, hour, minute, second, millis int
	n := func() int { return p.NextIntDefault(0) }
	switch format {
	case HMS:
		hour, minute, second = n(), n(), n()
	case SMH:
		second, minute, hour = n(), n(), n()
	case HMS_YMD:
		hour, minute, second = n(), n(), n()
		year, month, day = n(), n(), n()
	case HMS_DMY:
		hour, minute, second = n(), n(), n()
		day, month, year = n(), n(), n()
	case SMH_YMD:
		second, minute, hour = n(), n(), n()
		year, month, day = n(), n(), n()
	case SMH_DMY:
		second, minute, hour = n(), n(), n()
		day, month, year = n(), n(), n()
	case DMY_HMS, DMY_HMSS:
		day, month, year = n(), n(), n()
		hour, minute, second = n(), n(), n()
	default:
		year, month, day = n(), n(), n()
		hour, minute, second = n(), n(), n()
	}
	if format == YMD_HMSS || format == DMY_HMSS {
		millis = n()
	}
	if year >= 0 && year < 100 {
		year += 2000
	}
	var b *DateBuilder
	if format != HMS && format != SMH {
		b = NewDateBuilder(zone)
		b.SetDate(year, month, day)
	} else {
		b = NewDateBuilderAt(now, zone)
	}
	b.SetTimeMillis(hour, minute, second, millis)
	return b.Date()
}

func (p *Parser) NextDateTimeDefault() time.Time {
	return p.NextDateTime(YMD_HMS, nil, time.Time{})
}
