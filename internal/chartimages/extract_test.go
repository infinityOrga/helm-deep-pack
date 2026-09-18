package chartimages

import "testing"

func TestExtractImages(t *testing.T) {
	manifest := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: api
spec:
  template:
    spec:
      containers:
        - name: api
          image: quay.io/example/api:v1
        - name: sidecar
          image: busybox:1.36
      initContainers:
        - name: init
          image: busybox:1.36
---
apiVersion: batch/v1
kind: Job
metadata:
  name: migrate
spec:
  template:
    spec:
      containers:
        - name: migrate
          image: quay.io/example/migrate:v2
`

	got, err := ExtractImages(manifest)
	if err != nil {
		t.Fatalf("ExtractImages() error = %v", err)
	}

	want := []string{
		"quay.io/example/api:v1",
		"busybox:1.36",
		"quay.io/example/migrate:v2",
	}
	if len(got) != len(want) {
		t.Fatalf("ExtractImages() len = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ExtractImages()[%d] = %q, want %q (full = %v)", i, got[i], want[i], got)
		}
	}
}

func TestExtractImagesFindsImagesInScalarArgsAndEnvValues(t *testing.T) {
	manifest := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: operator
spec:
  template:
    spec:
      containers:
        - name: manager
          image: quay.io/example/operator:v1
          args:
            - --executor-image=quay.io/example/executor:v2
            - --prometheus-config-reloader=quay.io/prometheus-operator/prometheus-config-reloader:v4
            - --prometheus-config-reloader
            - quay.io/prometheus-operator/prometheus-config-reloader:v5
            - --config-reloader-cpu-request=200m
          env:
            - name: STRIMZI_DEFAULT_TOPIC_OPERATOR_IMAGE
              value: quay.io/strimzi/operator-topic:v3
`

	got, err := ExtractImages(manifest)
	if err != nil {
		t.Fatalf("ExtractImages() error = %v", err)
	}

	want := []string{
		"quay.io/example/operator:v1",
		"quay.io/example/executor:v2",
		"quay.io/prometheus-operator/prometheus-config-reloader:v4",
		"quay.io/prometheus-operator/prometheus-config-reloader:v5",
		"quay.io/strimzi/operator-topic:v3",
	}
	if len(got) != len(want) {
		t.Fatalf("ExtractImages() len = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ExtractImages()[%d] = %q, want %q (full=%v)", i, got[i], want[i], got)
		}
	}
}

func TestExtractImagesFindsBareImageReferencesInScalars(t *testing.T) {
	manifest := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: operator
spec:
  template:
    spec:
      containers:
        - name: manager
          args:
            - --default-image=nginx
          env:
            - name: SIDECAR_IMAGE
              value: alpine
`

	got, err := ExtractImages(manifest)
	if err != nil {
		t.Fatalf("ExtractImages() error = %v", err)
	}

	want := []string{"nginx", "alpine"}
	if len(got) != len(want) {
		t.Fatalf("ExtractImages() len = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ExtractImages()[%d] = %q, want %q (full=%v)", i, got[i], want[i], got)
		}
	}
}

func TestExtractImagesDoesNotTreatGenericCommandTokensAsImages(t *testing.T) {
	manifest := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: generic-command
spec:
  template:
    spec:
      containers:
        - name: manager
          command:
            - sleep
            - "3600"
          args:
            - bash
            - -c
            - echo hi
`

	got, err := ExtractImages(manifest)
	if err != nil {
		t.Fatalf("ExtractImages() error = %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("ExtractImages() = %v, want empty", got)
	}
}

func TestExtractImagesFindsImageInSplitFlagArguments(t *testing.T) {
	manifest := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: split-flags
spec:
  template:
    spec:
      containers:
        - name: manager
          args:
            - --executor-image
            - quay.io/example/executor:v2
`

	got, err := ExtractImages(manifest)
	if err != nil {
		t.Fatalf("ExtractImages() error = %v", err)
	}

	want := []string{"quay.io/example/executor:v2"}
	if len(got) != len(want) {
		t.Fatalf("ExtractImages() len = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ExtractImages()[%d] = %q, want %q (full=%v)", i, got[i], want[i], got)
		}
	}
}

func TestExtractChartAnnotationImagesParsesAnnotationHelmShImages(t *testing.T) {
	got, err := ExtractChartAnnotationImages(map[string]string{
		"annotation.helm.sh/images": `
- quay.io/example/from-annotation:v2
- busybox:1.36
`,
	})
	if err != nil {
		t.Fatalf("ExtractChartAnnotationImages() error = %v", err)
	}

	want := []string{
		"quay.io/example/from-annotation:v2",
		"busybox:1.36",
	}
	if len(got) != len(want) {
		t.Fatalf("ExtractChartAnnotationImages() len = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ExtractChartAnnotationImages()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestExtractChartAnnotationImagesSupportsBareReferencesInStructuredList(t *testing.T) {
	got, err := ExtractChartAnnotationImages(map[string]string{
		"annotation.helm.sh/images": `
- nginx
- alpine
`,
	})
	if err != nil {
		t.Fatalf("ExtractChartAnnotationImages() error = %v", err)
	}

	want := []string{"nginx", "alpine"}
	if len(got) != len(want) {
		t.Fatalf("ExtractChartAnnotationImages() len = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ExtractChartAnnotationImages()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestExtractChartAnnotationImagesIgnoresInvalidFallbackTokens(t *testing.T) {
	got, err := ExtractChartAnnotationImages(map[string]string{
		"annotation.helm.sh/images": "not-an-image, still-not-one",
	})
	if err == nil {
		t.Fatal("ExtractChartAnnotationImages() error = nil, want error")
	}
	if len(got) != 0 {
		t.Fatalf("ExtractChartAnnotationImages() = %v, want empty", got)
	}
}
