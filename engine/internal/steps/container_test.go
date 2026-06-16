package steps

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"
)

func TestToStrings(t *testing.T) {
	cases := []struct {
		in   any
		want []string
	}{
		{nil, nil},
		{"", nil},
		{"ffprobe", []string{"ffprobe"}},
		{`["a","b","c"]`, []string{"a", "b", "c"}},
		{[]any{"x", "y"}, []string{"x", "y"}},
		{[]string{"p", "q"}, []string{"p", "q"}},
	}
	for _, c := range cases {
		got, err := toStrings(c.in)
		if err != nil {
			t.Fatalf("toStrings(%v) error: %v", c.in, err)
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("toStrings(%v) = %v, want %v", c.in, got, c.want)
		}
	}
	if _, err := toStrings(`[not json`); err == nil {
		t.Error("expected error for malformed JSON array")
	}
}

func TestToStringMap(t *testing.T) {
	got, err := toStringMap(`{"A":"1","B":"2"}`)
	if err != nil {
		t.Fatal(err)
	}
	if got["A"] != "1" || got["B"] != "2" {
		t.Errorf("got %v", got)
	}
	if m, _ := toStringMap(nil); m != nil {
		t.Errorf("nil should map to nil, got %v", m)
	}
	if _, err := toStringMap("nope"); err == nil {
		t.Error("expected error for non-JSON")
	}
}

func TestClampInt(t *testing.T) {
	cases := []struct{ req, max, want int }{
		{0, 1024, 1024},   // unset → ceiling
		{512, 1024, 512},  // within → as-is
		{4096, 1024, 1024}, // over → ceiling
		{-5, 600, 600},    // negative → ceiling
	}
	for _, c := range cases {
		if got := clampInt(c.req, c.max); got != c.want {
			t.Errorf("clampInt(%d,%d) = %d, want %d", c.req, c.max, got, c.want)
		}
	}
}

func TestImageAllowed(t *testing.T) {
	if !imageAllowed(nil, "anything") {
		t.Error("empty allowlist should allow any image")
	}
	allow := []string{"ghcr.io/acme/", "docker.io/library/alpine"}
	if !imageAllowed(allow, "ghcr.io/acme/tool:1") {
		t.Error("prefix match should allow")
	}
	if imageAllowed(allow, "evil.io/x") {
		t.Error("non-matching image should be rejected")
	}
}

func TestParseStdout(t *testing.T) {
	if v := parseStdout([]byte(`{"n":42}`)); !reflect.DeepEqual(v, map[string]any{"n": float64(42)}) {
		t.Errorf("JSON stdout should parse, got %#v", v)
	}
	if v := parseStdout([]byte("plain text")); v != "plain text" {
		t.Errorf("non-JSON should pass through as string, got %v", v)
	}
	if v := parseStdout([]byte("  ")); v != "" {
		t.Errorf("blank should be empty string, got %q", v)
	}
}

func TestParseInputArtifacts(t *testing.T) {
	specs, err := parseInputArtifacts(`[{"from":"abc","as":"video.mp4"}]`)
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 1 || specs[0].From != "abc" || specs[0].As != "video.mp4" {
		t.Errorf("got %+v", specs)
	}
	if s, _ := parseInputArtifacts(nil); s != nil {
		t.Errorf("nil → nil, got %v", s)
	}
}

func TestPollBackoff(t *testing.T) {
	if d := pollBackoff(0); d != containerPollMin {
		t.Errorf("first backoff = %v, want %v", d, containerPollMin)
	}
	if d := pollBackoff(100); d != containerPollMax {
		t.Errorf("large poll count should cap at %v, got %v", containerPollMax, d)
	}
}

func TestSuspendedSentinel(t *testing.T) {
	// errors.As must find the sentinel through wrapping.
	wrapped := fmt.Errorf("context: %w", SuspendNow("poll", time.Now(), map[string]any{"k": "v"}))
	var s *Suspended
	if !errors.As(wrapped, &s) {
		t.Fatal("errors.As should unwrap *Suspended")
	}
	if s.Kind != "poll" || s.Payload["k"] != "v" {
		t.Errorf("unexpected suspension %+v", s)
	}
	// A plain error must not match.
	if errors.As(errors.New("nope"), &s) {
		t.Error("plain error should not match *Suspended")
	}
}
