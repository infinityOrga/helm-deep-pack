package pull

import (
	"bytes"
	"log"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestWithLintLogSerializesConcurrentRenders(t *testing.T) {
	previousWriter := log.Writer()
	var sentinel bytes.Buffer
	log.SetOutput(&sentinel)
	defer log.SetOutput(previousWriter)

	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseFirst) }) }
	secondEntered := make(chan struct{})
	results := make(chan lintLogResult, 2)

	go func() {
		_, warnings, err := withLintLog(func() (map[string]string, error) {
			close(firstEntered)
			<-releaseFirst
			log.Print("[INFO] first lint warning")
			return nil, nil
		})
		results <- lintLogResult{warnings: warnings, err: err}
	}()
	<-firstEntered
	defer release()

	attemptedSecond := make(chan struct{})
	go func() {
		close(attemptedSecond)
		_, warnings, err := withLintLog(func() (map[string]string, error) {
			close(secondEntered)
			log.Print("[INFO] second lint warning")
			return nil, nil
		})
		results <- lintLogResult{warnings: warnings, err: err}
	}()
	<-attemptedSecond

	select {
	case <-secondEntered:
		t.Fatal("second lint render entered before the first render released the logger")
	case <-time.After(25 * time.Millisecond):
	}
	release()
	first := <-results
	second := <-results
	for _, result := range []lintLogResult{first, second} {
		if result.err != nil {
			t.Fatalf("withLintLog() error = %v", result.err)
		}
	}
	if !containsWarning(first.warnings, "first lint warning") && !containsWarning(second.warnings, "first lint warning") {
		t.Fatalf("warnings = %v and %v, want first lint warning", first.warnings, second.warnings)
	}
	if !containsWarning(first.warnings, "second lint warning") && !containsWarning(second.warnings, "second lint warning") {
		t.Fatalf("warnings = %v and %v, want second lint warning", first.warnings, second.warnings)
	}
	if log.Writer() != &sentinel {
		t.Fatal("withLintLog() did not restore the logger writer")
	}
}

type lintLogResult struct {
	warnings []string
	err      error
}

func containsWarning(warnings []string, want string) bool {
	for _, warning := range warnings {
		if strings.Contains(warning, want) {
			return true
		}
	}
	return false
}
