// Package velocity renders Apache Velocity 2.4 templates (Traccar's notification
// templates, as configured there: defaults, so "lines" space gobbling, null references
// rendered as written, and empty values false in #if). It implements the part of the
// language the templates use: text, references with properties and method calls,
// #set, #if/#elseif/#else/#end (also as #{if}...), comments, string literals with
// interpolation, numbers, booleans and the arithmetic, comparison and logic operators.
package velocity

import (
	"fmt"
	"strconv"
	"strings"
)

// Template is a parsed template.
type Template struct {
	nodes []node
}

type node interface{}

type textNode string

// refNode is a reference: $root.member.member(args)..., ${...} or $!...
type refNode struct {
	src   string // as written: rendered when the value is null
	quiet bool
	root  string
	chain []member
}

type member struct {
	name string
	call bool
	args []expr
}

type setNode struct {
	target string
	value  expr
}

type ifNode struct {
	conds  []expr
	bodies [][]node
	els    []node // nil without #else
}

type expr interface{}

type literal struct{ v any }

type interpolated struct{ nodes []node }

type refExpr struct{ ref *refNode }

type binary struct {
	op   string
	l, r expr
}

type unary struct {
	op string
	e  expr
}

// ParseError is a template Velocity can't parse (ParseErrorException).
type ParseError struct {
	Pos int
	Msg string
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("velocity: parse error at %d: %s", e.Pos, e.Msg)
}

// Parse parses a template.
func Parse(src string) (*Template, error) {
	p := &parser{src: src}
	nodes, stop, err := p.nodes(true)
	if err != nil {
		return nil, err
	}
	if stop != "" {
		return nil, &ParseError{p.pos, "unexpected #" + stop}
	}
	return &Template{nodes: nodes}, nil
}

type parser struct {
	src string
	pos int
	// gobble is false inside interpolated string literals.
	gobble bool
	// elseifLineStart says whether the #elseif just returned is alone on its line.
	elseifLineStart bool
}

func isIdentStart(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_' }

func isIdentPart(c byte) bool { return isIdentStart(c) || c >= '0' && c <= '9' }

func (p *parser) peek(off int) byte {
	if p.pos+off < len(p.src) {
		return p.src[p.pos+off]
	}
	return 0
}

// nodes parses until the end, or until #elseif, #else or #end (returned as stop, the
// directive consumed; for #elseif the position is at its condition).
func (p *parser) nodes(top bool) ([]node, string, error) {
	if top {
		p.gobble = true
	}
	var out []node
	var text strings.Builder
	flush := func() {
		if text.Len() > 0 {
			out = append(out, textNode(text.String()))
			text.Reset()
		}
	}
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		switch c {
		case '#':
			start := p.pos
			// Comments.
			if p.peek(1) == '#' {
				end := strings.IndexByte(p.src[p.pos:], '\n')
				if end < 0 {
					p.pos = len(p.src)
				} else {
					p.pos += end + 1
				}
				continue
			}
			if p.peek(1) == '*' {
				end := strings.Index(p.src[p.pos+2:], "*#")
				if end < 0 {
					return nil, "", &ParseError{p.pos, "unterminated comment"}
				}
				p.pos += 2 + end + 2
				continue
			}
			name, after, ok := p.directiveName()
			if !ok {
				text.WriteByte(c)
				p.pos++
				continue
			}
			lineStart := p.lineOnlyWhitespaceBefore(start)
			p.pos = after
			var n node
			switch name {
			case "set":
				s, err := p.parseSet()
				if err != nil {
					return nil, "", err
				}
				n = s
			case "if":
				cond, err := p.parenExpr()
				if err != nil {
					return nil, "", err
				}
				p.gobbleLine(lineStart, &text)
				f, err := p.parseIf(cond)
				if err != nil {
					return nil, "", err
				}
				flush()
				out = append(out, f)
				continue
			case "elseif":
				p.gobbleBefore(lineStart, &text)
				flush()
				p.elseifLineStart = lineStart
				return out, "elseif", nil
			case "else", "end":
				p.gobbleLine(lineStart, &text)
				flush()
				return out, name, nil
			default:
				return nil, "", &ParseError{start, "unsupported directive #" + name}
			}
			p.gobbleLine(lineStart, &text)
			flush()
			out = append(out, n)
		case '$':
			ref, ok, err := p.reference()
			if err != nil {
				return nil, "", err
			}
			if !ok {
				text.WriteString(ref.src)
				continue
			}
			flush()
			out = append(out, ref)
		default:
			text.WriteByte(c)
			p.pos++
		}
	}
	flush()
	return out, "", nil
}

