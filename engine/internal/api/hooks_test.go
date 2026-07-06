package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

func sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func TestVerifyHMAC(t *testing.T) {
	secret := "test-secret-123"
	body := []byte(`{"hello":"world"}`)
	good := sign(secret, body)

	if !verifyHMAC(secret, body, good) {
		t.Fatalf("valid signature rejected")
	}
	// Wrong signature.
	if verifyHMAC(secret, body, sign("other-secret", body)) {
		t.Fatalf("signature under wrong secret accepted")
	}
	// Tampered body.
	if verifyHMAC(secret, []byte(`{"hello":"mars"}`), good) {
		t.Fatalf("signature for different body accepted")
	}
	// Absent signature.
	if verifyHMAC(secret, body, "") {
		t.Fatalf("empty signature accepted")
	}
	// Non-hex garbage.
	if verifyHMAC(secret, body, "not-hex") {
		t.Fatalf("malformed signature accepted")
	}
}

func TestEngineRunOptsIdempotencyPrefix(t *testing.T) {
	tid := uuid.New()
	// No header → no idempotency key, but trigger id is always set.
	opts := engineRunOpts(tid, "")
	if opts.IdempotencyKey != "" {
		t.Fatalf("empty header should yield empty key, got %q", opts.IdempotencyKey)
	}
	if opts.TriggerID == nil || *opts.TriggerID != tid {
		t.Fatalf("trigger id not carried")
	}
	// Header → key namespaced by trigger (and "hook:" so it can't collide with
	// cron: keys), so distinct triggers sharing a caller key stay distinct.
	opts = engineRunOpts(tid, "abc-123")
	want := "hook:" + tid.String() + ":abc-123"
	if opts.IdempotencyKey != want {
		t.Fatalf("key = %q, want %q", opts.IdempotencyKey, want)
	}
}

func TestValidateTriggerConfig(t *testing.T) {
	// Schedule: valid cron normalizes to {"cron": <expr>}.
	out, err := validateTriggerConfig("schedule", json.RawMessage(`{"cron":"*/5 * * * *"}`))
	if err != nil {
		t.Fatalf("valid schedule rejected: %v", err)
	}
	var sc struct {
		Cron string `json:"cron"`
	}
	if err := json.Unmarshal(out, &sc); err != nil || sc.Cron != "*/5 * * * *" {
		t.Fatalf("normalized schedule = %s (%v)", out, err)
	}

	// Schedule: 6-field (seconds) is rejected by the standard parser.
	if _, err := validateTriggerConfig("schedule", json.RawMessage(`{"cron":"* * * * * *"}`)); err == nil {
		t.Fatalf("6-field cron should be rejected")
	}
	// Schedule: empty cron rejected.
	if _, err := validateTriggerConfig("schedule", json.RawMessage(`{"cron":""}`)); err == nil {
		t.Fatalf("empty cron should be rejected")
	}

	// Webhook: valid path, optional secret carried through.
	out, err = validateTriggerConfig("webhook", json.RawMessage(`{"path":"my-hook","secret":"s3cr3t"}`))
	if err != nil {
		t.Fatalf("valid webhook rejected: %v", err)
	}
	var wc struct {
		Path   string `json:"path"`
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal(out, &wc); err != nil || wc.Path != "my-hook" || wc.Secret != "s3cr3t" {
		t.Fatalf("normalized webhook = %s (%v)", out, err)
	}

	// Webhook: secret omitted → not present in normalized config.
	out, _ = validateTriggerConfig("webhook", json.RawMessage(`{"path":"nohmac"}`))
	if got := string(out); got != `{"path":"nohmac"}` {
		t.Fatalf("no-secret webhook config = %s", got)
	}

	// Webhook: invalid paths rejected.
	for _, bad := range []string{"", "Bad_Path", "-leading", "has space", "slash/path", "UPPER"} {
		if _, err := validateTriggerConfig("webhook", json.RawMessage(`{"path":"`+bad+`"}`)); err == nil {
			t.Fatalf("webhook path %q should be rejected", bad)
		}
	}

	// Unknown type rejected.
	if _, err := validateTriggerConfig("bogus", json.RawMessage(`{}`)); err == nil {
		t.Fatalf("unknown type should be rejected")
	}
}
