package push

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/partial"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/static"
	"github.com/google/go-containerregistry/pkg/v1/types"
	"gopkg.in/yaml.v3"
)

const (
	helmChartConfigMediaType = types.MediaType("application/vnd.cncf.helm.config.v1+json")
	helmChartLayerMediaType  = types.MediaType("application/vnd.cncf.helm.chart.content.v1.tar+gzip")
	maxChartMetadataBytes    = 1 << 20
)

type chartBundle struct {
	name     string
	version  string
	tag      string
	data     []byte
	metadata map[string]any
}

func readChartBundle(inputDir string) (*chartBundle, error) {
	entries, err := os.ReadDir(inputDir)
	if err != nil {
		return nil, fmt.Errorf("read bundle directory: %w", err)
	}
	var archives []string
	for _, entry := range entries {
		if entry.IsDir() || !isChartArchive(entry.Name()) {
			continue
		}
		archives = append(archives, filepath.Join(inputDir, entry.Name()))
	}
	if len(archives) == 0 {
		return nil, nil
	}
	if len(archives) > 1 {
		return nil, fmt.Errorf("expected one chart archive in bundle, found %d", len(archives))
	}

	data, err := os.ReadFile(archives[0])
	if err != nil {
		return nil, fmt.Errorf("read chart archive: %w", err)
	}
	metadata, err := chartArchiveMetadata(data)
	if err != nil {
		return nil, fmt.Errorf("read chart metadata: %w", err)
	}
	nameValue, _ := metadata["name"].(string)
	versionValue, _ := metadata["version"].(string)
	if nameValue == "" || versionValue == "" {
		return nil, fmt.Errorf("chart archive metadata must include name and version")
	}

	return &chartBundle{
		name:     nameValue,
		version:  versionValue,
		tag:      strings.ReplaceAll(versionValue, "+", "_"),
		data:     data,
		metadata: metadata,
	}, nil
}

// Keep chart uploads on the push helper's existing OCI stack instead of adding
// the Helm registry client's extra dependencies.
func pushChartFromBundle(ctx context.Context, destination string, allowInsecureHTTP bool, inputDir string, status io.Writer) error {
	bundle, err := readChartBundle(inputDir)
	if err != nil {
		return err
	}
	if bundle == nil {
		return warnMissingChart(status)
	}
	return pushChartArtifact(ctx, destination, allowInsecureHTTP, bundle, status)
}

func warnMissingChart(status io.Writer) error {
	if status == nil {
		status = io.Discard
	}
	if _, err := fmt.Fprintln(status, "warning: no chart archive found in bundle; continuing with images"); err != nil {
		return fmt.Errorf("write missing-chart warning: %w", err)
	}
	return nil
}

func pushChartArtifact(ctx context.Context, destination string, allowInsecureHTTP bool, bundle *chartBundle, status io.Writer) error {
	if status == nil {
		status = io.Discard
	}
	ref := fmt.Sprintf("%s/%s:%s", strings.TrimRight(destination, "/"), bundle.name, bundle.tag)
	refOptions := []name.Option{}
	if allowInsecureHTTP {
		refOptions = append(refOptions, name.Insecure)
	}
	parsedRef, err := name.ParseReference(ref, refOptions...)
	if err != nil {
		return fmt.Errorf("parse chart destination %q: %w", ref, err)
	}
	artifact, err := newHelmChartImage(bundle.data, bundle.metadata)
	if err != nil {
		return fmt.Errorf("build chart artifact: %w", err)
	}
	if _, err := fmt.Fprintf(status, "uploading chart %s:%s to %s\n", bundle.name, bundle.version, parsedRef.Name()); err != nil {
		return fmt.Errorf("write chart upload status: %w", err)
	}
	if err := remote.Write(parsedRef, artifact, remote.WithContext(ctx), remote.WithAuthFromKeychain(authn.DefaultKeychain)); err != nil {
		return fmt.Errorf("upload chart artifact: %w", err)
	}
	if _, err := fmt.Fprintf(status, "uploaded chart %s:%s\n", bundle.name, bundle.version); err != nil {
		return fmt.Errorf("write chart completion status: %w", err)
	}
	return nil
}

func isChartArchive(name string) bool {
	name = strings.ToLower(name)
	return strings.HasSuffix(name, ".tgz") || strings.HasSuffix(name, ".tar.gz")
}

