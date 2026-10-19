package xlsx

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/velocity"
)

// Expressions are the JEXL 3 that JXLS evaluates in ${...}: literals, variables, property
// and index access, method calls, arithmetic, comparison, logic and the ternary operator,
// with JEXL's lenient rules (a property of null is null; "" and "false" are false).
//
// Values are Java values: nil, string, bool, int (Integer), int64 (Long), float64
// (Double), time.Time (Date), Bean, Attributes, map[int64]any (a Map with Long keys),
// []any, *time.Location (TimeZone), Hyperlink, and the tools.

// Bean is a Java object: its getter values by property name.
type Bean map[string]any

// Attributes is an ExtendedModel's attribute map (its toString is Java's Map.toString).
type Attributes struct{ A *model.Attributes }

// Hyperlink is Util.hyperlink's WritableHyperlink: a cell with text and a link.
type Hyperlink struct{ URL, Text string }

type dateTool struct{}
type utilTool struct{}

// Tools are the JXLS context's tools: dateTool (velocity-tools' DateTool) and util.
var (
	DateTool any = dateTool{}
	Util     any = utilTool{}
)

type expr interface{}

type lit struct{ v any }
type ident struct{ name string }
type prop struct {
	target expr
	name   string
}
type call struct {
	target expr
	name   string
	args   []expr
}
type index struct{ target, key expr }
type binop struct {
	op   string
	l, r expr
}
type unop struct {
	op string
	e  expr
}
type ternary struct{ cond, a, b expr }

type parser struct {
	s   string
	pos int
}

// parseExpr parses one JEXL expression.
func parseExpr(s string) (expr, error) {
	p := &parser{s: s}
	e, err := p.ternary()
	if err != nil {
		return nil, err
	}
	p.space()
	if p.pos != len(p.s) {
		return nil, fmt.Errorf("jexl: unexpected %q in %q", p.s[p.pos:], s)
	}
	return e, nil
}

func (p *parser) space() {
	for p.pos < len(p.s) && strings.IndexByte(" \t\r\n", p.s[p.pos]) >= 0 {
		p.pos++
	}
}

func (p *parser) eat(tok string) bool {
	p.space()
	if strings.HasPrefix(p.s[p.pos:], tok) {
		p.pos += len(tok)
		return true
	}
	return false
}

func (p *parser) ternary() (expr, error) {
	c, err := p.binary(0)
	if err != nil {
		return nil, err
	}
	if p.eat("?") {
		a, err := p.ternary()
		if err != nil {
			return nil, err
		}
		if !p.eat(":") {
			return nil, fmt.Errorf("jexl: expected : in %q", p.s)
		}
		b, err := p.ternary()
		if err != nil {
			return nil, err
		}
		return &ternary{c, a, b}, nil
	}
	return c, nil
}

var levels = [][]string{{"||"}, {"&&"}, {"==", "!="}, {"<=", ">=", "<", ">"}, {"+", "-"}, {"*", "/", "%"}}

func (p *parser) binary(level int) (expr, error) {
	if level == len(levels) {
		return p.unary()
	}
	l, err := p.binary(level + 1)
	if err != nil {
		return nil, err
	}
	for {
		p.space()
		op := ""
		for _, o := range levels[level] {
			if strings.HasPrefix(p.s[p.pos:], o) {
				op = o
				break
			}
		}
		if op == "" {
			return l, nil
		}
		p.pos += len(op)
		r, err := p.binary(level + 1)
		if err != nil {
			return nil, err
		}
		l = &binop{op, l, r}
	}
}

func (p *parser) unary() (expr, error) {
	p.space()
	if p.pos < len(p.s) && (p.s[p.pos] == '!' || p.s[p.pos] == '-') && !strings.HasPrefix(p.s[p.pos:], "!=") {
		op := p.s[p.pos : p.pos+1]
		p.pos++
		e, err := p.unary()
		if err != nil {
			return nil, err
		}
		return &unop{op, e}, nil
	}
	return p.postfix()
}