// directiveName reads #name or #{name} at pos; ok is false when it isn't a directive
// Velocity knows (the # is then text).
func (p *parser) directiveName() (string, int, bool) {
	i := p.pos + 1
	braced := i < len(p.src) && p.src[i] == '{'
	if braced {
		i++
	}
	j := i
	for j < len(p.src) && isIdentPart(p.src[j]) {
		j++
	}
	name := p.src[i:j]
	if braced {
		if j >= len(p.src) || p.src[j] != '}' {
			return "", 0, false
		}
		j++
	}
	switch name {
	case "set", "if", "elseif", "else", "end":
		return name, j, true
	case "foreach", "break", "stop", "macro", "define", "include", "parse", "evaluate":
		return name, j, true
	}
	return "", 0, false
}

func (p *parser) lineOnlyWhitespaceBefore(i int) bool {
	if !p.gobble {
		return false
	}
	for j := i - 1; j >= 0; j-- {
		switch p.src[j] {
		case '\n':
			return true
		case ' ', '\t':
		default:
			return false
		}
	}
	return true
}

// gobbleLine applies "lines" space gobbling after a directive that ends at pos: when the
// directive is alone on its line, the indentation before it and the rest of the line
// (whitespace and the newline) are dropped.
func (p *parser) gobbleLine(lineStart bool, text *strings.Builder) {
	if !lineStart {
		return
	}
	j := p.pos
	for j < len(p.src) && (p.src[j] == ' ' || p.src[j] == '\t') {
		j++
	}
	switch {
	case j == len(p.src):
	case p.src[j] == '\n':
		j++
	case p.src[j] == '\r' && j+1 < len(p.src) && p.src[j+1] == '\n':
		j += 2
	default:
		return
	}
	p.gobbleBefore(true, text)
	p.pos = j
}

// gobbleBefore drops the indentation before a directive alone on its line.
func (p *parser) gobbleBefore(lineStart bool, text *strings.Builder) {
	if !lineStart {
		return
	}
	s := text.String()
	trimmed := strings.TrimRight(s, " \t")
	if len(trimmed) != len(s) {
		text.Reset()
		text.WriteString(trimmed)
	}
}

func (p *parser) parseIf(cond expr) (*ifNode, error) {
	n := &ifNode{}
	for {
		body, stop, err := p.nodes(false)
		if err != nil {
			return nil, err
		}
		n.conds = append(n.conds, cond)
		n.bodies = append(n.bodies, body)
		switch stop {
		case "end":
			return n, nil
		case "else":
			els, stop, err := p.nodes(false)
			if err != nil {
				return nil, err
			}
			if stop != "end" {
				return nil, &ParseError{p.pos, "#else without #end"}
			}
			if els == nil {
				els = []node{}
			}
			n.els = els
			return n, nil
		case "elseif":
			lineStart := p.elseifLineStart
			if cond, err = p.parenExpr(); err != nil {
				return nil, err
			}
			var discard strings.Builder
			p.gobbleLine(lineStart, &discard)
		default:
			return nil, &ParseError{p.pos, "#if without #end"}
		}
	}
}

func (p *parser) skipSpace() {
	for p.pos < len(p.src) && strings.IndexByte(" \t\r\n", p.src[p.pos]) >= 0 {
		p.pos++
	}
}

func (p *parser) parenExpr() (expr, error) {
	for p.pos < len(p.src) && (p.src[p.pos] == ' ' || p.src[p.pos] == '\t') {
		p.pos++
	}
	if p.peek(0) != '(' {
		return nil, &ParseError{p.pos, "expected ("}
	}
	p.pos++
	e, err := p.expr()
	if err != nil {
		return nil, err
	}
	p.skipSpace()
	if p.peek(0) != ')' {
		return nil, &ParseError{p.pos, "expected )"}
	}
	p.pos++
	return e, nil
}

