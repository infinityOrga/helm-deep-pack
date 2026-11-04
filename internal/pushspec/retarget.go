package pushspec

import (
	"fmt"
	"strings"

	"github.com/google/go-containerregistry/pkg/name"
)

// Retarget returns spec with its target repository path replaced, keeping the tag or digest.
func Retarget(spec ArchiveSpec, repoPath string) (ArchiveSpec, error) {
	repoPath = strings.TrimSpace(repoPath)
	if repoPath == "" {
		return spec, fmt.Errorf("target path must not be empty")
	}
	if strings.ContainsAny(repoPath, ":@") {
		return spec, fmt.Errorf("target path %q must not contain a tag or digest", repoPath)
	}
	if first, _, _ := strings.Cut(repoPath, "/"); isRegistryHost(first) {
		return spec, fmt.Errorf("target path %q must not include a registry host", repoPath)
	}
	if _, err := name.NewRepository(repoPath); err != nil {
		return spec, fmt.Errorf("invalid target path %q: %w", repoPath, err)
	}

	spec.Target = repoPath + strings.TrimPrefix(spec.Target, TargetRepository(spec.Target))
	return spec, nil
}

// TargetRepository returns target without its tag or digest.
func TargetRepository(target string) string {
	if i := strings.Index(target, "@"); i >= 0 {
		return target[:i]
	}
	if i := strings.LastIndex(target, ":"); i > strings.LastIndex(target, "/") {
		return target[:i]
	}
	return target
}

// DuplicateTarget returns the first pair of specs that share the same Target.
func DuplicateTarget(specs []ArchiveSpec) (a, b ArchiveSpec, found bool) {
	seen := make(map[string]ArchiveSpec, len(specs))
	for _, spec := range specs {
		if prev, ok := seen[spec.Target]; ok {
			return prev, spec, true
		}
		seen[spec.Target] = spec
	}
	return a, b, false
}
