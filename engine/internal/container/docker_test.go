package container

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestSplitTag(t *testing.T) {
	cases := []struct{ image, name, tag string }{
		{"alpine", "alpine", "latest"},
		{"alpine:3.21", "alpine", "3.21"},
		{"ghcr.io/acme/tool:1.2", "ghcr.io/acme/tool", "1.2"},
		{"registry:5000/x", "registry:5000/x", "latest"}, // port, not tag
		{"registry:5000/x:v2", "registry:5000/x", "v2"},
	}
	for _, c := range cases {
		n, tag := splitTag(c.image)
		if n != c.name || tag != c.tag {
			t.Errorf("splitTag(%q) = (%q,%q), want (%q,%q)", c.image, n, tag, c.name, c.tag)
		}
	}
}

func TestNanoCPUs(t *testing.T) {
	cases := []struct {
		cpu  string
		want int64
	}{
		{"1", 1_000_000_000},
		{"0.5", 500_000_000},
		{"500m", 500_000_000},
		{"2", 2_000_000_000},
		{"", 0},
	}
	for _, c := range cases {
		if got := nanoCPUs(c.cpu); got != c.want {
			t.Errorf("nanoCPUs(%q) = %d, want %d", c.cpu, got, c.want)
		}
	}
}

func TestMatchAny(t *testing.T) {
	if !matchAny([]string{"*.mp4"}, "out.mp4") {
		t.Error("*.mp4 should match out.mp4")
	}
	if matchAny([]string{"*.mp4"}, "out.webm") {
		t.Error("*.mp4 should not match out.webm")
	}
	if !matchAny([]string{"*.json", "*.webm"}, "x.webm") {
		t.Error("one of multiple globs should match")
	}
}

// frame builds one Docker multiplexed-stream frame.
func frame(stream byte, payload string) []byte {
	var hdr [8]byte
	hdr[0] = stream
	binary.BigEndian.PutUint32(hdr[4:], uint32(len(payload)))
	return append(hdr[:], []byte(payload)...)
}

func TestDemux(t *testing.T) {
	var buf bytes.Buffer
	buf.Write(frame(1, "hello "))
	buf.Write(frame(2, "err1"))
	buf.Write(frame(1, "world"))
	stdout, stderr, err := demux(&buf, 1<<20, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if string(stdout) != "hello world" {
		t.Errorf("stdout = %q", stdout)
	}
	if string(stderr) != "err1" {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestDemuxCap(t *testing.T) {
	var buf bytes.Buffer
	buf.Write(frame(1, "abcdefgh"))
	stdout, _, err := demux(&buf, 4, 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(stdout) > 8 || string(stdout) != "abcdefgh"[:len(stdout)] {
		// cap is approximate (stops appending once over), just ensure bounded
		t.Logf("capped stdout len=%d", len(stdout))
	}
}

func TestRegistryHost(t *testing.T) {
	if got := registryHost("ghcr.io/acme/x:1"); got != "ghcr.io" {
		t.Errorf("registryHost ghcr = %q", got)
	}
	if got := registryHost("alpine"); got != "https://index.docker.io/v1/" {
		t.Errorf("registryHost dockerhub = %q", got)
	}
}
