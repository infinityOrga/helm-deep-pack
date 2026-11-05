package terminal

import "testing"

func TestTruncateRuneSafe(t *testing.T) {
	if got := Truncate("a→b❯c", 3); got != "a→b" {
		t.Fatalf("Truncate() = %q", got)
	}
	if got := Truncate("abc", 0); got != "abc" {
		t.Fatalf("Truncate() width 0 = %q", got)
	}
}
