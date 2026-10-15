package auth

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"strings"
	"time"
)

// CheckTOTP verifies a time-based one-time code the way com.warrenstrange.googleauth does
// for Traccar: base32 secret, HMAC-SHA1, 6 digits, 30-second steps, and a window of one
// step before and after now.
func CheckTOTP(secret string, code int, now time.Time) bool {
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(strings.TrimRight(secret, "=")))
	if err != nil || code < 0 || code > 999999 {
		return false
	}
	step := now.Unix() / 30
	for _, s := range []int64{step - 1, step, step + 1} {
		if totp(key, s) == code {
			return true
		}
	}
	return false
}

// totp is RFC 6238 / RFC 4226 with SHA-1 and 6 digits.
func totp(key []byte, step int64) int {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step))
	mac := hmac.New(sha1.New, key)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	value := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff
	return int(value % 1_000_000)
}
