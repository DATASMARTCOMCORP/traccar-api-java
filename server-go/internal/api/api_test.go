package api

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

// Behavior is checked end to end against the Java server by the compat harness
// (go run ./compat -suite auth -b go); these tests cover the pure helpers.

func TestAcceptsJSON(t *testing.T) {
	for accept, want := range map[string]bool{
		"": true, "*/*": true, "application/json": true, "application/*": true,
		"text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8": true,
		"text/html":            false,
		"application/json;q=0": false,
		"text/plain, application/json; charset=utf-8": true,
	} {
		if got := acceptsJSON(accept); got != want {
			t.Errorf("acceptsJSON(%q) = %v, want %v", accept, got, want)
		}
	}
}

func TestJavaHostAddress(t *testing.T) {
	for in, want := range map[string]string{
		"127.0.0.1": "127.0.0.1", "::1": "0:0:0:0:0:0:0:1", "[2001:db8::1]": "2001:db8:0:0:0:0:0:1",
		"::ffff:10.0.0.1": "10.0.0.1", "not-an-ip": "not-an-ip",
	} {
		if got := javaHostAddress(in); got != want {
			t.Errorf("javaHostAddress(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRemoteAddressProxyHeaders(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.1.1.1:5555"
	if got := remoteAddress(r); got != "10.1.1.1" {
		t.Errorf("peer: %s", got)
	}
	r.Header.Set("X-Forwarded-For", "203.0.113.7, 10.0.0.2")
	if got := remoteAddress(r); got != "203.0.113.7" {
		t.Errorf("X-Forwarded-For: %s", got)
	}
	r.Header.Set("CF-Connecting-IP", "198.51.100.9")
	if got := remoteAddress(r); got != "198.51.100.9" {
		t.Errorf("CF-Connecting-IP takes precedence: %s", got)
	}
}

func TestJavaSplit(t *testing.T) {
	for in, want := range map[string][]string{
		"Bearer abc": {"Bearer", "abc"}, "Basic": {"Basic"}, "Basic ": {"Basic"},
		"Bearer  abc": {"Bearer", "", "abc"}, "": {},
	} {
		if got := javaSplit(in, " "); !slices.Equal(got, want) {
			t.Errorf("javaSplit(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCORS(t *testing.T) {
	a := &API{opts: Options{Origin: "https://a.example https://b.example"}}
	for origin, want := range map[string]string{"": "*", "https://a.example": "https://a.example", "https://evil.example": ""} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/api/server", nil)
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		a.cors(w, r)
		if got := w.Header().Get("Access-Control-Allow-Origin"); got != want {
			t.Errorf("origin %q: got %q, want %q", origin, got, want)
		}
		if w.Header().Get("Access-Control-Allow-Methods") != "GET, POST, PUT, DELETE, OPTIONS" {
			t.Error("methods header missing")
		}
	}
}

func TestSessionCookieShape(t *testing.T) {
	s := newSessions(0, "")
	w := httptest.NewRecorder()
	sess := s.getOrCreate(w, nil)
	cookie := w.Header().Get("Set-Cookie")
	if cookie != "JSESSIONID="+sess.id+"; Path=/; HttpOnly" {
		t.Errorf("cookie %q", cookie)
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(&http.Cookie{Name: "JSESSIONID", Value: sess.id})
	if s.get(r) != sess {
		t.Error("session not found by cookie")
	}
	s.invalidate(sess)
	if s.get(r) != nil {
		t.Error("invalidated session still found")
	}
	w = httptest.NewRecorder()
	newSessions(3600_000_000_000, "None").setCookie(w, "x")
	if got := w.Header().Get("Set-Cookie"); !containsAll(got, "Max-Age=3600", "Secure", "SameSite=None", "HttpOnly") {
		t.Errorf("configured cookie %q", got)
	}
}

// containsAll reports whether a Set-Cookie value has every attribute (a prefix such as
// "Max-Age=3600" is enough).
func containsAll(cookie string, attrs ...string) bool {
	parts := strings.Split(cookie, ";")
	for _, want := range attrs {
		if !slices.ContainsFunc(parts, func(p string) bool { return strings.HasPrefix(strings.TrimSpace(p), want) }) {
			return false
		}
	}
	return true
}
