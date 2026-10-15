package api

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/auth"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/store"
)

func (a *API) routes() {
	a.handle("GET /api/server", public, a.getServer)

	a.handle("GET /api/session", public, a.getSession)
	a.handle("POST /api/session", public, a.postSession)
	a.handle("DELETE /api/session", authenticated, a.deleteSession)
	a.handle("GET /api/session/{id}", authenticated, a.getSessionAs)
	a.handle("POST /api/session/token", authenticated, a.postToken)
	a.handle("POST /api/session/token/revoke", authenticated, a.revokeToken)
	a.handle("GET /api/session/openid/auth", public, openIDDisabled)
	a.handle("GET /api/session/openid/callback", public, openIDDisabled)

	// Anything else under /api is Jersey's 404, which still carries the CORS headers.
	a.mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		a.cors(w, r)
		a.fail(w, r, errNoRoute)
	})
}

// login opens a fresh session for user (SessionHelper.userLogin): the old session is
// invalidated, the new one remembers the user, the token expiration and the Origin.
func (a *API) login(w http.ResponseWriter, r *request, user *model.User, expiration time.Time) {
	a.sessions.invalidate(r.session)
	sess := a.sessions.getOrCreate(w, nil)
	sess.userID = user.ID
	sess.expiration = expiration
	sess.origin = r.Header.Get("Origin")
	r.session = sess
	a.audit(r.Context(), r.Request, "login", user.ID, "", 0)
}

// getSession returns the logged-in user. With ?token= it first logs in with the token
// and opens a session.
func (a *API) getSession(w http.ResponseWriter, r *request) error {
	if token, ok := r.URL.Query()["token"]; ok {
		user, expiration, err := a.loginToken(r.Context(), token[0])
		switch {
		case errors.Is(err, auth.ErrTokenInvalid), errors.Is(err, auth.ErrTokenEncoding), errors.Is(err, errLoginRejected):
			return errStatus(http.StatusUnauthorized, "%v", err)
		case err != nil:
			return err // malformed token or storage: 400
		case user == nil:
			return errors.New("token user not found") // NullPointerException in Java
		}
		a.login(w, r, user, expiration)
		return writeJSON(w, http.StatusOK, user)
	}
	if r.session != nil && r.session.userID != 0 && originValid(r.session, r.Request) {
		user, err := a.user(r.Context(), r.session.userID)
		if err != nil {
			return err
		}
		if user != nil {
			return writeJSON(w, http.StatusOK, user)
		}
	}
	return errNotFound
}

// postSession logs in with e-mail (or login) and password, and TOTP code when required.
func (a *API) postSession(w http.ResponseWriter, r *request) error {
	f, err := form(r)
	if err != nil {
		return err
	}
	email := f.get("email")
	if email == nil {
		return errors.New("email: null value") // NullPointerException in Java
	}
	var code *int
	if c := f.get("code"); c != nil {
		n, err := strconv.ParseInt(*c, 10, 32)
		if err != nil {
			return errStatus(http.StatusBadRequest, "code: %v", err)
		}
		v := int(n)
		code = &v
	}
	user, err := a.loginPassword(r.Context(), *email, f.get("password"), code)
	switch {
	case errors.Is(err, errCodeRequired):
		return &httpError{status: http.StatusUnauthorized, msg: "Code required", header: http.Header{"Www-Authenticate": {"TOTP"}}}
	case err != nil:
		return err // disabled, expired, wrong code: 400 here, as in Java
	case user == nil:
		a.audit(r.Context(), r.Request, "denied", 0, "", 0)
		return errStatus(http.StatusUnauthorized, "Unauthorized")
	}
	a.login(w, r, user, time.Time{})
	return writeJSON(w, http.StatusOK, user)
}

