package api

import (
	"crypto/rand"
	"encoding/base32"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Web sessions, as Jetty keeps them for the Java server: in memory, identified by the
// JSESSIONID cookie (Path=/, HttpOnly), and not shared with Java (users log in again once at
// cutover; tokens keep working).
//
// Without web.sessionTimeout a session never expires, which is Jetty's default.

const sessionCookie = "JSESSIONID"

type session struct {
	id     string
	userID int64
	// expiration is set when the session was opened with a token; past it, the session
	// is invalidated on the next request.
	expiration time.Time
	origin     string // Origin header at login; later requests must match it if set
	lastAccess time.Time
}

type sessions struct {
	mu       sync.Mutex
	byID     map[string]*session
	timeout  time.Duration // 0: never expire
	sameSite string        // web.sameSiteCookie: "", "lax", "strict" or "none"
}

func newSessions(timeout time.Duration, sameSite string) *sessions {
	return &sessions{byID: map[string]*session{}, timeout: timeout, sameSite: strings.ToLower(sameSite)}
}

// get returns the request's session, or nil if it has none or it expired
// (HttpServletRequest.getSession(false)).
func (s *sessions) get(r *http.Request) *session {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess := s.byID[c.Value]
	if sess == nil {
		return nil
	}
	now := time.Now()
	if s.timeout > 0 && now.Sub(sess.lastAccess) > s.timeout {
		delete(s.byID, sess.id)
		return nil
	}
	sess.lastAccess = now
	return sess
}

// getOrCreate returns the current session, creating one (and its cookie) if needed
// (HttpServletRequest.getSession()). current is the session already resolved for this
// request, if any.
func (s *sessions) getOrCreate(w http.ResponseWriter, current *session) *session {
	if current != nil {
		s.mu.Lock()
		_, alive := s.byID[current.id]
		s.mu.Unlock()
		if alive {
			return current
		}
	}
	sess := &session{id: newSessionID(), lastAccess: time.Now()}
	s.mu.Lock()
	s.byID[sess.id] = sess
	s.mu.Unlock()
	s.setCookie(w, sess.id)
	return sess
}

func (s *sessions) invalidate(sess *session) {
	if sess == nil {
		return
	}
	s.mu.Lock()
	delete(s.byID, sess.id)
	s.mu.Unlock()
}

// setCookie writes the cookie as Jetty does, including its "Expires: 1970" header that
// keeps caches from storing the cookie.
func (s *sessions) setCookie(w http.ResponseWriter, id string) {
	c := sessionCookie + "=" + id + "; Path=/"
	if s.timeout > 0 {
		expires := time.Now().Add(s.timeout).UTC().Format(http.TimeFormat)
		c += "; Expires=" + expires + "; Max-Age=" + strconv.Itoa(int(s.timeout.Seconds()))
	}
	switch s.sameSite {
	case "lax":
		c += "; SameSite=Lax"
	case "strict":
		c += "; SameSite=Strict"
	case "none":
		c += "; Secure; SameSite=None"
	}
	c += "; HttpOnly"
	w.Header().Add("Set-Cookie", c)
	w.Header().Set("Expires", "Thu, 01 Jan 1970 00:00:00 GMT")
}

// newSessionID returns an id shaped like Jetty's ("node0" + random + ".node0").
func newSessionID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err) // crypto/rand doesn't fail on supported platforms
	}
	enc := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b[:])
	return "node0" + strings.ToLower(enc) + ".node0"
}
