package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/auth"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/store"
)

// Login failures with Java's semantics. errLoginRejected is Java's SecurityException:
// when the request is authenticating, it means "not authenticated" (401, or anonymous on
// a public endpoint).
var (
	errLoginRejected = errors.New("login rejected")
	errCodeRequired  = fmt.Errorf("%w: code not provided", errLoginRejected)
)

// serviceAccountID is ServiceAccountUser.ID.
const serviceAccountID int64 = 9000000000000000000

func serviceAccount() *model.User {
	return &model.User{ID: serviceAccountID, Name: "Service Account", Email: "none", Administrator: true}
}

// authenticate does what Java's SecurityRequestFilter does before every API call:
//   - with an Authorization header, it logs in with it ("Bearer <token>" or "Basic ...")
//     and ignores the session
//   - otherwise it uses the session cookie, if the session's origin matches
//
// Public endpoints run without a user when this fails; the others get 401. A malformed
// header is a 400, and some token and database errors are 500, both as in Java.
func (a *API) authenticate(r *request, acc access) error {
	if r.Method == http.MethodOptions {
		return nil
	}
	r.session = a.sessions.get(r.Request)
	var err error
	if header := r.Header.Get("Authorization"); header != "" {
		err = a.authenticateHeader(r, header)
	} else {
		err = a.authenticateSession(r)
	}
	if err != nil && !errors.Is(err, errLoginRejected) {
		return err
	}
	if err != nil {
		slog.Warn("authentication error", "err", err)
	}
	if r.user == nil && acc != public {
		e := &httpError{status: http.StatusUnauthorized, msg: "Unauthorized"}
		if strings.Contains(r.Header.Get("Accept"), "text/html") {
			e.header = http.Header{"Www-Authenticate": {`Basic realm="api"`}}
		}
		return e
	}
	return nil
}

func (a *API) authenticateHeader(r *request, header string) error {
	// Java: header.split(" ") then auth[0] and auth[1]; a missing part is an
	// ArrayIndexOutOfBoundsException (400).
	parts := javaSplit(header, " ")
	if len(parts) < 2 {
		return errStatus(http.StatusBadRequest, "malformed Authorization header")
	}
	switch strings.ToLower(parts[0]) {
	case "bearer":
		user, expiration, err := a.loginToken(r.Context(), parts[1])
		switch {
		case errors.Is(err, auth.ErrTokenEncoding):
			return errStatus(http.StatusInternalServerError, "%v", err) // wrapped in WebApplicationException
		case errors.Is(err, auth.ErrTokenMalformed):
			return errStatus(http.StatusBadRequest, "%v", err)
		case errors.Is(err, auth.ErrTokenInvalid):
			return fmt.Errorf("%w: %v", errLoginRejected, err)
		case errors.Is(err, errLoginRejected):
			return err
		case err != nil:
			return errStatus(http.StatusInternalServerError, "%v", err)
		}
		r.user, r.tokenExpiration = user, expiration
		return nil
	case "basic":
		decoded := javaBase64(parts[1])
		email, password, ok := strings.Cut(decoded, ":")
		if !ok {
			return errStatus(http.StatusBadRequest, "malformed Basic credentials")
		}
		user, err := a.loginPassword(r.Context(), email, &password, nil)
		switch {
		case errors.Is(err, errLoginRejected):
			return err
		case err != nil:
			return errStatus(http.StatusInternalServerError, "%v", err)
		}
		r.user = user // nil: wrong credentials, not authenticated
		return nil
	default:
		return fmt.Errorf("%w: unsupported authorization scheme", errLoginRejected)
	}
}

func (a *API) authenticateSession(r *request) error {
	sess := r.session
	if sess == nil || !originValid(sess, r.Request) {
		return nil
	}
	if !sess.expiration.IsZero() && sess.expiration.Before(time.Now()) {
		a.sessions.invalidate(sess)
		r.session = nil
		return nil
	}
	if sess.userID == 0 {
		return nil
	}
	user, err := a.user(r.Context(), sess.userID)
	if err != nil {
		// Java catches StorageException here and continues unauthenticated.
		return fmt.Errorf("%w: %v", errLoginRejected, err)
	}
	if user == nil {
		return nil
	}
	if err := checkEnabled(user); err != nil {
		return err
	}
	r.user = user
	r.tokenExpiration = sess.expiration
	return nil
}

// originValid is SessionHelper.isSessionOriginValid.
func originValid(sess *session, r *http.Request) bool {
	if sess.origin == "" {
		return true
	}
	origin := r.Header.Get("Origin")
	return origin == "" || origin == sess.origin
}

// user loads a user (PermissionsService.getUser); nil if it doesn't exist.
func (a *API) user(ctx context.Context, id int64) (*model.User, error) {
	if id == serviceAccountID {
		return serviceAccount(), nil
	}
	u, err := store.Get[model.User](ctx, a.st, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// checkEnabled is Disableable.checkDisabled.
func checkEnabled(u *model.User) error {
	if u.Disabled {
		return fmt.Errorf("%w: User is disabled", errLoginRejected)
	}
	if !u.ExpirationTime.IsZero() && time.Now().After(u.ExpirationTime.Time) {
		return fmt.Errorf("%w: User has expired", errLoginRejected)
	}
	return nil
}

// loginToken is LoginService.login(token): the service account token, or a verified
// bearer token whose user exists and is enabled.
func (a *API) loginToken(ctx context.Context, token string) (*model.User, time.Time, error) {
	if a.opts.ServiceAccountToken != "" && token == a.opts.ServiceAccountToken {
		return serviceAccount(), time.Time{}, nil
	}
	data, err := a.tokens.Verify(ctx, token)
	if err != nil {
		return nil, time.Time{}, err
	}
	user, err := a.user(ctx, data.UserID)
	if err != nil {
		return nil, time.Time{}, err
	}
	if user != nil {
		if err := checkEnabled(user); err != nil {
			return nil, time.Time{}, err
		}
	}
	return user, data.Expiration, nil
}

// loginPassword is LoginService.login(email, password, code). It returns (nil, nil) for
// wrong credentials. password nil is Java's null password, which fails with a
// NullPointerException (400) once a user is found.
func (a *API) loginPassword(ctx context.Context, email string, password *string, code *int) (*model.User, error) {
	user, err := a.st.UserByLogin(ctx, email)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if password == nil || user.HashedPassword == nil || user.Salt == nil {
		return nil, errors.New("password: null value")
	}
	if !auth.CheckPassword(*password, *user.HashedPassword, *user.Salt) {
		return nil, nil
	}
	if user.TotpKey != nil && *user.TotpKey != "" {
		if code == nil {
			return nil, errCodeRequired
		}
		if !auth.CheckTOTP(*user.TotpKey, *code, time.Now()) {
			return nil, fmt.Errorf("%w: User authorization failed", errLoginRejected)
		}
	}
	if err := checkEnabled(&user); err != nil {
		return nil, err
	}
	return &user, nil
}

// javaSplit is String.split with a literal separator: trailing empty strings removed.
func javaSplit(s, sep string) []string {
	parts := strings.Split(s, sep)
	for len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}

// javaBase64 decodes Basic credentials like commons-codec (lenient) and reads them as
// US-ASCII, as Java does (non-ASCII bytes become U+FFFD).
func javaBase64(s string) string {
	b := auth.DecodeBase64(s)
	var out strings.Builder
	for _, c := range b {
		if c < 0x80 {
			out.WriteByte(c)
		} else {
			out.WriteRune('�')
		}
	}
	return out.String()
}
