// Package auth implements the Java server's credentials so both servers accept each
// other's: PBKDF2 password hashes (tc_users.hashedpassword/salt) and signed bearer tokens
// (keys in tc_keystore, revocations in tc_revoked_tokens).
package auth

import (
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
)

// Parameters of org.traccar.helper.Hashing: PBKDF2-HMAC-SHA1, 1000 iterations, 24-byte
// salt and hash, both stored as lowercase hex. Java derives the key from the password's
// UTF-8 bytes.
const (
	iterations = 1000
	saltSize   = 24
	hashSize   = 24
)

// HashPassword returns a new random salt and the password's hash, hex-encoded as Java
// stores them.
func HashPassword(password string) (hash, salt string, err error) {
	s := make([]byte, saltSize)
	if _, err := rand.Read(s); err != nil {
		return "", "", err
	}
	h, err := pbkdf2.Key(sha1.New, password, s, iterations, hashSize)
	if err != nil {
		return "", "", fmt.Errorf("hash password: %w", err)
	}
	return hex.EncodeToString(h), hex.EncodeToString(s), nil
}

// CheckPassword reports whether password matches a stored hash and salt. Unparseable
// stored values never match.
func CheckPassword(password, hash, salt string) bool {
	want, err := hex.DecodeString(hash)
	if err != nil {
		return false
	}
	s, err := hex.DecodeString(salt)
	if err != nil {
		return false
	}
	got, err := pbkdf2.Key(sha1.New, password, s, iterations, hashSize)
	if err != nil {
		return false
	}
	return hmac.Equal(got, want)
}