func (p *parser) parseSet() (*setNode, error) {
	for p.pos < len(p.src) && (p.src[p.pos] == ' ' || p.src[p.pos] == '\t') {
		p.pos++
	}
	if p.peek(0) != '(' {
		return nil, &ParseError{p.pos, "expected ("}
	}
	p.pos++
	p.skipSpace()
	if p.peek(0) != '$' {
		return nil, &ParseError{p.pos, "expected a reference"}
	}
	ref, ok, err := p.reference()
	if err != nil {
		return nil, err
	}
	if !ok || len(ref.chain) > 0 {
		return nil, &ParseError{p.pos, "unsupported #set target"}
	}
	p.skipSpace()
	if p.peek(0) != '=' {
		return nil, &ParseError{p.pos, "expected ="}
	}
	p.pos++
	value, err := p.expr()
	if err != nil {
		return nil, err
	}
	p.skipSpace()
	if p.peek(0) != ')' {
		return nil, &ParseError{p.pos, "expected )"}
	}
	p.pos++
	return &setNode{target: ref.root, value: value}, nil
}

// reference reads a reference at '$'. ok is false when the text isn't one (ref.src is
// then the literal text consumed).
func (p *parser) reference() (*refNode, bool, error) {
	start := p.pos
	i := p.pos + 1
	quiet := i < len(p.src) && p.src[i] == '!'
	if quiet {
		i++
	}
	formal := i < len(p.src) && p.src[i] == '{'
	if formal {
		i++
	}
	if i >= len(p.src) || !isIdentStart(p.src[i]) {
		p.pos = start + 1
		return &refNode{src: "$"}, false, nil
	}
	j := i
	for j < len(p.src) && isIdentPart(p.src[j]) {
		j++
	}
	ref := &refNode{quiet: quiet, root: p.src[i:j]}
	p.pos = j
	for p.peek(0) == '.' && isIdentStart(p.peek(1)) {
		k := p.pos + 1
		m := k
		for m < len(p.src) && isIdentPart(p.src[m]) {
			m++
		}
		mem := member{name: p.src[k:m]}
		p.pos = m
		if p.peek(0) == '(' {
			p.pos++
			args, err := p.args()
			if err != nil {
				return nil, false, err
			}
			mem.call, mem.args = true, args
		}
		ref.chain = append(ref.chain, mem)
	}
	if formal {
		if p.peek(0) != '}' {
			return nil, false, &ParseError{p.pos, "expected }"}
		}
		p.pos++
	}
	ref.src = p.src[start:p.pos]
	return ref, true, nil
}

func (p *parser) args() ([]expr, error) {
	var args []expr
	p.skipSpace()
	if p.peek(0) == ')' {
		p.pos++
		return args, nil
	}
	for {
		e, err := p.expr()
		if err != nil {
			return nil, err
		}
		args = append(args, e)
		p.skipSpace()
		switch p.peek(0) {
		case ',':
			p.pos++
		case ')':
			p.pos++
			return args, nil
		default:
			return nil, &ParseError{p.pos, "expected , or )"}
		}
	}
}

// Expressions: || && == != < <= > >= + - * / % ! unary -, parentheses, literals and
// references, with Velocity's word operators (or and eq ne lt le gt ge not).
func (p *parser) expr() (expr, error) { return p.binaryLevel(0) }

var levels = [][]string{
	{"||", "or"},
	{"&&", "and"},
	{"==", "!=", "eq", "ne"},
	{"<=", ">=", "<", ">", "le", "ge", "lt", "gt"},
	{"+", "-"},
	{"*", "/", "%"},
}

var wordOps = map[string]string{"or": "||", "and": "&&", "eq": "==", "ne": "!=", "le": "<=", "ge": ">=", "lt": "<", "gt": ">"}

func (p *parser) binaryLevel(level int) (expr, error) {
	if level == len(levels) {
		return p.unaryExpr()
	}
	l, err := p.binaryLevel(level + 1)
	if err != nil {
		return nil, err
	}
	for {
		p.skipSpace()
		op := p.matchOp(levels[level])
		if op == "" {
			return l, nil
		}
		r, err := p.binaryLevel(level + 1)
		if err != nil {
			return nil, err
		}
		l = &binary{op: op, l: l, r: r}
	}
}

