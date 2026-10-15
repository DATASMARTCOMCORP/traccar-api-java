package auth

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/store"
)

// Vectors in testdata were made by the Java server's own classes (Hashing, TokenManager),
// see the header line of each file.

func TestCheckPasswordJavaVectors(t *testing.T) {
	f, err := os.Open("testdata/java_passwords.tsv")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	n := 0
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), "#") {
			continue
		}
		fields := strings.Split(scanner.Text(), "\t")
		password := strings.ReplaceAll(fields[0], `\t`, "\t")
		if !CheckPassword(password, fields[1], fields[2]) {
			t.Errorf("Java hash of %q not accepted", password)
		}
		if CheckPassword(password+"x", fields[1], fields[2]) {
			t.Errorf("wrong password accepted for %q", password)
		}
		n++
	}
	if n < 5 {
		t.Fatalf("only %d vectors", n)
	}
}

func TestHashPassword(t *testing.T) {
	hash, salt, err := HashPassword("çãõ€ 😀")
	if err != nil {
		t.Fatal(err)
	}
	if len(hash) != 48 || len(salt) != 48 || strings.ToLower(hash) != hash {
		t.Errorf("hash %q salt %q: want 48 lowercase hex chars each, as Java stores", hash, salt)
	}
	if !CheckPassword("çãõ€ 😀", hash, salt) || CheckPassword("other", hash, salt) {
		t.Error("round trip failed")
	}
	if CheckPassword("x", "zz", salt) || CheckPassword("x", hash, "") {
		t.Error("unparseable stored values must not match")
	}
}

// memKeys is an in-memory KeyStore.
type memKeys struct {
	public, private []byte
	revoked         map[int64]bool
}

func (m *memKeys) KeyPair(context.Context) ([]byte, []byte, error) {
	if m.private == nil {
		return nil, nil, store.ErrNotFound
	}
	return m.public, m.private, nil
}

func (m *memKeys) InsertKeyPair(_ context.Context, public, private []byte) error {
	m.public, m.private = public, private
	return nil
}

func (m *memKeys) TokenRevoked(_ context.Context, id int64) (bool, error) { return m.revoked[id], nil }

func (m *memKeys) RevokeToken(_ context.Context, id int64) error {
	if m.revoked[id] {
		return store.ErrDuplicate
	}
	m.revoked[id] = true
	return nil
}

func javaTokens(t *testing.T) (*memKeys, map[string][]string) {
	t.Helper()
	data, err := os.ReadFile("testdata/java_tokens.txt")
	if err != nil {
		t.Fatal(err)
	}
	keys := &memKeys{revoked: map[int64]bool{}}
	values := map[string][]string{}
	for line := range strings.Lines(string(data)) {
		if strings.HasPrefix(line, "#") {
			continue
		}
		name, rest, _ := strings.Cut(strings.TrimSpace(line), "=")
		values[name] = strings.Split(rest, "\t")
	}
	keys.public, _ = hex.DecodeString(values["public"][0])
	keys.private, _ = hex.DecodeString(values["private"][0])
	return keys, values
}

