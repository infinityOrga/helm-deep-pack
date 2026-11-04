package push

import (
	"context"
	"errors"
	"helm-deep-pack/internal/pushspec"
	"net/http"
	"strings"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
)

type targetStatus string

const (
	statusNewRepository targetStatus = "new repository"
	statusNewTag        targetStatus = "new tag"
	statusMirrored      targetStatus = "mirrored"
	statusTagExists     targetStatus = "tag exists"
	statusConflict      targetStatus = "conflict"
	statusMissing       targetStatus = "missing"
	statusUnknown       targetStatus = "unknown"
)

var errMissing = errors.New("not found")

type classifiedImage struct {
	Spec pushspec.ArchiveSpec
	classifiedTarget
}

type classifiedChart struct {
	Bundle *chartBundle
	classifiedTarget
}

type classifiedTarget struct {
	Status       targetStatus
	RemoteDigest string
	ProbeErr     error
}

type remoteHeadFn func(name.Reference, ...remote.Option) (*v1.Descriptor, error)

func classifyOne(ctx context.Context, registry string, allowInsecureHTTP bool, spec pushspec.ArchiveSpec, headFn remoteHeadFn) classifiedImage {
	return classifiedImage{
		Spec:             spec,
		classifiedTarget: classifyTarget(ctx, registry, spec.Target, allowInsecureHTTP, spec.OCIDigest, headFn),
	}
}

func classifyChartWithHead(ctx context.Context, registry string, allowInsecureHTTP bool, bundle *chartBundle, headFn remoteHeadFn) classifiedChart {
	return classifiedChart{
		Bundle:           bundle,
		classifiedTarget: classifyTarget(ctx, registry, bundle.repo()+":"+bundle.tag, allowInsecureHTTP, "", headFn),
	}
}

func classifyTarget(ctx context.Context, registry, target string, allowInsecureHTTP bool, expectedDigest string, headFn remoteHeadFn) classifiedTarget {
	refOpts := make([]name.Option, 0, 1)
	if allowInsecureHTTP {
		refOpts = append(refOpts, name.Insecure)
	}
	ref, err := name.ParseReference(strings.TrimRight(registry, "/")+"/"+target, refOpts...)
	if err != nil {
		return classifiedTarget{Status: statusUnknown, ProbeErr: err}
	}

	descriptor, err := headFn(ref, remote.WithContext(ctx), remote.WithAuthFromKeychain(authn.DefaultKeychain))
	if err != nil {
		if status, ok := missingTargetStatus(err); ok {
			return classifiedTarget{Status: status}
		}
		if errors.Is(err, errMissing) {
			return classifiedTarget{Status: statusMissing}
		}
		return classifiedTarget{Status: statusUnknown, ProbeErr: err}
	}

	remoteDigest := descriptor.Digest.String()
	if expectedDigest == "" {
		return classifiedTarget{Status: statusTagExists, RemoteDigest: remoteDigest}
	}
	if remoteDigest == expectedDigest {
		return classifiedTarget{Status: statusMirrored, RemoteDigest: remoteDigest}
	}
	return classifiedTarget{Status: statusConflict, RemoteDigest: remoteDigest}
}

// headOrGet retries a body-less HEAD 404 as a GET, whose error body carries the diagnostic code.
func headOrGet(ref name.Reference, opts ...remote.Option) (*v1.Descriptor, error) {
	desc, err := remote.Head(ref, opts...)
	var transportErr *transport.Error
	if err == nil || !errors.As(err, &transportErr) || transportErr.StatusCode != http.StatusNotFound {
		return desc, err
	}
	got, getErr := remote.Get(ref, opts...)
	if getErr == nil {
		return &got.Descriptor, nil
	}
	if _, ok := missingTargetStatus(getErr); ok || !errors.As(getErr, &transportErr) || transportErr.StatusCode != http.StatusNotFound {
		return nil, getErr
	}
	return nil, errMissing
}

func classifyImages(ctx context.Context, registry string, allowInsecureHTTP bool, specs []pushspec.ArchiveSpec) []classifiedImage {
	return classifyImagesWithHead(ctx, registry, allowInsecureHTTP, specs, headOrGet)
}

func classifyImagesWithHead(ctx context.Context, registry string, allowInsecureHTTP bool, specs []pushspec.ArchiveSpec, headFn remoteHeadFn) []classifiedImage {
	result := make([]classifiedImage, len(specs))
	for i, spec := range specs {
		result[i] = classifyOne(ctx, registry, allowInsecureHTTP, spec, headFn)
	}
	return result
}

func missingTargetStatus(err error) (targetStatus, bool) {
	var transportErr *transport.Error
	if errors.As(err, &transportErr) && transportErr.StatusCode == http.StatusNotFound {
		for _, diag := range transportErr.Errors {
			switch diag.Code {
			case transport.NameUnknownErrorCode:
				return statusNewRepository, true
			case transport.ManifestUnknownErrorCode:
				return statusNewTag, true
			}
		}
	}
	return statusUnknown, false
}
