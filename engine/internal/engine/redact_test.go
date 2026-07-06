package engine

import "testing"

// TestRedactorString exercises the secret-scrubbing invariant (hard rule 6):
// every secret value — and its JSON-escaped form — must be replaced before a
// task's data is persisted or logged, longest-first so overlapping secrets
// leave no fragments, and short values (<4 chars) are left alone to avoid
// shredding unrelated text.
func TestRedactorString(t *testing.T) {
	cases := []struct {
		name    string
		secrets map[string]string
		in      string
		want    string
	}{
		{
			name:    "basic replacement",
			secrets: map[string]string{"tok": "supersecret"},
			in:      "auth=supersecret done",
			want:    "auth=[redacted] done",
		},
		{
			name:    "value appears multiple times",
			secrets: map[string]string{"tok": "supersecret"},
			in:      "supersecret and supersecret",
			want:    "[redacted] and [redacted]",
		},
		{
			// A secret containing a quote is stored both raw and JSON-escaped,
			// so it matches inside a marshaled JSON payload too.
			name:    "json-escaped quote form matches in serialized payload",
			secrets: map[string]string{"tok": `ab"cd`},
			in:      `{"k":"ab\"cd"}`,
			want:    `{"k":"[redacted]"}`,
		},
		{
			// The raw form is still scrubbed when the secret appears unescaped.
			name:    "raw quote form matches in plain text",
			secrets: map[string]string{"tok": `ab"cd`},
			in:      `value=ab"cd`,
			want:    `value=[redacted]`,
		},
		{
			// Newline secret: the escaped `\n` sequence is scrubbed from JSON.
			name:    "json-escaped newline form matches",
			secrets: map[string]string{"tok": "line1\nline2"},
			in:      `{"k":"line1\nline2"}`,
			want:    `{"k":"[redacted]"}`,
		},
		{
			// "abcd" is a prefix of "abcdef"; longest-first means the full value
			// wins so no "ef" fragment survives (shortest-first would leave "ef").
			name:    "overlapping secrets replaced longest-first",
			secrets: map[string]string{"long": "abcdef", "short": "abcd"},
			in:      "abcdef",
			want:    "[redacted]",
		},
		{
			// The shorter secret still matches where the longer one does not.
			name:    "shorter secret still matches on its own",
			secrets: map[string]string{"long": "abcdef", "short": "abcd"},
			in:      "abcdxyz",
			want:    "[redacted]xyz",
		},
		{
			name:    "values under 4 chars are not redacted",
			secrets: map[string]string{"tiny": "abc", "two": "xy"},
			in:      "abc xy",
			want:    "abc xy",
		},
		{
			name:    "4-char boundary value is redacted",
			secrets: map[string]string{"edge": "abcd"},
			in:      "z abcd z",
			want:    "z [redacted] z",
		},
		{
			name:    "no secrets is a passthrough",
			secrets: map[string]string{},
			in:      "nothing to scrub",
			want:    "nothing to scrub",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRedactor(tc.secrets)
			if got := r.String(tc.in); got != tc.want {
				t.Fatalf("String(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestRedactorJSON(t *testing.T) {
	r := newRedactor(map[string]string{"tok": "supersecret"})
	got := r.JSON([]byte(`{"key":"supersecret"}`))
	want := `{"key":"[redacted]"}`
	if string(got) != want {
		t.Fatalf("JSON = %q, want %q", got, want)
	}
	// Empty input is returned as-is.
	if got := r.JSON([]byte{}); len(got) != 0 {
		t.Fatalf("JSON(empty) = %q, want empty", got)
	}
	if got := r.JSON(nil); got != nil {
		t.Fatalf("JSON(nil) = %q, want nil", got)
	}
}

// TestNilRedactorSafety proves the nil-safe guarantee the engine relies on: a
// taskRef with no redactor (secrets never loaded) can still call String/JSON.
func TestNilRedactorSafety(t *testing.T) {
	var r *redactor
	if got := r.String("hello world"); got != "hello world" {
		t.Fatalf("nil String = %q, want passthrough", got)
	}
	if got := r.JSON([]byte("hello")); string(got) != "hello" {
		t.Fatalf("nil JSON = %q, want passthrough", got)
	}
	if got := r.JSON(nil); got != nil {
		t.Fatalf("nil JSON(nil) = %q, want nil", got)
	}
}
