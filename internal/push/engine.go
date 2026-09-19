package push

import (
	"context"
	"io"
	"net/http"
	"os"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/layout"
	"github.com/google/go-containerregistry/pkg/v1/remote"

	"helm-deep-pack/internal/pushspec"
)

// Engine owns the adapters used by the pull and push phases of image transfer.
// Keeping them on an instance makes each workflow independently testable.
type Engine struct {
	copyImageToRegistry   func(ctx context.Context, registry string, allowInsecureHTTP bool, layoutPath layout.Path, sourceImage, target, ociDigest string) error
	writeRemoteImage      func(ref name.Reference, img v1.Image, options ...remote.Option) error
	loadLayoutImage       func(layoutPath layout.Path, hash v1.Hash) (v1.Image, error)
	loadOCILayout         func(path string) (layout.Path, error)
	resolveExecutablePath func() (string, error)

	fetchRemoteImage  func(ref name.Reference, options ...remote.Option) (v1.Image, error)
	writeLayout       func(path string, index v1.ImageIndex) (layout.Path, error)
	fromLayoutPath    func(path string) (layout.Path, error)
	writeLayoutImage  func(path layout.Path, img v1.Image) error
	appendLayoutImage func(path layout.Path, img v1.Image) error

	probeClient         *http.Client
	isInteractive       func(in io.Reader, out io.Writer) bool
	promptForRegistry   func(in io.Reader, out io.Writer) (string, error)
	confirmInsecureHTTP func(in io.Reader, out io.Writer, registry string) (bool, error)
	selectImagesToPush  func(ctx context.Context, opts Options, registry string, specs []pushspec.ArchiveSpec) ([]pushspec.ArchiveSpec, bool, error)
}

// NewEngine returns an image-transfer engine with production adapters.
func NewEngine() Engine {
	e := Engine{
		writeRemoteImage:      remote.Write,
		loadLayoutImage:       func(path layout.Path, hash v1.Hash) (v1.Image, error) { return path.Image(hash) },
		loadOCILayout:         layout.FromPath,
		resolveExecutablePath: os.Executable,

		fetchRemoteImage:  remote.Image,
		writeLayout:       layout.Write,
		fromLayoutPath:    layout.FromPath,
		writeLayoutImage:  func(path layout.Path, img v1.Image) error { return path.WriteImage(img) },
		appendLayoutImage: func(path layout.Path, img v1.Image) error { return path.AppendImage(img) },

		probeClient:         newRegistryProbeClient(),
		isInteractive:       defaultIsInteractive,
		promptForRegistry:   readRegistryLoop,
		confirmInsecureHTTP: confirmInsecureHTTP,
		selectImagesToPush:  selectImagesToPush,
	}
	return e
}

// ArchiveImages archives images into an OCI layout.
func (e Engine) ArchiveImages(ctx context.Context, images []string, outputDir string, concurrency int, status ...io.Writer) ([]pushspec.ArchiveSpec, error) {
	specs, _, err := e.archiveImages(ctx, images, outputDir, concurrency, strictArchivePolicy{}, status...)
	return specs, err
}

// ArchiveImagesBestEffort archives every valid image it can fetch and returns
// per-image failures separately.
func (e Engine) ArchiveImagesBestEffort(ctx context.Context, images []string, outputDir string, concurrency int, status ...io.Writer) ([]pushspec.ArchiveSpec, []ArchiveFailure, error) {
	return e.archiveImages(ctx, images, outputDir, concurrency, bestEffortArchivePolicy{}, status...)
}

// PushImages uploads the staged images to a registry.
func (e Engine) PushImages(ctx context.Context, opts Options, status ...io.Writer) error {
	probeClient := e.probeClient
	if probeClient == nil {
		probeClient = newRegistryProbeClient()
	}
	return e.pushImages(ctx, opts, probeClient, status...)
}

// ArchiveImages is the default image archiving entrypoint.
func ArchiveImages(ctx context.Context, images []string, outputDir string, concurrency int, status ...io.Writer) ([]pushspec.ArchiveSpec, error) {
	return NewEngine().ArchiveImages(ctx, images, outputDir, concurrency, status...)
}

// ArchiveImagesBestEffort is the default best-effort image archiving entrypoint.
func ArchiveImagesBestEffort(ctx context.Context, images []string, outputDir string, concurrency int, status ...io.Writer) ([]pushspec.ArchiveSpec, []ArchiveFailure, error) {
	return NewEngine().ArchiveImagesBestEffort(ctx, images, outputDir, concurrency, status...)
}

// PushImages is the default image pushing entrypoint.
func PushImages(ctx context.Context, opts Options, status ...io.Writer) error {
	return NewEngine().PushImages(ctx, opts, status...)
}