func isIdent(c byte, first bool) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || !first && c >= '0' && c <= '9'
}

func (p *parser) name() string {
	start := p.pos
	for p.pos < len(p.s) && isIdent(p.s[p.pos], p.pos == start) {
		p.pos++
	}
	return p.s[start:p.pos]
}

func (p *parser) postfix() (expr, error) {
	e, err := p.primary()
	if err != nil {
		return nil, err
	}
	for {
		switch {
		case p.eat("."):
			p.space()
			n := p.name()
			if n == "" {
				return nil, fmt.Errorf("jexl: expected a name in %q", p.s)
			}
			if p.eat("(") {
				args, err := p.args()
				if err != nil {
					return nil, err
				}
				e = &call{e, n, args}
			} else {
				e = &prop{e, n}
			}
		case p.eat("["):
			k, err := p.ternary()
			if err != nil {
				return nil, err
			}
			if !p.eat("]") {
				return nil, fmt.Errorf("jexl: expected ] in %q", p.s)
			}
			e = &index{e, k}
		default:
			return e, nil
		}
	}
}

func (p *parser) args() ([]expr, error) {
	var out []expr
	if p.eat(")") {
		return out, nil
	}
	for {
		a, err := p.ternary()
		if err != nil {
			return nil, err
		}
		out = append(out, a)
		if p.eat(")") {
			return out, nil
		}
		if !p.eat(",") {
			return nil, fmt.Errorf("jexl: expected , or ) in %q", p.s)
		}
	}
}

func (p *parser) primary() (expr, error) {
	p.space()
	if p.pos >= len(p.s) {
		return nil, fmt.Errorf("jexl: unexpected end of %q", p.s)
	}
	c := p.s[p.pos]
	switch {
	case c == '(':
		p.pos++
		e, err := p.ternary()
		if err != nil {
			return nil, err
		}
		if !p.eat(")") {
			return nil, fmt.Errorf("jexl: expected ) in %q", p.s)
		}
		return e, nil
	case c == '"' || c == '\'':
		p.pos++
		var b strings.Builder
		for p.pos < len(p.s) && p.s[p.pos] != c {
			if p.s[p.pos] == '\\' && p.pos+1 < len(p.s) {
				p.pos++
				switch p.s[p.pos] {
				case 'n':
					b.WriteByte('\n')
				case 't':
					b.WriteByte('\t')
				default:
					b.WriteByte(p.s[p.pos])
				}
				p.pos++
				continue
			}
			b.WriteByte(p.s[p.pos])
			p.pos++
		}
		if p.pos >= len(p.s) {
			return nil, fmt.Errorf("jexl: unterminated string in %q", p.s)
		}
		p.pos++
		return &lit{b.String()}, nil
	case c >= '0' && c <= '9':
		start := p.pos
		for p.pos < len(p.s) && (p.s[p.pos] >= '0' && p.s[p.pos] <= '9' || p.s[p.pos] == '.') {
			p.pos++
		}
		text := p.s[start:p.pos]
		if strings.Contains(text, ".") {
			f, err := strconv.ParseFloat(text, 64)
			return &lit{f}, err
		}
		n, err := strconv.ParseInt(text, 10, 64)
		if n == int64(int32(n)) {
			return &lit{int(n)}, err
		}
		return &lit{n}, err
	case isIdent(c, true):
		n := p.name()
		switch n {
		case "null":
			return &lit{nil}, nil
		case "true":
			return &lit{true}, nil
		case "false":
			return &lit{false}, nil
		}
		return &ident{n}, nil
	}
	return nil, fmt.Errorf("jexl: unexpected %q in %q", c, p.s)
}