// deleteSession logs out: the session forgets the user but stays (Java removes the
// attribute from request.getSession(), creating a session if there was none).
func (a *API) deleteSession(w http.ResponseWriter, r *request) error {
	a.audit(r.Context(), r.Request, "logout", r.userID(), "", 0)
	sess := a.sessions.getOrCreate(w, r.session)
	sess.userID = 0
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// getSessionAs logs an administrator or manager in as another user.
func (a *API) getSessionAs(w http.ResponseWriter, r *request) error {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		return errNotFound // Jersey: unconvertible path parameter (the resource matched)
	}
	if err := a.checkUser(r, id); err != nil {
		return err
	}
	user, err := a.user(r.Context(), id)
	if err != nil {
		return err
	}
	if user == nil {
		// Java's userLogin has already replaced the session when it hits the
		// NullPointerException: the caller ends up logged out, with a new empty session.
		a.sessions.invalidate(r.session)
		a.sessions.getOrCreate(w, nil)
		return errors.New("user not found")
	}
	a.login(w, r, user, time.Time{})
	return writeJSON(w, http.StatusOK, user)
}

// checkUser is PermissionsService.checkUser: yourself, an administrator, or the manager
// of that user. A denial is a SecurityException, so 400.
func (a *API) checkUser(r *request, managedUserID int64) error {
	if r.user.ID == managedUserID || r.user.Administrator {
		return nil
	}
	if r.user.Manager() {
		ok, err := a.st.Manages(r.Context(), r.user.ID, managedUserID)
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
	}
	return errors.New("User access denied")
}

// postToken issues a bearer token for the current user. The token can't outlive the
// session's own token expiration. The response is the raw token, labelled JSON as Java does.
func (a *API) postToken(w http.ResponseWriter, r *request) error {
	f, err := form(r)
	if err != nil {
		return err
	}
	var expiration time.Time
	if v := f.get("expiration"); v != nil {
		t, err := time.Parse(time.RFC3339Nano, *v) // DateTimeFormatter.ISO_ZONED_DATE_TIME
		if err != nil {
			return errStatus(http.StatusBadRequest, "expiration: %v", err)
		}
		expiration = t
	}
	sess := a.sessions.getOrCreate(w, r.session)
	if !sess.expiration.IsZero() {
		if expiration.IsZero() {
			return errors.New("expiration: null value") // Date.before(null) in Java
		}
		if sess.expiration.Before(expiration) {
			expiration = sess.expiration
		}
	}
	token, data, err := a.tokens.Issue(r.Context(), r.userID(), expiration)
	if err != nil {
		return err
	}
	a.audit(r.Context(), r.Request, "create", r.userID(), "token", data.ID)
	w.Header().Set("Content-Type", "application/json")
	_, err = w.Write([]byte(token))
	return err
}

// revokeToken revokes a token signed by this server, whatever its owner or expiry.
func (a *API) revokeToken(w http.ResponseWriter, r *request) error {
	f, err := form(r)
	if err != nil {
		return err
	}
	token := f.get("token")
	if token == nil {
		return errors.New("token: null value")
	}
	data, err := a.tokens.Decode(r.Context(), *token)
	if err != nil {
		return err // 400 for every decoding failure here
	}
	if err := a.tokens.Revoke(r.Context(), data.ID); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func openIDDisabled(http.ResponseWriter, *request) error {
	return errors.New("OpenID not enabled") // UnsupportedOperationException: 400
}

// getServer is GET /api/server: the server row plus computed flags. Administrators also
// get the host's disk space; anonymous callers learn whether the server has no users yet.
func (a *API) getServer(w http.ResponseWriter, r *request) error {
	servers, err := store.All[model.Server](r.Context(), a.st)
	if err != nil {
		return err
	}
	if len(servers) == 0 {
		return errors.New("tc_servers is empty")
	}
	info := model.ServerInfo{
		Server:          servers[0],
		EmailEnabled:    a.opts.EmailEnabled,
		GeocoderEnabled: a.opts.GeocoderEnabled,
		TextEnabled:     a.opts.TextEnabled,
		Version:         Version,
	}
	if r.user != nil {
		if r.user.Administrator {
			info.StorageSpace = a.storageSpace()
		}
	} else {
		if info.NewServer, err = a.st.NoUsers(r.Context()); err != nil {
			return err
		}
	}
	return writeJSON(w, http.StatusOK, info)
}
