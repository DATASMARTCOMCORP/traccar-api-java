package velocity

import (
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
)

// Context holds a render's variables; #set changes it.
//
// Values are Java values: nil (null), string, bool, int (Integer), int64 (Long), float64
// (Double), map[string]string or map[string]any (a Map), *Date (java.util.Date), and
// Objects (beans and tools).
type Context map[string]any

// Object is a Java object with bean properties and methods.
type Object interface {
	// Property is $x.name (getName(), isName() or get("name")); ok is false without one.
	Property(name string) (v any, ok bool)
	// Method is $x.name(args); ok is false without such a method. An error is the
	// method's exception (Velocity's MethodInvocationException ends the render).
	Method(name string, args []any) (v any, ok bool, err error)
}

// Date is a java.util.Date (mutable, as templates call setTime on one).
type Date struct{ Millis int64 }

// NewDate is new Date(t).
func NewDate(t time.Time) *Date { return &Date{Millis: t.UnixMilli()} }

func (d *Date) Property(name string) (any, bool) {
	if name == "time" {
		return d.Millis, true
	}
	return nil, false
}

func (d *Date) Method(name string, args []any) (any, bool, error) {
	switch {
	case name == "getTime" && len(args) == 0:
		return d.Millis, true, nil
	case name == "setTime" && len(args) == 1:
		ms, ok := toLong(args[0])
		if !ok {
			return nil, false, nil
		}
		d.Millis = ms
		return nil, true, nil
	}
	return nil, false, nil
}

// TimeZone is a java.util.TimeZone.
type TimeZone struct{ Location *time.Location }

func (TimeZone) Property(string) (any, bool)             { return nil, false }
func (TimeZone) Method(string, []any) (any, bool, error) { return nil, false, nil }
func (z TimeZone) String() string                        { return z.Location.String() }

// Locale is a java.util.Locale ("en_US").
type Locale string

func (Locale) Property(string) (any, bool)             { return nil, false }
func (Locale) Method(string, []any) (any, bool, error) { return nil, false, nil }

// MethodError is an exception thrown by a method a template calls.
type MethodError struct {
	Ref string
	Err error
}

func (e *MethodError) Error() string { return fmt.Sprintf("velocity: %s threw %v", e.Ref, e.Err) }

func (e *MethodError) Unwrap() error { return e.Err }

// Render merges the template with ctx (Template.merge).
func (t *Template) Render(ctx Context) (string, error) {
	var b strings.Builder
	err := renderNodes(&b, t.nodes, ctx)
	return b.String(), err
}

