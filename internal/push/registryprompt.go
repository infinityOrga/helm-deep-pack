package push

import (
	"bufio"
	"errors"
	"fmt"
	"helm-deep-pack/internal/terminal"
	"io"
	"strings"

	"helm-deep-pack/internal/validation"
)

// errRegistryRequired indicates no usable registry was provided.
var errRegistryRequired = errors.New("registry argument is required")

// isInteractive reports whether prompting is safe on both streams.
// Kept as a var so tests can override terminal detection.
var isInteractive = func(in io.Reader, out io.Writer) bool {
	return in != nil && terminal.IsReader(in) && out != nil && terminal.IsWriter(out)
}

// promptForRegistry prompts for a registry only in interactive mode.
func promptForRegistry(in io.Reader, out io.Writer) (string, error) {
	if !isInteractive(in, out) {
		return "", errRegistryRequired
	}
	return readRegistryLoop(in, out)
}

// readRegistryLoop keeps prompting until a valid registry is entered.
// Empty input or EOF is treated as cancel.
func readRegistryLoop(in io.Reader, out io.Writer) (string, error) {
	reader := bufio.NewReader(in)
	for {
		if _, err := fmt.Fprint(out, "Target registry: "); err != nil {
			return "", fmt.Errorf("write registry prompt: %w", err)
		}

		line, readErr := reader.ReadString('\n')
		registry := strings.TrimSpace(line)

		if registry != "" {
			if valErr := validation.ValidateImageRegistryWithPath("registry argument", registry); valErr == nil {
				return registry, nil
			} else if _, err := fmt.Fprintf(out, "invalid registry %q: %v\n", registry, valErr); err != nil {
				return "", fmt.Errorf("write registry prompt: %w", err)
			}
		}

		// Stop on cancel or closed input.
		if readErr != nil || registry == "" {
			return "", errRegistryRequired
		}
	}
}
