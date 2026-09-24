package push

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"context"

	"github.com/google/go-containerregistry/pkg/registry"

	"github.com/google/go-containerregistry/pkg/v1/types"
	helmregistry "helm.sh/helm/v3/pkg/registry"
)

func TestChartArchiveBuildsHelmOCIArtifact(t *testing.T) {
	archive := chartArchiveFixture(t)
	metadata, err := chartArchiveMetadata(archive)
	if err != nil {
		t.Fatalf("chartArchiveMetadata() error = %v", err)
	}
	image, err := newHelmChartImage(archive, metadata)
	if err != nil {
		t.Fatalf("newHelmChartImage() error = %v", err)
	}
	manifest, err := image.Manifest()
	if err != nil {
		t.Fatalf("Manifest() error = %v", err)
	}
	if manifest.MediaType != types.OCIManifestSchema1 || manifest.Config.MediaType != helmChartConfigMediaType || len(manifest.Layers) != 1 || manifest.Layers[0].MediaType != helmChartLayerMediaType {
		t.Fatalf("chart artifact media types = %#v", manifest)
	}
	var config map[string]any
	configBytes, err := image.RawConfigFile()
	if err != nil {
		t.Fatalf("RawConfigFile() error = %v", err)
	}
	if err := json.Unmarshal(configBytes, &config); err != nil {
		t.Fatalf("unmarshal chart config: %v", err)
	}
	if config["name"] != "example" || config["version"] != "1.2.3" {
		t.Fatalf("chart artifact config = %#v, want example:1.2.3", config)
	}
}

func TestPushChartFromBundleIsPullableByHelm(t *testing.T) {
	archive := chartArchiveFixture(t)
	inputDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(inputDir, "example-1.2.3.tgz"), archive, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	server := httptest.NewServer(registry.New())
	t.Cleanup(server.Close)
	host := strings.TrimPrefix(server.URL, "http://")
	if err := pushChartFromBundle(context.Background(), host, true, inputDir, &bytes.Buffer{}); err != nil {
		t.Fatalf("pushChartFromBundle() error = %v", err)
	}
	client, err := helmregistry.NewClient(helmregistry.ClientOptPlainHTTP())
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	result, err := client.Pull("oci://" + host + "/example:1.2.3")
	if err != nil {
		t.Fatalf("Helm Pull() error = %v", err)
	}
	if result.Chart == nil || result.Chart.Meta == nil || result.Chart.Meta.Name != "example" || result.Chart.Meta.Version != "1.2.3" {
		t.Fatalf("Helm chart metadata = %#v", result.Chart)
	}
	if !bytes.Equal(result.Chart.Data, archive) {
		t.Fatal("Helm pulled chart bytes differ from the staged chart archive")
	}
}

func chartArchiveFixture(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	content := []byte("apiVersion: v2\nname: example\nversion: 1.2.3\ndescription: sample chart\n")
	if err := tw.WriteHeader(&tar.Header{Name: "example/Chart.yaml", Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatalf("WriteHeader() error = %v", err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close error = %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("gzip close error = %v", err)
	}
	return buf.Bytes()
}
