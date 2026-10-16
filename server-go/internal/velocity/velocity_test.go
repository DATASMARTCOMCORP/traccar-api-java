package velocity

import "testing"

// The cases are what Velocity 2.4.1 printed for the same templates and values.
func TestJavaSemantics(t *testing.T) {
	ctx := func() Context {
		return Context{"m": map[string]string{"k": "val"}, "emptyMap": map[string]string{}, "d": 2.5, "s": "hello"}
	}
	cases := []struct{ tpl, want string }{
		{`#if(0)T#{else}F#end|#if(0.0)T#{else}F#end|#if("")T#{else}F#end|#if(" ")T#{else}F#end|#if($m)T#{else}F#end|#if($emptyMap)T#{else}F#end`, "F|F|F|T|T|F"},
		{"#set($x = 'a')#set($x = $nothing)[$x]", "[$x]"},
		{"#set($y = 2 * 1.5)$y #set($z = 2 * 3)$z #set($w = $d * 2)$w #set($v = 10 / 4)$v #set($u = 10.0 / 4)$u", "3.0 6 5.0 2 2.5"},
		{`#if("1" == 1)T#{else}F#end #if($nothing == $nothing2)T#{else}F#end #if(1 == 1.0)T#{else}F#end`, "T T T"},
		{"a\n  #set($q = 1)  \nb\n#if(true)\nc\n#end\nd #if(true)e#end\n", "a\nb\nc\nd e\n"},
		{"$m.missing|$m.get('k')|${m.k}|$!m.missing|$m.k.length()|$s.substring(1)|$s.toUpperCase()", "$m.missing|val|val||3|ello|HELLO"},
		{"x ## comment\ny #* block *# z #foo #{bar} $ $1 $.a", "x y  z #foo #{bar} $ $1 $.a"},
		{`#set($s2 = "a""b")$s2 #set($s3 = 'c''d')$s3`, `a"b c'd`},
	}
	for _, c := range cases {
		tpl, err := Parse(c.tpl)
		if err != nil {
			t.Errorf("%q: %v", c.tpl, err)
			continue
		}
		got, err := tpl.Render(ctx())
		if err != nil || got != c.want {
			t.Errorf("%q:\n got %q (%v)\nwant %q", c.tpl, got, err, c.want)
		}
	}
}

func TestDecimalAndHTML(t *testing.T) {
	for _, c := range []struct {
		pattern string
		v       any
		want    string
	}{
		{"0.0 km/h", 50.004, "50.0 km/h"}, {"0.0 kn", 0.15, "0.1 kn"}, {"0.0", 0.25, "0.2"}, {"0.0", -0.04, "-0.0"},
		{"#,##0.00", 1234567.891, "1,234,567.89"}, {"0.0", 7, "7.0"},
	} {
		if got, _ := FormatDecimal(c.pattern, c.v); got != c.want {
			t.Errorf("%s %v: %q, want %q", c.pattern, c.v, got, c.want)
		}
	}
	if got := EscapeHTML(`<a href="x">Caminhão & 'y' €</a>`); got != `&lt;a href=&quot;x&quot;&gt;Caminh&atilde;o &amp; 'y' &euro;&lt;/a&gt;` {
		t.Errorf("escape: %q", got)
	}
}
