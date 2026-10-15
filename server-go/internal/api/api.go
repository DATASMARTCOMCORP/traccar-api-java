// Package api serves the REST API with the Java server's paths, parameters, status codes
// and JSON. It is plain net/http: one handler function per endpoint, and a request wrapper
// that does what Jersey's filters do in Java (authentication, CORS, error mapping).
//
// Error bodies are a short plain-text message where Java sends a stack trace (agreed in
// MIGRATION.md). Status codes are Java's, including its quirks: an unexpected exception
// in a resource is 400 there, so a plain error returned by a handler is 400 here too.
package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/auth"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/config"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/store"
)

// Version is what /api/server reports. Clients compare it with the Java server's, so it
// is the Traccar version this server is compatible with, not the Go build's version.
const Version = "6.14.5"

// Options are the web settings from the config file.
type Options struct {
	Origin              string        // web.origin: allowed CORS origins; "" or "*" allows any
	ServiceAccountToken string        // web.serviceAccountToken
	SessionTimeout      time.Duration // web.sessionTimeout
	SameSiteCookie      string        // web.sameSiteCookie
	EmailEnabled        bool          // mail.debug, or mail.smtp.host set
	TextEnabled         bool          // sms.http.url or sms.aws.region set
	GeocoderEnabled     bool          // geocoder.enable
	Notificators        []string      // notificator.types
	PositionPeriod      time.Duration // database.positionPeriod: latest positions newer than this
	MaxPositions        int           // report.maxPositions
	// ProtocolEnabled reports whether this server runs a protocol (enabled, with a port),
	// as ServerManager.getProtocol does.
	ProtocolEnabled func(name string) bool
	// PushCommands: a push command sender is configured (command.client.serviceAccount
	// or notificator.traccar.key).
	PushCommands bool
}

// OptionsFromConfig reads the web and feature keys with Java's defaults.
func OptionsFromConfig(c *config.Config) (Options, error) {
	timeout, err := c.Int("web.sessionTimeout", 0)
	if err != nil {
		return Options{}, err
	}
	period, err := c.Int64("database.positionPeriod", int64(defaultPositionPeriod/time.Second))
	if err != nil {
		return Options{}, err
	}
	maxPositions, err := c.Int("report.maxPositions", defaultMaxPositions)
	if err != nil {
		return Options{}, err
	}
	var notificators []string
	if v, ok := c.Lookup("notificator.types"); ok {
		notificators = strings.Split(v, ",")
	}
	return Options{
		Notificators:   notificators,
		PositionPeriod: time.Duration(period) * time.Second,
		MaxPositions:   maxPositions,
		ProtocolEnabled: func(name string) bool {
			port, err := c.Port(name)
			return err == nil && port > 0 && c.ProtocolEnabled(name)
		},
		PushCommands:        c.Has("command.client.serviceAccount") || c.Has("notificator.traccar.key"),
		Origin:              c.String("web.origin", ""),
		ServiceAccountToken: c.String("web.serviceAccountToken", ""),
		SessionTimeout:      time.Duration(timeout) * time.Second,
		SameSiteCookie:      c.String("web.sameSiteCookie", ""),
		EmailEnabled:        c.Bool("mail.debug", false) || c.Has("mail.smtp.host"),
		TextEnabled:         c.Has("sms.http.url") || c.Has("sms.aws.region"),
		GeocoderEnabled:     c.Bool("geocoder.enable", true),
	}, nil
}

// API holds the dependencies of the handlers.
type API struct {
	opts     Options
	st       *store.Store
	tokens   *auth.Tokens
	sessions *sessions
	mux      *http.ServeMux
	// storageSpace reports free and total bytes of the host's file systems for
	// administrators; replaceable in tests.
	storageSpace func() []int64
}

func New(opts Options, st *store.Store, tokens *auth.Tokens) *API {
	a := &API{
		opts:         opts,
		st:           st,
		tokens:       tokens,
		sessions:     newSessions(opts.SessionTimeout, opts.SameSiteCookie),
		mux:          http.NewServeMux(),
		storageSpace: storageSpace,
	}
	a.routes()
	return a
}

