package crypto

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	argonTime    = 1
	argonMemory  = 64 * 1024
	argonThreads = 4
	argonKeyLen  = 32
	argonSaltLen = 16
)

var ErrInvalidHash = errors.New("invalid password hash format")

// dummyHash is a valid Argon2id encoding of an unguessable value. It exists so
// that a login attempt for a missing (or passwordless) account performs the same
// Argon2 work as a real one, closing the timing side-channel that would otherwise
// reveal whether an email is registered.
var dummyHash = mustHash("ctech-account-dummy-password-never-matches")

func mustHash(s string) string {
	h, err := HashPassword(s)
	if err != nil {
		panic("crypto: hashing dummy password: " + err.Error())
	}
	return h
}

// VerifyDummyPassword burns the same CPU as VerifyPassword against a real hash.
// Always returns false. Call it on the "user not found" path.
func VerifyDummyPassword(password string) {
	_, _ = VerifyPassword(password, dummyHash)
}

func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generating salt: %w", err)
	}

	hash := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)

	b64Salt := base64.RawStdEncoding.EncodeToString(salt)
	b64Hash := base64.RawStdEncoding.EncodeToString(hash)

	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		argonMemory, argonTime, argonThreads, b64Salt, b64Hash), nil
}

func VerifyPassword(password, encoded string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 {
		return false, ErrInvalidHash
	}

	var memory, time uint32
	var threads uint8
	_, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &threads)
	if err != nil {
		return false, ErrInvalidHash
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, ErrInvalidHash
	}

	storedHash, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, ErrInvalidHash
	}

	hash := argon2.IDKey([]byte(password), salt, time, memory, threads, uint32(len(storedHash)))

	return subtle.ConstantTimeCompare(hash, storedHash) == 1, nil
}