func chartArchiveMetadata(data []byte) (map[string]any, error) {
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("open gzip archive: %w", err)
	}
	defer zr.Close() //nolint:errcheck
	tr := tar.NewReader(zr)
	for {
		header, err := tr.Next()
		if err != nil {
			return nil, fmt.Errorf("find Chart.yaml: %w", err)
		}
		if path.Base(header.Name) != "Chart.yaml" || header.Typeflag != tar.TypeReg {
			continue
		}
		metadataBytes, err := io.ReadAll(io.LimitReader(tr, maxChartMetadataBytes+1))
		if err != nil {
			return nil, fmt.Errorf("read Chart.yaml: %w", err)
		}
		if len(metadataBytes) > maxChartMetadataBytes {
			return nil, fmt.Errorf("chart.yaml exceeds %d bytes", maxChartMetadataBytes)
		}
		var metadata map[string]any
		if err := yaml.Unmarshal(metadataBytes, &metadata); err != nil {
			return nil, fmt.Errorf("parse Chart.yaml: %w", err)
		}
		return metadata, nil
	}
}

type helmChartImage struct {
	config      []byte
	layer       v1.Layer
	annotations map[string]string
}

var _ v1.Image = (*helmChartImage)(nil)

func newHelmChartImage(chartArchive []byte, metadata map[string]any) (*helmChartImage, error) {
	config, err := json.Marshal(metadata)
	if err != nil {
		return nil, err
	}
	annotations := map[string]string{
		"org.opencontainers.image.title":   metadataString(metadata, "name"),
		"org.opencontainers.image.version": metadataString(metadata, "version"),
		"org.opencontainers.image.created": time.Now().UTC().Format(time.RFC3339),
	}
	for key, annotation := range map[string]string{
		"org.opencontainers.image.description": metadataString(metadata, "description"),
		"org.opencontainers.image.url":         metadataString(metadata, "home"),
	} {
		if annotation != "" {
			annotations[key] = annotation
		}
	}
	if sources, ok := metadata["sources"].([]any); ok && len(sources) > 0 {
		if source, ok := sources[0].(string); ok && source != "" {
			annotations["org.opencontainers.image.source"] = source
		}
	}
	if chartAnnotations, ok := metadata["annotations"].(map[string]any); ok {
		for key, value := range chartAnnotations {
			if !isGeneratedOCIAnnotation(key) {
				if text, ok := value.(string); ok {
					annotations[key] = text
				}
			}
		}
	}
	return &helmChartImage{
		config:      config,
		layer:       static.NewLayer(chartArchive, helmChartLayerMediaType),
		annotations: annotations,
	}, nil
}

func isGeneratedOCIAnnotation(key string) bool {
	switch key {
	case "org.opencontainers.image.title", "org.opencontainers.image.version", "org.opencontainers.image.created", "org.opencontainers.image.description", "org.opencontainers.image.url", "org.opencontainers.image.source":
		return true
	default:
		return false
	}
}

func metadataString(metadata map[string]any, key string) string {
	value, _ := metadata[key].(string)
	return value
}

func (i *helmChartImage) Layers() ([]v1.Layer, error)         { return []v1.Layer{i.layer}, nil }
func (i *helmChartImage) MediaType() (types.MediaType, error) { return types.OCIManifestSchema1, nil }
func (i *helmChartImage) Size() (int64, error)                { return partial.Size(i) }
func (i *helmChartImage) ConfigName() (v1.Hash, error)        { return partial.ConfigName(i) }
func (i *helmChartImage) ConfigFile() (*v1.ConfigFile, error) { return partial.ConfigFile(i) }
func (i *helmChartImage) RawConfigFile() ([]byte, error)      { return i.config, nil }
func (i *helmChartImage) Digest() (v1.Hash, error)            { return partial.Digest(i) }
func (i *helmChartImage) RawManifest() ([]byte, error)        { return partial.RawManifest(i) }

func (i *helmChartImage) Manifest() (*v1.Manifest, error) {
	configDigest, configSize, err := v1.SHA256(bytes.NewReader(i.config))
	if err != nil {
		return nil, err
	}
	layerDigest, err := i.layer.Digest()
	if err != nil {
		return nil, err
	}
	layerSize, err := i.layer.Size()
	if err != nil {
		return nil, err
	}
	return &v1.Manifest{
		SchemaVersion: 2,
		MediaType:     types.OCIManifestSchema1,
		Config: v1.Descriptor{
			MediaType: helmChartConfigMediaType,
			Size:      configSize,
			Digest:    configDigest,
		},
		Layers: []v1.Descriptor{{
			MediaType: helmChartLayerMediaType,
			Size:      layerSize,
			Digest:    layerDigest,
		}},
		Annotations: i.annotations,
	}, nil
}

func (i *helmChartImage) LayerByDigest(digest v1.Hash) (v1.Layer, error) {
	actual, err := i.layer.Digest()
	if err != nil {
		return nil, err
	}
	if actual != digest {
		return nil, fmt.Errorf("chart layer %s not found", digest)
	}
	return i.layer, nil
}

func (i *helmChartImage) LayerByDiffID(diffID v1.Hash) (v1.Layer, error) {
	actual, err := i.layer.DiffID()
	if err != nil {
		return nil, err
	}
	if actual != diffID {
		return nil, fmt.Errorf("chart layer %s not found", diffID)
	}
	return i.layer, nil
}