func renderNodes(b *strings.Builder, nodes []node, ctx Context) error {
	for _, n := range nodes {
		switch n := n.(type) {
		case textNode:
			b.WriteString(string(n))
		case *refNode:
			v, err := evalRef(n, ctx)
			if err != nil {
				return err
			}
			switch {
			case v != nil:
				b.WriteString(toString(v))
			case !n.quiet:
				b.WriteString(n.src)
			}
		case *setNode:
			v, err := eval(n.value, ctx)
			if err != nil {
				return err
			}
			if v == nil {
				delete(ctx, n.target)
			} else {
				ctx[n.target] = v
			}
		case *ifNode:
			done := false
			for i, cond := range n.conds {
				v, err := eval(cond, ctx)
				if err != nil {
					return err
				}
				if truthy(v) {
					if err := renderNodes(b, n.bodies[i], ctx); err != nil {
						return err
					}
					done = true
					break
				}
			}
			if !done && n.els != nil {
				if err := renderNodes(b, n.els, ctx); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func evalRef(r *refNode, ctx Context) (any, error) {
	v := ctx[r.root]
	for _, m := range r.chain {
		if v == nil {
			return nil, nil
		}
		if !m.call {
			v = property(v, m.name)
			continue
		}
		args := make([]any, len(m.args))
		for i, a := range m.args {
			av, err := eval(a, ctx)
			if err != nil {
				return nil, err
			}
			args[i] = av
		}
		var err error
		if v, err = call(v, m.name, args); err != nil {
			return nil, &MethodError{Ref: r.src, Err: err}
		}
	}
	return v, nil
}

func property(v any, name string) any {
	switch x := v.(type) {
	case Object:
		p, _ := x.Property(name)
		return p
	case map[string]string:
		if s, ok := x[name]; ok {
			return s
		}
	case map[string]any:
		return x[name]
	}
	return nil
}

func call(v any, name string, args []any) (any, error) {
	switch x := v.(type) {
	case Object:
		r, _, err := x.Method(name, args)
		return r, err
	case string:
		return stringMethod(x, name, args)
	case map[string]string:
		if name == "get" && len(args) == 1 && args[0] != nil {
			if s, ok := x[toString(args[0])]; ok {
				return s, nil
			}
		}
	case map[string]any:
		if name == "get" && len(args) == 1 && args[0] != nil {
			return x[toString(args[0])], nil
		}
	}
	return nil, nil
}

// javaIndexError is StringIndexOutOfBoundsException.
type javaIndexError struct{ begin, end, length int }

func (e javaIndexError) Error() string {
	return fmt.Sprintf("StringIndexOutOfBoundsException: begin %d, end %d, length %d", e.begin, e.end, e.length)
}

// stringMethod is a java.lang.String method, with UTF-16 indexes.
func stringMethod(s, name string, args []any) (any, error) {
	units := utf16.Encode([]rune(s))
	switch name {
	case "substring":
		if len(args) < 1 || len(args) > 2 {
			return nil, nil
		}
		begin, ok := toInt(args[0])
		if !ok {
			return nil, nil
		}
		end := len(units)
		if len(args) == 2 {
			if end, ok = toInt(args[1]); !ok {
				return nil, nil
			}
		}
		if begin < 0 || end > len(units) || begin > end {
			return nil, javaIndexError{begin, end, len(units)}
		}
		return string(utf16.Decode(units[begin:end])), nil
	case "toUpperCase":
		if len(args) == 0 {
			return strings.ToUpper(s), nil
		}
	case "toLowerCase":
		if len(args) == 0 {
			return strings.ToLower(s), nil
		}
	case "length":
		if len(args) == 0 {
			return len(units), nil
		}
	case "trim":
		if len(args) == 0 {
			return strings.TrimFunc(s, func(r rune) bool { return r <= ' ' }), nil
		}
	case "isEmpty":
		if len(args) == 0 {
			return len(units) == 0, nil
		}
	case "toString":
		if len(args) == 0 {
			return s, nil
		}
	case "equals":
		if len(args) == 1 {
			o, ok := args[0].(string)
			return ok && o == s, nil
		}
	case "contains", "startsWith", "endsWith":
		if len(args) == 1 && args[0] != nil {
			o := toString(args[0])
			switch name {
			case "contains":
				return strings.Contains(s, o), nil
			case "startsWith":
				return strings.HasPrefix(s, o), nil
			}
			return strings.HasSuffix(s, o), nil
		}
	case "replace":
		if len(args) == 2 && args[0] != nil && args[1] != nil {
			return strings.ReplaceAll(s, toString(args[0]), toString(args[1])), nil
		}
	}
	return nil, nil
}

func toInt(v any) (int, bool) {
	switch x := v.(type) {
	case int:
		return x, true
	case int64:
		if x == int64(int32(x)) {
			return int(x), true
		}
	}
	return 0, false
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

func eval(e expr, ctx Context) (any, error) {
	switch e := e.(type) {
	case *literal:
		return e.v, nil
	case *interpolated:
		var b strings.Builder
		if err := renderNodes(&b, e.nodes, ctx); err != nil {
			return nil, err
		}
		return b.String(), nil
	case *refExpr:
		return evalRef(e.ref, ctx)
	case *unary:
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
	case *binary:
		switch e.op {
		case "&&", "||":
			l, err := eval(e.l, ctx)
			if err != nil {
				return nil, err
			}
			if e.op == "&&" && !truthy(l) {
				return false, nil
			}
			if e.op == "||" && truthy(l) {
				return true, nil
			}
			r, err := eval(e.r, ctx)
			if err != nil {
				return nil, err
			}
			return truthy(r), nil
		}
		l, err := eval(e.l, ctx)
		if err != nil {
			return nil, err
		}
		r, err := eval(e.r, ctx)
		if err != nil {
			return nil, err
		}
		switch e.op {
		case "==":
			return equal(l, r), nil
		case "!=":
			return !equal(l, r), nil
		case "<", "<=", ">", ">=":
			c, ok := compare(l, r)
			if !ok {
				return false, nil
			}
			switch e.op {
			case "<":
				return c < 0, nil
			case "<=":
				return c <= 0, nil
			case ">":
				return c > 0, nil
			}
			return c >= 0, nil
		}
		return arithmetic(e.op, l, r), nil
	}
	return nil, fmt.Errorf("velocity: unknown expression %T", e)
}

func isNumber(v any) bool {
	switch v.(type) {
	case int, int64, float64:
		return true
	}
	return false
}

func asFloat(v any) float64 {
	switch x := v.(type) {
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case float64:
		return x
	}
	return 0
}

// equal is ASTEQNode: nulls are equal, numbers compare by value, and other values by
// equals when of the same type, else by their text.
func equal(l, r any) bool {
	switch {
	case l == nil || r == nil:
		return l == nil && r == nil
	case isNumber(l) && isNumber(r):
		c, _ := compare(l, r)
		return c == 0
	case reflect.TypeOf(l) == reflect.TypeOf(r) && reflect.TypeOf(l).Comparable():
		return l == r
	}
	return toString(l) == toString(r)
}

func compare(l, r any) (int, bool) {
	if isNumber(l) && isNumber(r) {
		_, lf := l.(float64)
		_, rf := r.(float64)
		if !lf && !rf {
			a, _ := toLong(l)
			b, _ := toLong(r)
			switch {
			case a < b:
				return -1, true
			case a > b:
				return 1, true
			}
			return 0, true
		}
		a, b := asFloat(l), asFloat(r)
		switch {
		case a < b:
			return -1, true
		case a > b:
			return 1, true
		}
		return 0, true
	}
	ls, lok := l.(string)
	rs, rok := r.(string)
	if lok && rok {
		return strings.Compare(ls, rs), true
	}
	return 0, false
}

// arithmetic is Velocity's MathUtils: integers stay integers (Integer when both are and
// the result fits, else Long), anything with a double is a double; + with a string
// concatenates. A null operand or a division by zero gives null.
func arithmetic(op string, l, r any) any {
	if op == "+" {
		_, ls := l.(string)
		_, rs := r.(string)
		if (ls || rs) && l != nil && r != nil {
			return toString(l) + toString(r)
		}
	}
	if !isNumber(l) || !isNumber(r) {
		return nil
	}
	_, lf := l.(float64)
	_, rf := r.(float64)
	if lf || rf {
		a, b := asFloat(l), asFloat(r)
		switch op {
		case "+":
			return a + b
		case "-":
			return a - b
		case "*":
			return a * b
		case "/":
			if b == 0 {
				return nil
			}
			return a / b
		case "%":
			if b == 0 {
				return nil
			}
			return math.Mod(a, b)
		}
		return nil
	}
	a, _ := toLong(l)
	b, _ := toLong(r)
	var v int64
	switch op {
	case "+":
		v = a + b
	case "-":
		v = a - b
	case "*":
		v = a * b
	case "/":
		if b == 0 {
			return nil
		}
		v = a / b
	case "%":
		if b == 0 {
			return nil
		}
		v = a % b
	default:
		return nil
	}
	_, li := l.(int)
	_, ri := r.(int)
	if li && ri && v == int64(int32(v)) {
		return int(v)
	}
	return v
}

// truthy is #if's test (DuckType.asBoolean with empty checks): null, false, empty
// strings, maps and collections, zero, and objects that print as "" are false.
func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case int:
		return x != 0
	case int64:
		return x != 0
	case float64:
		return x != 0
	case map[string]string:
		return len(x) > 0
	case map[string]any:
		return len(x) > 0
	case fmt.Stringer:
		return x.String() != ""
	}
	return true
}

// String is how Velocity prints a value: Java's toString.
func String(v any) string { return toString(v) }

// toString is how Velocity prints a value: Java's toString.
func toString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return model.JavaToString(x)
	case *Date:
		return time.UnixMilli(x.Millis).Format("Mon Jan 02 15:04:05 MST 2006")
	case fmt.Stringer:
		return x.String()
	}
	return fmt.Sprint(v)
}
