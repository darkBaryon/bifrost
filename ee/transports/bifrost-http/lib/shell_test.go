package lib

import (
	"strings"
	"testing"
)

func TestEEShellRewriter(t *testing.T) {
	out := string(EEShellRewriter(nil, []byte("<html><head><title>x</title></head><body></body></html>")))
	if !strings.Contains(out, eeShellMarker+"</head>") {
		t.Fatalf("marker not injected before </head>: %s", out)
	}
	if strings.Count(out, eeShellMarker) != 1 {
		t.Fatalf("marker injected more than once: %s", out)
	}
	raw := "no head here"
	if got := string(EEShellRewriter(nil, []byte(raw))); got != raw {
		t.Fatalf("data without </head> must be untouched, got %q", got)
	}
}
