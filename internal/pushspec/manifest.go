// Package pushspec defines the on-disk contract shared between the pull and
// push phases: the push manifest model, its file/layout naming, and the rules
// for deriving mirrored image references.
package pushspec

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type ArchiveSpec struct {
	Image         string   `json:"image"`
	Target        string   `json:"target"`
	OCIDigest     string   `json:"ociDigest"`
	Optional      bool     `json:"optional,omitempty"`
	OptionalFlags []string `json:"optionalFlags,omitempty"`
}

type PushManifest struct {
	LayoutDir string        `json:"layoutDir"`
	Images    []ArchiveSpec `json:"images"`
	// ChartTarget is the chart's repository path under the registry; empty means the chart name.
	ChartTarget string `json:"chartTarget,omitempty"`
}

func GeneratePushManifest(manifest PushManifest) (string, error) {
	for _, spec := range manifest.Images {
		if spec.OCIDigest == "" {
			return "", fmt.Errorf("missing oci digest for image %q", spec.Image)
		}
	}

	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal push manifest: %w", err)
	}
	return string(data) + "\n", nil
}

func PushManifestFileName() string {
	return "push_images.json"
}

func WritePushManifest(outputDir string, manifest PushManifest) error {
	if manifest.LayoutDir == "" {
		manifest.LayoutDir = OCILayoutDirName()
	}
	data, err := GeneratePushManifest(manifest)
	if err != nil {
		return fmt.Errorf("generate push manifest: %w", err)
	}

	manifestPath := filepath.Join(outputDir, PushManifestFileName())
	if err := os.WriteFile(manifestPath, []byte(data), 0o644); err != nil {
		return fmt.Errorf("write push manifest: %w", err)
	}

	return nil
}

func ReadPushManifest(inputDir string) (*PushManifest, error) {
	path := filepath.Join(inputDir, PushManifestFileName())
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read push manifest: %w", err)
	}

	var manifest PushManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("parse push manifest: %w", err)
	}

	return &manifest, nil
}

func OCILayoutDirName() string {
	return "oci-layout"
}
