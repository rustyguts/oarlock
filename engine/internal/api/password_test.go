package api

import (
	"strings"
	"testing"
)

func TestPasswordHashRoundTrip(t *testing.T) {
	hash, err := hashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$") {
		t.Fatalf("hash not PHC argon2id: %q", hash)
	}
	if !verifyPassword(hash, "correct horse battery staple") {
		t.Fatal("correct password rejected")
	}
	if verifyPassword(hash, "wrong password") {
		t.Fatal("wrong password accepted")
	}
	// Two hashes of the same password differ (random salt).
	hash2, _ := hashPassword("correct horse battery staple")
	if hash == hash2 {
		t.Fatal("salts must differ between hashes")
	}
	if !verifyPassword(hash2, "correct horse battery staple") {
		t.Fatal("second hash rejected its own password")
	}
}

func TestVerifyPasswordRejectsMalformed(t *testing.T) {
	for _, bad := range []string{
		"",
		"plaintext",
		"$argon2id$v=19$m=19456,t=2,p=1$notb64!!$notb64!!",
		"$bcrypt$whatever",
		"$argon2id$v=18$m=19456,t=2,p=1$AAAA$AAAA", // wrong version
	} {
		if verifyPassword(bad, "anything") {
			t.Fatalf("malformed hash %q accepted", bad)
		}
	}
}

func TestLoginLimiter(t *testing.T) {
	l := newLoginLimiter()
	key := "1.2.3.4|a@b"
	for i := 0; i < l.max; i++ {
		if !l.allowed(key) {
			t.Fatalf("attempt %d should be allowed", i)
		}
		l.fail(key)
	}
	if l.allowed(key) {
		t.Fatal("limiter should block after max failures")
	}
	l.reset(key)
	if !l.allowed(key) {
		t.Fatal("reset should clear the counter")
	}
	if !l.allowed("other|key") {
		t.Fatal("keys must be independent")
	}
}
