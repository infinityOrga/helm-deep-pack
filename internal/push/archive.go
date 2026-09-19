package push

import (
	"context"
	"errors"
	"fmt"
	"helm-deep-pack/internal/progress"
	"helm-deep-pack/internal/pushspec"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/layout"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"golang.org/x/sync/errgroup"
)

type ArchiveFailure struct {
	Image string
	Err   error
}

func (f ArchiveFailure) Error() string {
	return fmt.Sprintf("archive %s: %v", f.Image, f.Err)
}

type archivePolicy interface {
	buildSpecs(images []string) ([]pushspec.ArchiveSpec, []ArchiveFailure, error)
	recordFailure(image string, err error) (ArchiveFailure, bool)
}

type strictArchivePolicy struct{}

func (strictArchivePolicy) buildSpecs(images []string) ([]pushspec.ArchiveSpec, []ArchiveFailure, error) {
	specs, err := pushspec.BuildSpecs(images)
	if err != nil {
		return nil, nil, fmt.Errorf("build archive specs: %w", err)
	}
	return specs, nil, nil
}

func (strictArchivePolicy) recordFailure(string, error) (ArchiveFailure, bool) {
	return ArchiveFailure{}, false
}

type bestEffortArchivePolicy struct{}

func (bestEffortArchivePolicy) buildSpecs(images []string) ([]pushspec.ArchiveSpec, []ArchiveFailure, error) {
	specs := make([]pushspec.ArchiveSpec, 0, len(images))
	failures := make([]ArchiveFailure, 0)
	for _, image := range images {
		built, err := pushspec.BuildSpecs([]string{image})
		if err != nil {
			failures = append(failures, ArchiveFailure{Image: image, Err: fmt.Errorf("build archive spec: %w", err)})
			continue
		}
		specs = append(specs, built[0])
	}
	return specs, failures, nil
}

func (bestEffortArchivePolicy) recordFailure(image string, err error) (ArchiveFailure, bool) {
	return ArchiveFailure{Image: image, Err: err}, true
}

func (e Engine) archiveImages(ctx context.Context, images []string, outputDir string, concurrency int, policy archivePolicy, status ...io.Writer) ([]pushspec.ArchiveSpec, []ArchiveFailure, error) {
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return nil, nil, fmt.Errorf("create output dir: %w", err)
	}

	specs, failures, err := policy.buildSpecs(images)
	if err != nil {
		return nil, nil, err
	}
	if len(specs) == 0 {
		return nil, failures, nil
	}
	progressTracker := progress.New(progress.StatusWriter(status...), "pulling", len(specs))
	defer progressTracker.Finish()

	layoutRoot := filepath.Join(outputDir, pushspec.OCILayoutDirName())
	layoutPath, createdLayout, err := e.openOrCreateLayout(layoutRoot)
	if err != nil {
		return nil, nil, fmt.Errorf("open or create oci layout: %w", err)
	}

	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(normalizeConcurrency(concurrency))

	var writeMu sync.Mutex
	var failureMu sync.Mutex
	for i := range specs {
		i := i
		group.Go(func() error {
			progressTracker.Begin(specs[i].Image)
			defer progressTracker.End(specs[i].Image)

			digest, copyErr := e.copyImageToLayoutUsingGoContainerRegistry(groupCtx, specs[i].Image, layoutPath, &writeMu, progressTracker)
			if copyErr != nil {
				failure, record := policy.recordFailure(specs[i].Image, copyErr)
				if record {
					failureMu.Lock()
					failures = append(failures, failure)
					failureMu.Unlock()
					return nil
				}
				return fmt.Errorf("archive %s: %w", specs[i].Image, copyErr)
			}
			specs[i].OCIDigest = digest
			return nil
		})
	}

	if err := group.Wait(); err != nil {
		if createdLayout {
			_ = os.RemoveAll(layoutRoot)
		}
		return nil, nil, fmt.Errorf("archive images: %w", err)
	}

	if len(failures) > 0 {
		successful := make([]pushspec.ArchiveSpec, 0, len(specs)-len(failures))
		failed := make(map[string]struct{}, len(failures))
		for _, failure := range failures {
			failed[failure.Image] = struct{}{}
		}
		for _, spec := range specs {
			if _, ok := failed[spec.Image]; !ok {
				successful = append(successful, spec)
			}
		}
		specs = successful
	}
	if len(specs) == 0 && createdLayout {
		_ = os.RemoveAll(layoutRoot)
	}

	return specs, failures, nil
}