func (p *parser) matchOp(ops []string) string {
	for _, op := range ops {
		if !strings.HasPrefix(p.src[p.pos:], op) {
			continue
		}
		end := p.pos + len(op)
		if isIdentStart(op[0]) && end < len(p.src) && isIdentPart(p.src[end]) {
			continue // a word operator must stand alone
		}
		p.pos = end
		if w, ok := wordOps[op]; ok {
			return w
		}
		return op
	}
	return ""
}

func (p *parser) unaryExpr() (expr, error) {
	p.skipSpace()
	switch {
	case p.peek(0) == '!' && p.peek(1) != '=':
		p.pos++
		e, err := p.unaryExpr()
		if err != nil {
			return nil, err
		}
		return &unary{op: "!", e: e}, nil
	case strings.HasPrefix(p.src[p.pos:], "not") && !isIdentPart(p.peek(3)):
		p.pos += 3
		e, err := p.unaryExpr()
		if err != nil {
			return nil, err
		}
		return &unary{op: "!", e: e}, nil
	case p.peek(0) == '-' && (p.peek(1) >= '0' && p.peek(1) <= '9'):
		p.pos++
		e, err := p.primary()
		if err != nil {
			return nil, err
		}
		return &unary{op: "-", e: e}, nil
	}
	return p.primary()
}

func (p *parser) primary() (expr, error) {
	p.skipSpace()
	c := p.peek(0)
	switch {
	case c == '(':
		p.pos++
		e, err := p.expr()
		if err != nil {
			return nil, err
		}
		p.skipSpace()
		if p.peek(0) != ')' {
			return nil, &ParseError{p.pos, "expected )"}
		}
		p.pos++
		return e, nil
	case c == '$':
		ref, ok, err := p.reference()
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, &ParseError{p.pos, "expected a reference"}
		}
		return &refExpr{ref: ref}, nil
	case c == '"' || c == '\'':
		return p.stringLiteral(c)
	case c >= '0' && c <= '9':
		j := p.pos
		for j < len(p.src) && p.src[j] >= '0' && p.src[j] <= '9' {
			j++
		}
		if j+1 < len(p.src) && p.src[j] == '.' && p.src[j+1] >= '0' && p.src[j+1] <= '9' {
			j++
			for j < len(p.src) && p.src[j] >= '0' && p.src[j] <= '9' {
				j++
			}
			v, err := strconv.ParseFloat(p.src[p.pos:j], 64)
			if err != nil {
				return nil, &ParseError{p.pos, err.Error()}
			}
			p.pos = j
			return &literal{v}, nil
		}
		v, err := strconv.ParseInt(p.src[p.pos:j], 10, 64)
		if err != nil {
			return nil, &ParseError{p.pos, err.Error()}
		}
		p.pos = j
		if v == int64(int32(v)) {
			return &literal{int(v)}, nil
		}
		return &literal{v}, nil
	case strings.HasPrefix(p.src[p.pos:], "true") && !isIdentPart(p.peek(4)):
		p.pos += 4
		return &literal{true}, nil
	case strings.HasPrefix(p.src[p.pos:], "false") && !isIdentPart(p.peek(5)):
		p.pos += 5
		return &literal{false}, nil
	case strings.HasPrefix(p.src[p.pos:], "null") && !isIdentPart(p.peek(4)):
		p.pos += 4
		return &literal{nil}, nil
	}
	return nil, &ParseError{p.pos, "unexpected input"}
}

// stringLiteral reads '...' (as is) or "..." (interpolated: parsed as a template). A
// doubled quote inside stands for one.
func (p *parser) stringLiteral(q byte) (expr, error) {
	start := p.pos
	p.pos++
	var b strings.Builder
	for {
		if p.pos >= len(p.src) {
			return nil, &ParseError{start, "unterminated string"}
		}
		c := p.src[p.pos]
		if c == q {
			if p.peek(1) == q {
				b.WriteByte(q)
				p.pos += 2
				continue
			}
			p.pos++
			break
		}
		b.WriteByte(c)
		p.pos++
	}
	s := b.String()
	if q == '\'' || !strings.ContainsAny(s, "$#") {
		return &literal{s}, nil
	}
	sub := &parser{src: s}
	nodes, stop, err := sub.nodes(false)
	if err != nil {
		return nil, err
	}
	if stop != "" {
		return nil, &ParseError{start, "unexpected #" + stop + " in string"}
	}
	return &interpolated{nodes: nodes}, nil
}