func TestJavaTokens(t *testing.T) {
	ctx := context.Background()
	keys, v := javaTokens(t)
	tokens := NewTokens(keys)

	valid := v["valid"][0]
	data, err := tokens.Verify(ctx, valid)
	if err != nil {
		t.Fatalf("Java token rejected: %v", err)
	}
	wantID, _ := strconv.ParseInt(strings.TrimPrefix(v["valid"][2], "id="), 10, 64)
	if data.UserID != 42 || data.ID != wantID || !data.Expiration.Equal(time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("payload = %+v, want user 42, id %d, 2100-01-01", data, wantID)
	}

	expired := v["expired"][0]
	if _, err := tokens.Verify(ctx, expired); !errors.Is(err, ErrTokenInvalid) {
		t.Errorf("expired token: %v", err)
	}
	if d, err := tokens.Decode(ctx, expired); err != nil || d.UserID != 7 ||
		d.Expiration.UnixMilli() != 946684800123 {
		t.Errorf("Decode ignores expiry: %+v, %v", d, err)
	}

	if err := tokens.Revoke(ctx, data.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := tokens.Verify(ctx, valid); !errors.Is(err, ErrTokenInvalid) {
		t.Errorf("revoked token: %v", err)
	}
	if err := tokens.Revoke(ctx, data.ID); !errors.Is(err, store.ErrDuplicate) {
		t.Errorf("revoking twice: %v", err)
	}
}

func TestIssueUsesJavaKeyAndFormat(t *testing.T) {
	ctx := context.Background()
	keys, _ := javaTokens(t)
	tokens := NewTokens(keys)
	exp := time.Date(2034, 6, 1, 12, 0, 0, 999_999_999, time.UTC)
	token, data, err := tokens.Issue(ctx, 5, exp)
	if err != nil {
		t.Fatal(err)
	}
	if data.ID < 0 || data.UserID != 5 || data.Expiration.UnixMilli() != exp.UnixMilli() {
		t.Errorf("data = %+v", data)
	}
	frame, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		t.Fatalf("token must be unpadded base64url like Java's: %v", err)
	}
	payload := string(frame[1+int(frame[0]):])
	want := `{"i":` + strconv.FormatInt(data.ID, 10) + `,"u":5,"e":"2034-06-01T12:00:00.999+00:00"}`
	if payload != want {
		t.Errorf("payload %s, want %s", payload, want)
	}
	if got, err := tokens.Verify(ctx, token); err != nil || got != data {
		t.Errorf("own token: %+v, %v", got, err)
	}
	if _, data2, _ := tokens.Issue(ctx, 5, time.Time{}); time.Until(data2.Expiration) < DefaultTokenLifetime-time.Minute {
		t.Errorf("default lifetime: %s", data2.Expiration)
	}
}

func TestTokenErrorClasses(t *testing.T) {
	ctx := context.Background()
	keys, v := javaTokens(t)
	tokens := NewTokens(keys)
	valid := v["valid"][0]
	raw, _ := base64.RawURLEncoding.DecodeString(valid)
	tampered := append([]byte{}, raw...)
	tampered[len(tampered)-2] ^= 1 // payload byte: signature no longer matches
	tests := []struct {
		token string
		want  error
	}{
		{"", ErrTokenMalformed},
		{"not-a-token", ErrTokenMalformed}, // length byte larger than the frame
		{"AAAA", ErrTokenEncoding},         // zero-length signature
		{"_wAA", ErrTokenMalformed},        // negative length byte
		{base64.RawURLEncoding.EncodeToString(tampered), ErrTokenInvalid},
		{strings.ReplaceAll(strings.ReplaceAll(valid, "-", "+"), "_", "/") + "==", nil}, // standard alphabet, padded: commons-codec accepts it
	}
	for _, tt := range tests {
		_, err := tokens.Decode(ctx, tt.token)
		if !errors.Is(err, tt.want) && !(tt.want == nil && err == nil) {
			t.Errorf("Decode(%.20q) = %v, want %v", tt.token, err, tt.want)
		}
	}
}

func TestKeyPairCreatedOnFirstUse(t *testing.T) {
	ctx := context.Background()
	keys := &memKeys{revoked: map[int64]bool{}}
	tokens := NewTokens(keys)
	token, _, err := tokens.Issue(ctx, 1, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if keys.private == nil || keys.public == nil {
		t.Fatal("key pair not stored")
	}
	// A second instance (another process) loads the stored pair and accepts the token.
	if _, err := NewTokens(keys).Verify(ctx, token); err != nil {
		t.Errorf("stored key pair: %v", err)
	}
}

func TestCheckTOTP(t *testing.T) {
	// RFC 6238 appendix B, SHA-1 secret "12345678901234567890", truncated to 6 digits.
	secret := "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
	for unix, code := range map[int64]int{59: 287082, 1111111109: 81804, 1234567890: 5924, 2000000000: 279037} {
		now := time.Unix(unix, 0)
		if !CheckTOTP(secret, code, now) {
			t.Errorf("t=%d: code %06d rejected", unix, code)
		}
		if CheckTOTP(secret, code, now.Add(2*time.Minute)) {
			t.Errorf("t=%d: code accepted two minutes later", unix)
		}
	}
	if !CheckTOTP(strings.ToLower(secret), 287082, time.Unix(59+30, 0)) {
		t.Error("one step of clock skew and lower-case secrets must be accepted")
	}
}