func (a *API) ServeHTTP(w http.ResponseWriter, r *http.Request) { a.mux.ServeHTTP(w, r) }

// access says whether an endpoint needs an authenticated user (Java's @PermitAll).
type access int

const (
	authenticated access = iota
	public
)

// request is what a handler gets besides the ResponseWriter.
type request struct {
	*http.Request
	user    *model.User // nil when not authenticated
	session *session    // the session from the cookie, if any
	// tokenExpiration is set when the user authenticated with a token.
	tokenExpiration time.Time
}

func (r *request) userID() int64 {
	if r.user == nil {
		return 0
	}
	return r.user.ID
}

type handlerFunc func(w http.ResponseWriter, r *request) error

// handle registers an endpoint. Every response gets the CORS headers, and handler errors
// are turned into Java's status codes.
func (a *API) handle(pattern string, acc access, h handlerFunc) {
	a.mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		a.cors(w, r)
		// Jersey matches Accept against @Produces(JSON) before any filter runs.
		if !acceptsJSON(r.Header.Get("Accept")) {
			a.fail(w, r, errStatus(http.StatusNotAcceptable, "Not Acceptable"))
			return
		}
		req := &request{Request: r}
		if err := a.authenticate(req, acc); err != nil {
			a.fail(w, r, err)
			return
		}
		if err := h(w, req); err != nil {
			a.fail(w, r, err)
		}
	})
}

// acceptsJSON reports whether an Accept header allows application/json. An absent or
// unparseable header accepts anything, as in Jersey.
func acceptsJSON(accept string) bool { return accepts(accept, "application", "json") }

// accepts reports whether an Accept header allows type/subtype.
func accepts(accept, typ, sub string) bool {
	if strings.TrimSpace(accept) == "" {
		return true
	}
	for _, part := range strings.Split(accept, ",") {
		mt, params, err := mime.ParseMediaType(strings.TrimSpace(part))
		if err != nil {
			continue
		}
		if q, ok := params["q"]; ok && strings.Trim(q, "0.") == "" {
			continue // q=0: explicitly not acceptable
		}
		if mt == "*/*" || mt == typ+"/*" || mt == typ+"/"+sub {
			return true
		}
	}
	return false
}

// handleText registers a public text/plain endpoint (HealthResource).
func (a *API) handleText(pattern string, h handlerFunc) {
	a.mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		a.cors(w, r)
		if !accepts(r.Header.Get("Accept"), "text", "plain") {
			a.fail(w, r, errStatus(http.StatusNotAcceptable, "Not Acceptable"))
			return
		}
		req := &request{Request: r}
		if err := a.authenticate(req, public); err != nil {
			a.fail(w, r, err)
			return
		}
		if err := h(w, req); err != nil {
			a.fail(w, r, err)
		}
	})
}

// httpError carries a status code (Java's WebApplicationException).
type httpError struct {
	status int
	msg    string
	header http.Header
	// fromResource marks errors raised inside an endpoint (e.g. its own 404), which Java
	// sends with the endpoint's media type.
	fromResource bool
	// contentType overrides the Content-Type of the error response.
	contentType string
}

func (e *httpError) Error() string { return e.msg }

func errStatus(status int, format string, args ...any) error {
	return &httpError{status: status, msg: fmt.Sprintf(format, args...)}
}

// errNotFound is a 404 returned by an endpoint (e.g. no session); unknown paths get
// errNoRoute.
var (
	errNotFound = &httpError{status: http.StatusNotFound, msg: "Not Found", fromResource: true}
	errNoRoute  = &httpError{status: http.StatusNotFound, msg: "Not Found"}
)

