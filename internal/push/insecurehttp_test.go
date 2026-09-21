package push

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestPreflightRegistryReturnsPlainHTTPError(t *testing.T) {
	probeClient := withRegistryProbeClient(t, func(*http.Request) (*http.Response, error) {
		return nil, errors.New("Get \"https://registry.local:5000/v2/\": http: server gave HTTP response to HTTPS client")
	})

	err := preflightRegistry(context.Background(), "registry.local:5000", false, probeClient)
	if err == nil {
		t.Fatal("preflightRegistry() error = nil, want plain HTTP error")
	}
	if !strings.Contains(err.Error(), "--allow-insecure-http") {
		t.Fatalf("plainHTTPRegistryError message = %q, want --allow-insecure-http hint", err.Error())
	}
}

func TestConfirmInsecureHTTP(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"yes", "yes\n", true},
		{"y", "y\n", true},
		{"no", "no\n", false},
		{"n", "n\n", false},
		{"empty defaults no", "\n", false},
		{"eof defaults no", "", false},
		{"reprompt then yes", "maybe\ny\n", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := new(bytes.Buffer)
			got, err := confirmInsecureHTTP(strings.NewReader(tt.input), out, "registry.local:5000")
			if err != nil {
				t.Fatalf("confirmInsecureHTTP() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("confirmInsecureHTTP() = %v, want %v (out=%q)", got, tt.want, out.String())
			}
			if !strings.Contains(out.String(), "plain HTTP") {
				t.Fatalf("expected an HTTP warning, got %q", out.String())
			}
			if !strings.Contains(out.String(), "Continue over insecure HTTP?") {
				t.Fatalf("expected a yes/no prompt, got %q", out.String())
			}
		})
	}
}

// forceInteractive overrides the engine's terminal seam so the interactive
// confirm branch runs without a real terminal.
func forceInteractive(t *testing.T, engine *Engine) {
	t.Helper()
	engine.isInteractive = func(io.Reader, io.Writer) bool { return true }
}

func TestPushImagesInteractiveAcceptInsecureHTTPSwitchesToHTTP(t *testing.T) {
	var schemes []string
	probeClient := withRegistryProbeClient(t, func(req *http.Request) (*http.Response, error) {
		schemes = append(schemes, req.URL.Scheme)
		if req.URL.Scheme == "https" {
			return nil, errors.New("Get \"https://registry.local:5000/v2/\": http: server gave HTTP response to HTTPS client")
		}
		return registryProbeResponse(http.StatusOK, "application/json", ""), nil
	})
	engine := newPushTestEngine(probeClient)
	forceInteractive(t, &engine)

	out := new(bytes.Buffer)
	err := pushImagesWithEngineForTest(t, engine, Options{
		Registry:    "registry.local:5000",
		InputDir:    t.TempDir(),
		Concurrency: 1,
		All:         true,
		In:          strings.NewReader("y\n"),
		Out:         out,
	})

	if err == nil {
		t.Fatal("pushImages() error = nil, want a later manifest-read error after accepting HTTP")
	}
	if strings.Contains(err.Error(), "preflight") {
		t.Fatalf("pushImages() error = %v, want to have moved past preflight after HTTP confirm", err)
	}
	if len(schemes) < 2 || schemes[0] != "https" || schemes[len(schemes)-1] != "http" {
		t.Fatalf("probe schemes = %v, want an https attempt followed by an http retry", schemes)
	}
	if !strings.Contains(out.String(), "Continue over insecure HTTP?") {
		t.Fatalf("expected an HTTP confirm prompt, got %q", out.String())
	}
}

func TestPushImagesInteractiveDeclineInsecureHTTPReturnsError(t *testing.T) {
	probeClient := withRegistryProbeClient(t, func(*http.Request) (*http.Response, error) {
		return nil, errors.New("Get \"https://registry.local:5000/v2/\": http: server gave HTTP response to HTTPS client")
	})
	engine := newPushTestEngine(probeClient)
	forceInteractive(t, &engine)

	out := new(bytes.Buffer)
	err := pushImagesWithEngineForTest(t, engine, Options{
		Registry:    "registry.local:5000",
		InputDir:    t.TempDir(),
		Concurrency: 1,
		All:         true,
		In:          strings.NewReader("n\n"),
		Out:         out,
	})

	if err == nil {
		t.Fatal("pushImages() error = nil, want preflight error after declining HTTP")
	}
	if !strings.Contains(err.Error(), "--allow-insecure-http") {
		t.Fatalf("pushImages() error = %v, want --allow-insecure-http hint after decline", err)
	}
}
