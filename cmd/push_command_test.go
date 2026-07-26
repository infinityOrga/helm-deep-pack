package cmd

import "testing"

func TestNewPushState_Defaults(t *testing.T) {
	state := newPushState()

	if state.Concurrency != defaultPushConcurrency {
		t.Fatalf("expected default concurrency %d, got %d", defaultPushConcurrency, state.Concurrency)
	}
	if state.Registry != "" || state.InputDir != "" || state.All || state.AllowHTTP || state.Verbose {
		t.Fatalf("expected zero-value push state fields except concurrency, got %#v", state)
	}
}

func TestNewPushCommand_FlagContract(t *testing.T) {
	state := newPushState()
	cmd := newPushCommand("push_images [REGISTRY]", &state)

	flags := []string{"input-dir", "concurrency", "all", "allow-insecure-http", "verbose"}
	for _, flag := range flags {
		AssertFlagExists(t, cmd, flag)
	}

	tests := map[string]string{
		"input-dir":           "i",
		"concurrency":         "c",
		"all":                 "a",
		"allow-insecure-http": "k",
		"verbose":             "V",
	}
	for flagName, shorthand := range tests {
		AssertFlagShorthand(t, cmd, flagName, shorthand)
	}

	defaults := map[string]string{
		"input-dir":           "",
		"concurrency":         "4",
		"all":                 "false",
		"allow-insecure-http": "false",
		"verbose":             "false",
	}
	for flagName, expectedDefault := range defaults {
		AssertFlagDefault(t, cmd, flagName, expectedDefault)
	}
}