func (e Engine) openOrCreateLayout(layoutRoot string) (layout.Path, bool, error) {
	info, err := os.Stat(layoutRoot)
	if err == nil {
		if !info.IsDir() {
			return "", false, fmt.Errorf("open oci image layout: %s is not a directory", layoutRoot)
		}
		path, openErr := e.fromLayoutPath(layoutRoot)
		if openErr != nil {
			return "", false, fmt.Errorf("open oci image layout: %w", openErr)
		}
		return path, false, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", false, fmt.Errorf("stat oci image layout: %w", err)
	}

	path, createErr := e.writeLayout(layoutRoot, empty.Index)
	if createErr != nil {
		return "", false, fmt.Errorf("create oci image layout: %w", createErr)
	}
	return path, true, nil
}

func (e Engine) copyImageToLayoutUsingGoContainerRegistry(ctx context.Context, image string, layoutPath layout.Path, writeMu *sync.Mutex, progressTracker *progress.Progress) (string, error) {
	ref, err := name.ParseReference(image)
	if err != nil {
		return "", fmt.Errorf("parse source image %q: %w", image, err)
	}

	img, err := e.fetchRemoteImage(ref, remote.WithContext(ctx))
	if err != nil {
		return "", fmt.Errorf("fetch source image %q: %w", image, err)
	}

	manifest, err := img.Manifest()
	if err != nil {
		return "", fmt.Errorf("read manifest for image %q: %w", image, err)
	}

	totalBytes := int64(0)
	for _, layer := range manifest.Layers {
		totalBytes += layer.Size
	}
	if progressTracker != nil {
		progressTracker.Update(image, 0, totalBytes, "fetching")
	}

	digest, err := img.Digest()
	if err != nil {
		return "", fmt.Errorf("resolve digest for image %q: %w", image, err)
	}

	if err := e.writeLayoutImage(layoutPath, newProgressImage(image, img, totalBytes, progressTracker)); err != nil {
		return "", fmt.Errorf("write image %q blobs to oci layout: %w", image, err)
	}

	writeMu.Lock()
	defer writeMu.Unlock()
	if err := e.appendLayoutImage(layoutPath, img); err != nil {
		return "", fmt.Errorf("append image %q to oci layout: %w", image, err)
	}

	return digest.String(), nil
}

type progressImage struct {
	v1.Image
	image    string
	total    int64
	progress *progress.Progress
	counter  *progressCounter
}

func newProgressImage(image string, img v1.Image, total int64, progressTracker *progress.Progress) v1.Image {
	if progressTracker == nil {
		return img
	}
	return progressImage{
		Image:    img,
		image:    image,
		total:    total,
		progress: progressTracker,
		counter:  &progressCounter{},
	}
}

func (p progressImage) Layers() ([]v1.Layer, error) {
	layers, err := p.Image.Layers()
	if err != nil {
		return nil, err
	}
	if p.progress == nil {
		return layers, nil
	}
	wrapped := make([]v1.Layer, 0, len(layers))
	for _, layer := range layers {
		wrapped = append(wrapped, progressLayer{
			Layer:    layer,
			image:    p.image,
			total:    p.total,
			progress: p.progress,
			counter:  p.counter,
		})
	}
	return wrapped, nil
}

func (p progressImage) RawConfigFile() ([]byte, error) {
	return p.Image.RawConfigFile()
}

type progressLayer struct {
	v1.Layer
	image    string
	total    int64
	progress *progress.Progress
	counter  *progressCounter
}

func (p progressLayer) Compressed() (io.ReadCloser, error) {
	rc, err := p.Layer.Compressed()
	if err != nil {
		return nil, err
	}
	if p.progress == nil {
		return rc, nil
	}
	return &progressReadCloser{
		ReadCloser: rc,
		image:      p.image,
		total:      p.total,
		progress:   p.progress,
		counter:    p.counter,
	}, nil
}

type progressCounter struct {
	mu       sync.Mutex
	complete int64
}

type progressReadCloser struct {
	io.ReadCloser
	image    string
	total    int64
	progress *progress.Progress
	counter  *progressCounter
	complete int64
}

func (p *progressReadCloser) Read(buf []byte) (int, error) {
	n, err := p.ReadCloser.Read(buf)
	if n > 0 && p.progress != nil && p.counter != nil {
		p.counter.mu.Lock()
		p.counter.complete += int64(n)
		p.complete = p.counter.complete
		complete := p.counter.complete
		p.counter.mu.Unlock()
		p.progress.Update(p.image, complete, p.total, "downloading")
	}
	return n, err
}

func normalizeConcurrency(value int) int {
	if value < 1 {
		return 1
	}
	return value
}