// eval evaluates an expression; an error is JEXL's exception (JXLS then writes nothing).
func eval(e expr, ctx map[string]any) (any, error) {
	switch e := e.(type) {
	case *lit:
		return e.v, nil
	case *ident:
		return ctx[e.name], nil
	case *prop:
		t, err := eval(e.target, ctx)
		if err != nil {
			return nil, err
		}
		return property(t, e.name), nil
	case *index:
		t, err := eval(e.target, ctx)
		if err != nil {
			return nil, err
		}
		k, err := eval(e.key, ctx)
		if err != nil {
			return nil, err
		}
		if m, ok := t.(map[int64]any); ok {
			if key, ok := toLong(k); ok {
				return m[key], nil
			}
		}
		return nil, nil
	case *call:
		t, err := eval(e.target, ctx)
		if err != nil {
			return nil, err
		}
		args := make([]any, len(e.args))
		for i, a := range e.args {
			if args[i], err = eval(a, ctx); err != nil {
				return nil, err
			}
		}
		return method(t, e.name, args)
	case *unop:
		v, err := eval(e.e, ctx)
		if err != nil {
			return nil, err
		}
		if e.op == "!" {
			return !truthy(v), nil
		}
		switch x := v.(type) {
		case int:
			return -x, nil
		case int64:
			return -x, nil
		case float64:
			return -x, nil
		}
		return nil, nil
	case *ternary:
		c, err := eval(e.cond, ctx)
		if err != nil {
			return nil, err
		}
		if truthy(c) {
			return eval(e.a, ctx)
		}
		return eval(e.b, ctx)
	case *binop:
		l, err := eval(e.l, ctx)
		if err != nil {
			return nil, err
		}
		switch e.op {
		case "&&":
			if !truthy(l) {
				return false, nil
			}
			r, err := eval(e.r, ctx)
			return truthy(r), err
		case "||":
			if truthy(l) {
				return true, nil
			}
			r, err := eval(e.r, ctx)
			return truthy(r), err
		}
		r, err := eval(e.r, ctx)
		if err != nil {
			return nil, err
		}
		return binary(e.op, l, r)
	}
	return nil, fmt.Errorf("jexl: unknown expression %T", e)
}

func property(v any, name string) any {
	switch x := v.(type) {
	case Bean:
		return x[name]
	case Attributes:
		val, _ := x.A.Get(name)
		return val
	case map[string]any:
		return x[name]
	}
	return nil
}

var regexps = map[string]*regexp.Regexp{}

func method(v any, name string, args []any) (any, error) {
	switch x := v.(type) {
	case nil:
		return nil, nil
	case Bean:
		if len(args) == 0 {
			for _, prefix := range []string{"get", "is"} {
				if rest, ok := strings.CutPrefix(name, prefix); ok && rest != "" {
					return x[strings.ToLower(rest[:1])+rest[1:]], nil
				}
			}
		}
	case Attributes:
		if name == "toString" && len(args) == 0 {
			if x.A == nil {
				return "{}", nil
			}
			return model.JavaToString(x.A), nil
		}
	case string:
		switch {
		case name == "equals" && len(args) == 1:
			s, ok := args[0].(string)
			return ok && s == x, nil
		case name == "format" && len(args) >= 1:
			// String.format through an instance ("".format(...)).
			f, ok := args[0].(string)
			if !ok {
				return nil, fmt.Errorf("jexl: format pattern %T", args[0])
			}
			return javaFormat(f, args[1:])
		case name == "replaceAll" && len(args) == 2:
			re, ok1 := args[0].(string)
			repl, ok2 := args[1].(string)
			if !ok1 || !ok2 {
				return nil, nil
			}
			r, ok := regexps[re]
			if !ok {
				var err error
				if r, err = regexp.Compile(re); err != nil {
					return nil, err
				}
				regexps[re] = r
			}
			return r.ReplaceAllLiteralString(x, repl), nil
		case name == "toString" && len(args) == 0:
			return x, nil
		case name == "length" && len(args) == 0:
			return len([]rune(x)), nil
		}
	case dateTool:
		if name == "format" && len(args) == 4 {
			pattern, ok := args[0].(string)
			t, ok2 := args[1].(time.Time)
			if !ok || !ok2 {
				return nil, nil
			}
			if z, ok := args[3].(*time.Location); ok {
				t = t.In(z)
			}
			s, err := velocity.FormatDate(pattern, t)
			if err != nil {
				return nil, nil
			}
			return s, nil
		}
	case utilTool:
		if name == "hyperlink" && len(args) == 2 {
			return Hyperlink{URL: stringOf(args[0]), Text: stringOf(args[1])}, nil
		}
	}
	return nil, fmt.Errorf("jexl: unknown method %s on %T", name, v)
}