func (a *API) fail(w http.ResponseWriter, r *http.Request, err error) {
	status := http.StatusBadRequest // Java's ResourceErrorHandler for any other exception
	var he *httpError
	if errors.As(err, &he) {
		status = he.status
		for k, vs := range he.header {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
	}
	if status >= 500 || status == http.StatusBadRequest {
		slog.Warn("request failed", "method", r.Method, "path", r.URL.Path, "status", status, "err", err)
	}
	// Java labels error bodies with the endpoint's media type (application/json), except
	// where Jersey fails before reaching a resource method (no match, 406, 415), which have
	// none. The body is a short message either way (MIGRATION.md: no stack traces).
	switch {
	case he != nil && he.contentType != "":
		w.Header().Set("Content-Type", he.contentType)
	case status == http.StatusNotFound || status == http.StatusNotAcceptable ||
		status == http.StatusUnsupportedMediaType || status == http.StatusMethodNotAllowed:
		if he == nil || !he.fromResource {
			w.Header()["Content-Type"] = nil // also stops net/http from sniffing one
			break
		}
		fallthrough
	default:
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(status)
	fmt.Fprintln(w, err.Error())
}

// cors adds the headers of Java's CorsResponseFilter.
func (a *API) cors(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Access-Control-Allow-Headers", "origin, content-type, accept, authorization")
	h.Set("Access-Control-Allow-Credentials", "true")
	h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
	origin := r.Header.Get("Origin")
	switch {
	case origin == "":
		h.Set("Access-Control-Allow-Origin", "*")
	case a.opts.Origin == "" || a.opts.Origin == "*" || strings.Contains(a.opts.Origin, origin):
		h.Set("Access-Control-Allow-Origin", origin)
	}
}

func writeJSON(w http.ResponseWriter, status int, v interface{ MarshalJSON() ([]byte, error) }) error {
	body, err := v.MarshalJSON()
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, err = w.Write(body)
	return err
}

// form returns the url-encoded body, enforcing Jersey's @Consumes(FORM): any other
// content type is 415.
func form(r *request) (formValues, error) {
	mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mt != "application/x-www-form-urlencoded" {
		return nil, errStatus(http.StatusUnsupportedMediaType, "Unsupported Media Type")
	}
	if err := r.ParseForm(); err != nil {
		return nil, err
	}
	return formValues(r.PostForm), nil
}

// formValues gives @FormParam semantics: the first value, and nil when absent.
type formValues map[string][]string

func (f formValues) get(name string) *string {
	if v, ok := f[name]; ok && len(v) > 0 {
		return &v[0]
	}
	return nil
}

// remoteAddress is WebHelper.retrieveRemoteAddress: the first address of a proxy header,
// or the peer address formatted the way Java formats it.
func remoteAddress(r *http.Request) string {
	for _, h := range []string{"CF-Connecting-IP", "X-Real-IP", "X-Forwarded-For"} {
		if v := r.Header.Get(h); v != "" {
			if i := strings.Index(v, ","); i > 0 {
				return v[:i]
			}
			return v
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return javaHostAddress(host)
}

// javaHostAddress formats an IP like InetAddress.getHostAddress: IPv4 dotted, IPv6 as
// eight groups without zero compression (0:0:0:0:0:0:0:1).
func javaHostAddress(host string) string {
	ip := net.ParseIP(strings.Trim(host, "[]"))
	if ip == nil {
		return host
	}
	if v4 := ip.To4(); v4 != nil {
		return v4.String()
	}
	groups := make([]string, 8)
	for i := range groups {
		groups[i] = fmt.Sprintf("%x", uint16(ip[2*i])<<8|uint16(ip[2*i+1]))
	}
	return strings.Join(groups, ":")
}

// audit stores an action row (LogAction). Like Java, a failure is logged, not returned.
func (a *API) audit(ctx context.Context, r *http.Request, actionType string, userID int64, objectType string, objectID int64) {
	act := model.Action{
		ActionTime: model.NewTime(time.Now()),
		UserID:     userID,
		ActionType: actionType,
		ObjectID:   objectID,
	}
	addr := remoteAddress(r)
	act.Address = &addr
	if objectType != "" {
		act.ObjectType = &objectType
	}
	if err := store.Insert(ctx, a.st, &act); err != nil {
		slog.Warn("failed to store action", "type", actionType, "err", err)
	}
}
