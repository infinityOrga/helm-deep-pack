package progress

import (
	"bytes"
	"helm-deep-pack/internal/terminal"
	"io"
	"strings"
	"testing"
	"time"
)

func TestProgressRendersPerImageBytesAndStages(t *testing.T) {
	orig := terminal.IsWriter
	terminal.IsWriter = func(io.Writer) bool { return true }
	defer func() { terminal.IsWriter = orig }()

	var buf bytes.Buffer
	p := New(&buf, "pulling", 2)
	p.Begin("docker.io/library/nginx:1.0")
	if !strings.Contains(buf.String(), "fetching") {
		t.Fatalf("expected fetching stage, got %q", buf.String())
	}
	p.Update("docker.io/library/nginx:1.0", 45*1024*1024, 120*1024*1024, "downloading")
	time.Sleep(90 * time.Millisecond)
	p.Update("docker.io/library/nginx:1.0", 45*1024*1024, 120*1024*1024, "downloading")
	out := buf.String()
	for _, want := range []string{"library/nginx:1.0", "45MB/120MB", "downloading", "pulling ["} {
		if !strings.Contains(out, want) {
			t.Fatalf("aggregate/per-image render missing %q, got %q", want, out)
		}
	}
	p.End("docker.io/library/nginx:1.0")
	if !strings.Contains(buf.String(), "1/2") {
		t.Fatalf("expected aggregate count 1/2, got %q", buf.String())
	}
}

func TestProgressMarksUnreadImagesCached(t *testing.T) {
	var buf bytes.Buffer
	p := New(&buf, "pulling", 1) // non-terminal: prints on End
	p.Begin("docker.io/library/nginx:1.0")
	p.Update("docker.io/library/nginx:1.0", 0, 1000, "fetching")
	p.End("docker.io/library/nginx:1.0")
	out := buf.String()
	if !strings.Contains(out, "cached") || !strings.Contains(out, "1000B/1000B") {
		t.Fatalf("expected cached full-size line, got %q", out)
	}
}

func TestNormalizeDisplayImageStripsDockerHubRegistry(t *testing.T) {
	tests := map[string]string{
		"docker.io/library/busybox:1.36":    "library/busybox:1.36",
		"index.docker.io/library/nginx:1.0": "library/nginx:1.0",
		"quay.io/example/app:v1":            "quay.io/example/app:v1",
	}

	for input, want := range tests {
		if got := NormalizeDisplayImage(input); got != want {
			t.Fatalf("NormalizeDisplayImage(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestHumanizeBytes(t *testing.T) {
	tests := map[int64]string{
		0:                      "0B",
		512:                    "512B",
		1024:                   "1KB",
		45 * 1024 * 1024:       "45MB",
		1288490189:             "1.2GB",
		2 * 1024 * 1024 * 1024: "2.0GB",
	}

	for input, want := range tests {
		if got := HumanizeBytes(input); got != want {
			t.Fatalf("HumanizeBytes(%d) = %q, want %q", input, got, want)
		}
	}
}