// javaFormat is String.format with the arguments as kit.Format takes them.
func javaFormat(f string, args []any) (s string, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("jexl: format %q: %v", f, r)
		}
	}()
	return kit.Format(f, args...), nil
}

func stringOf(v any) string {
	if v == nil {
		return "null"
	}
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return model.JavaToString(x)
	}
	return fmt.Sprint(v)
}

func toLong(v any) (int64, bool) {
	switch x := v.(type) {
	case int:
		return int64(x), true
	case int64:
		return x, true
	}
	return 0, false
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case float64:
		return x, true
	}
	return 0, false
}

// truthy is JexlArithmetic.toBoolean: null false; numbers not 0; strings neither empty
// nor "false"; anything else true.
func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != "" && x != "false"
	case int:
		return x != 0
	case int64:
		return x != 0
	case float64:
		return x != 0 && !math.IsNaN(x)
	}
	return true
}

func binary(op string, l, r any) (any, error) {
	switch op {
	case "==":
		return equal(l, r), nil
	case "!=":
		return !equal(l, r), nil
	case "<", "<=", ">", ">=":
		a, ok1 := toFloat(l)
		b, ok2 := toFloat(r)
		if !ok1 || !ok2 {
			return false, nil
		}
		switch op {
		case "<":
			return a < b, nil
		case "<=":
			return a <= b, nil
		case ">":
			return a > b, nil
		}
		return a >= b, nil
	case "+":
		_, ls := l.(string)
		_, rs := r.(string)
		if ls || rs {
			return stringOf(l) + stringOf(r), nil
		}
	}
	if l == nil || r == nil {
		return nil, nil
	}
	_, lf := l.(float64)
	_, rf := r.(float64)
	if lf || rf {
		a, ok1 := toFloat(l)
		b, ok2 := toFloat(r)
		if !ok1 || !ok2 {
			return nil, fmt.Errorf("jexl: %v %s %v", l, op, r)
		}
		switch op {
		case "+":
			return a + b, nil
		case "-":
			return a - b, nil
		case "*":
			return a * b, nil
		case "/":
			return a / b, nil
		case "%":
			return math.Mod(a, b), nil
		}
	}
	a, ok1 := toLong(l)
	b, ok2 := toLong(r)
	if !ok1 || !ok2 {
		return nil, fmt.Errorf("jexl: %v %s %v", l, op, r)
	}
	switch op {
	case "+":
		return a + b, nil
	case "-":
		return a - b, nil
	case "*":
		return a * b, nil
	case "/":
		if b == 0 {
			return nil, fmt.Errorf("jexl: division by zero")
		}
		return a / b, nil
	case "%":
		if b == 0 {
			return nil, fmt.Errorf("jexl: division by zero")
		}
		return a % b, nil
	}
	return nil, fmt.Errorf("jexl: unknown operator %s", op)
}

func equal(l, r any) bool {
	if l == nil || r == nil {
		return l == nil && r == nil
	}
	a, ok1 := toFloat(l)
	b, ok2 := toFloat(r)
	if ok1 && ok2 {
		return a == b
	}
	ls, ok1 := l.(string)
	rs, ok2 := r.(string)
	if ok1 && ok2 {
		return ls == rs
	}
	return fmt.Sprint(l) == fmt.Sprint(r)
}
