package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/asn1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/store"
)

// Bearer tokens, as org.traccar.api.signature.TokenManager and CryptoManager make them:
//
//	base64url( [signature length: 1 byte][ECDSA P-256 SHA-256 signature, DER][payload] )
//	payload = {"i":<random 63-bit id>,"u":<user id>,"e":"<expiration>"}
//
// The key pair is the first row of tc_keystore, shared with Java, so tokens issued by
// either server verify on both. Like Java, the key pair is created on first use when the
// table is empty.

// Token errors. They are distinct because Java answers each with a different status
// depending on where the token was presented (see internal/api).
var (
	// ErrTokenMalformed: not even a signed frame. Java hits a RuntimeException.
	ErrTokenMalformed = errors.New("malformed token")
	// ErrTokenEncoding: the signature isn't valid DER. Java throws a
	// GeneralSecurityException.
	ErrTokenEncoding = errors.New("invalid signature encoding")
	// ErrTokenInvalid: wrong signature, expired or revoked. Java throws a
	// SecurityException.
	ErrTokenInvalid = errors.New("invalid token")
)

// TokenData is the signed payload.
type TokenData struct {
	ID         int64
	UserID     int64
	Expiration time.Time
}

// DefaultTokenLifetime applies when no expiration is requested (7 days, as in Java).
const DefaultTokenLifetime = 7 * 24 * time.Hour

// KeyStore is the storage Tokens needs (implemented by *store.Store).
type KeyStore interface {
	KeyPair(ctx context.Context) (public, private []byte, err error)
	InsertKeyPair(ctx context.Context, public, private []byte) error
	TokenRevoked(ctx context.Context, id int64) (bool, error)
	RevokeToken(ctx context.Context, id int64) error
}

// Tokens issues and checks bearer tokens.
type Tokens struct {
	st KeyStore

	mu  sync.Mutex
	key *ecdsa.PrivateKey
}

func NewTokens(st KeyStore) *Tokens { return &Tokens{st: st} }

func (t *Tokens) keyPair(ctx context.Context) (*ecdsa.PrivateKey, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.key != nil {
		return t.key, nil
	}
	_, private, err := t.st.KeyPair(ctx)
	switch {
	case err == nil:
		k, err := x509.ParsePKCS8PrivateKey(private)
		if err != nil {
			return nil, fmt.Errorf("tc_keystore private key: %w", err)
		}
		key, ok := k.(*ecdsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("tc_keystore private key is %T, want ECDSA", k)
		}
		t.key = key
	case errors.Is(err, store.ErrNotFound):
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return nil, err
		}
		public, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
		if err != nil {
			return nil, err
		}
		private, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			return nil, err
		}
		if err := t.st.InsertKeyPair(ctx, public, private); err != nil {
			return nil, err
		}
		t.key = key
	default:
		return nil, err
	}
	return t.key, nil
}

// Issue signs a new token for userID. A zero expiration means DefaultTokenLifetime.
func (t *Tokens) Issue(ctx context.Context, userID int64, expiration time.Time) (string, TokenData, error) {
	key, err := t.keyPair(ctx)
	if err != nil {
		return "", TokenData{}, err
	}
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", TokenData{}, err
	}
	data := TokenData{
		ID:     int64(binary.BigEndian.Uint64(b[:]) & (1<<63 - 1)), // random.nextLong() & Long.MAX_VALUE
		UserID: userID,
	}
	if expiration.IsZero() {
		expiration = time.Now().Add(DefaultTokenLifetime)
	}
	data.Expiration = model.NewTime(expiration).Time
	payload, err := json.Marshal(struct {
		I int64      `json:"i"`
		U int64      `json:"u"`
		E model.Time `json:"e"`
	}{data.ID, data.UserID, model.Time{Time: data.Expiration}})
	if err != nil {
		return "", TokenData{}, err
	}
	digest := sha256.Sum256(payload)
	sig, err := ecdsa.SignASN1(rand.Reader, key, digest[:])
	if err != nil {
		return "", TokenData{}, err
	}
	frame := append(append([]byte{byte(len(sig))}, sig...), payload...)
	return base64.RawURLEncoding.EncodeToString(frame), data, nil
}

// Decode checks the signature and returns the payload, without checking expiry or
// revocation (TokenManager.decodeToken, used to revoke).
func (t *Tokens) Decode(ctx context.Context, token string) (TokenData, error) {
	frame := DecodeBase64(token)
	if len(frame) == 0 {
		return TokenData{}, ErrTokenMalformed
	}
	n := int(int8(frame[0])) // Java reads the length as a signed byte
	if n < 0 || 1+n > len(frame) {
		return TokenData{}, ErrTokenMalformed
	}
	sig, payload := frame[1:1+n], frame[1+n:]
	key, err := t.keyPair(ctx)
	if err != nil {
		return TokenData{}, err
	}
	var parsed struct{ R, S *big.Int }
	if rest, err := asn1.Unmarshal(sig, &parsed); err != nil || len(rest) > 0 {
		return TokenData{}, ErrTokenEncoding
	}
	digest := sha256.Sum256(payload)
	if !ecdsa.VerifyASN1(&key.PublicKey, digest[:], sig) {
		return TokenData{}, fmt.Errorf("%w: signature", ErrTokenInvalid)
	}
	var p struct {
		I int64      `json:"i"`
		U int64      `json:"u"`
		E model.Time `json:"e"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return TokenData{}, fmt.Errorf("%w: payload: %v", ErrTokenMalformed, err)
	}
	return TokenData{ID: p.I, UserID: p.U, Expiration: p.E.Time}, nil
}

// Verify is Decode plus the expiry and revocation checks (TokenManager.verifyToken).
func (t *Tokens) Verify(ctx context.Context, token string) (TokenData, error) {
	data, err := t.Decode(ctx, token)
	if err != nil {
		return TokenData{}, err
	}
	if data.Expiration.Before(time.Now()) {
		return TokenData{}, fmt.Errorf("%w: expired", ErrTokenInvalid)
	}
	revoked, err := t.st.TokenRevoked(ctx, data.ID)
	if err != nil {
		return TokenData{}, err
	}
	if revoked {
		return TokenData{}, fmt.Errorf("%w: revoked", ErrTokenInvalid)
	}
	return data, nil
}

// Revoke records a token id as revoked.
func (t *Tokens) Revoke(ctx context.Context, id int64) error { return t.st.RevokeToken(ctx, id) }

// DecodeBase64 decodes like commons-codec's Base64.decodeBase64, which Java uses: both
// alphabets, padding and characters outside the alphabet ignored, a trailing partial
// group dropped.
func DecodeBase64(s string) []byte {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		case r == '+':
			b.WriteByte('-')
		case r == '/':
			b.WriteByte('_')
		}
	}
	clean := b.String()
	if len(clean)%4 == 1 {
		clean = clean[:len(clean)-1]
	}
	out, err := base64.RawURLEncoding.DecodeString(clean)
	if err != nil {
		return nil
	}
	return out
}
