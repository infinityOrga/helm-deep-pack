package push

import (
	"bufio"
	"helm-deep-pack/internal/pushspec"
	"helm-deep-pack/internal/termstyle"
	"strings"
	"testing"
)

func newClassifiedImageForTest(spec pushspec.ArchiveSpec, status imageStatus) classifiedImage {
	return classifiedImage{
		Spec:   spec,
		Status: status,
	}
}

func TestSelectModelToggleAndSelectedSpecs(t *testing.T) {
	items := []classifiedImage{
		newClassifiedImageForTest(
			pushspec.ArchiveSpec{Image: "busybox:1.36", Target: "library/busybox:1.36"},
			statusPushable,
		),
		newClassifiedImageForTest(
			pushspec.ArchiveSpec{Image: "alpine:3.18", Target: "library/alpine:3.18"},
			statusMirrored,
		),
		newClassifiedImageForTest(
			pushspec.ArchiveSpec{Image: "nginx:latest", Target: "library/nginx:latest"},
			statusPushable,
		),
	}

	model := newSelectModel(items, 10, "")

	// Initially no items selected
	if got := model.selectedCount(); got != 0 {
		t.Fatalf("selectedCount() = %d, want 0", got)
	}

	// Toggle first item
	model.toggle()
	if got := model.selectedCount(); got != 1 {
		t.Fatalf("after toggle at cursor 0: selectedCount() = %d, want 1", got)
	}

	// Move down twice
	model.moveDown()
	model.moveDown()

	// Toggle third item
	model.toggle()
	if got := model.selectedCount(); got != 2 {
		t.Fatalf("after toggle at cursor 2: selectedCount() = %d, want 2", got)
	}

	// Check selectedSpecs
	selected := model.selectedSpecs()
	if len(selected) != 2 {
		t.Fatalf("selectedSpecs() returned %d specs, want 2", len(selected))
	}
	if selected[0].Image != "busybox:1.36" {
		t.Fatalf("selected[0].Image = %q, want busybox:1.36", selected[0].Image)
	}
	if selected[1].Image != "nginx:latest" {
		t.Fatalf("selected[1].Image = %q, want nginx:latest", selected[1].Image)
	}
}

func TestSelectModelMoveClampsAndScrolls(t *testing.T) {
	items := []classifiedImage{
		newClassifiedImageForTest(pushspec.ArchiveSpec{Image: "image1", Target: "target1"}, statusPushable),
		newClassifiedImageForTest(pushspec.ArchiveSpec{Image: "image2", Target: "target2"}, statusPushable),
		newClassifiedImageForTest(pushspec.ArchiveSpec{Image: "image3", Target: "target3"}, statusPushable),
		newClassifiedImageForTest(pushspec.ArchiveSpec{Image: "image4", Target: "target4"}, statusPushable),
		newClassifiedImageForTest(pushspec.ArchiveSpec{Image: "image5", Target: "target5"}, statusPushable),
	}

	model := newSelectModel(items, 2, "")

	// moveUp at top keeps cursor at 0
	model.moveUp()
	if model.cursor != 0 {
		t.Fatalf("after moveUp at top: cursor = %d, want 0", model.cursor)
	}

	// moveDown 4 times to reach last item
	for i := 0; i < 4; i++ {
		model.moveDown()
	}

	if model.cursor != 4 {
		t.Fatalf("after 4 moveDown: cursor = %d, want 4", model.cursor)
	}

	// Check that cursor is within visible range
	if model.cursor < model.top || model.cursor >= model.top+model.height {
		t.Fatalf("cursor %d not in viewport [%d, %d)", model.cursor, model.top, model.top+model.height)
	}

	// moveUp back to 0
	for i := 0; i < 4; i++ {
		model.moveUp()
	}

	if model.cursor != 0 {
		t.Fatalf("after 4 moveUp: cursor = %d, want 0", model.cursor)
	}

	if model.top != 0 {
		t.Fatalf("after scrolling back to cursor 0: top = %d, want 0", model.top)
	}
}

func TestSelectModelToggleAll(t *testing.T) {
	items := []classifiedImage{
		newClassifiedImageForTest(pushspec.ArchiveSpec{Image: "image1", Target: "target1"}, statusPushable),
		newClassifiedImageForTest(pushspec.ArchiveSpec{Image: "image2", Target: "target2"}, statusPushable),
		newClassifiedImageForTest(pushspec.ArchiveSpec{Image: "image3", Target: "target3"}, statusPushable),
	}

	model := newSelectModel(items, 10, "")

	// toggleAll when nothing checked -> all checked
	model.toggleAll()
	if got := model.selectedCount(); got != 3 {
		t.Fatalf("after first toggleAll: selectedCount() = %d, want 3", got)
	}

	// toggleAll when all checked -> none checked
	model.toggleAll()
	if got := model.selectedCount(); got != 0 {
		t.Fatalf("after second toggleAll: selectedCount() = %d, want 0", got)
	}

	// Toggle one, then toggleAll -> since not all checked, becomes all checked
	model.toggle()
	if got := model.selectedCount(); got != 1 {
		t.Fatalf("after toggle on one: selectedCount() = %d, want 1", got)
	}

	model.toggleAll()
	if got := model.selectedCount(); got != 3 {
		t.Fatalf("after toggleAll with one checked: selectedCount() = %d, want 3", got)
	}
}

func TestSelectModelRender(t *testing.T) {
	items := []classifiedImage{
		newClassifiedImageForTest(
			pushspec.ArchiveSpec{Image: "busybox:1.36", Target: "library/busybox:1.36"},
			statusPushable,
		),
		newClassifiedImageForTest(
			pushspec.ArchiveSpec{Image: "nginx:latest", Target: "library/nginx:latest"},
			statusConflict,
		),
	}

	model := newSelectModel(items, 10, "registry.local:5000/")

	lines := model.render()

	// Should have header, 2 items, footer
	if len(lines) != 4 {
		t.Fatalf("render() returned %d lines, want 4", len(lines))
	}

	// First line should be header
	if !strings.Contains(lines[0], "Select images to push") {
		t.Fatalf("header line = %q", lines[0])
	}

	// Check item line contains cursor, checkbox, body, status
	itemLine := lines[1]
	if !strings.Contains(itemLine, "❯ ") {
		t.Fatalf("item line missing cursor: %q", itemLine)
	}
	if !strings.Contains(itemLine, "◯ ") {
		t.Fatalf("item line missing unchecked checkbox: %q", itemLine)
	}
	if !strings.Contains(itemLine, "busybox:1.36") {
		t.Fatalf("item line missing image: %q", itemLine)
	}
	if !strings.Contains(itemLine, "registry.local:5000/library/busybox:1.36") {
		t.Fatalf("item line missing registry-qualified target: %q", itemLine)
	}
	if !strings.Contains(itemLine, "[missing]") {
		t.Fatalf("item line missing status: %q", itemLine)
	}

	// Last line should be footer with count
	footer := lines[3]
	if footer != "0/2 selected" {
		t.Fatalf("footer = %q, want \"0/2 selected\"", footer)
	}

	// Toggle first item
	model.toggle()
	lines = model.render()
	itemLine = lines[1]

	// Now should have checked checkbox and updated footer
	if !strings.Contains(itemLine, "◉ ") {
		t.Fatalf("after toggle: item line missing checked checkbox: %q", itemLine)
	}

	footer = lines[3]
	if footer != "1/2 selected" {
		t.Fatalf("after toggle: footer = %q, want \"1/2 selected\"", footer)
	}
}

func TestSelectModelRenderMarksOptionalImagesAndLeavesThemUnchecked(t *testing.T) {
	model := newSelectModel([]classifiedImage{
		newClassifiedImageForTest(pushspec.ArchiveSpec{
			Image:         "quay.io/example/metrics:v1",
			Target:        "example/metrics:v1",
			Optional:      true,
			OptionalFlags: []string{".Values.metrics.enabled"},
		}, statusPushable),
	}, 10, "")

	lines := model.render()
	if model.selectedCount() != 0 {
		t.Fatalf("optional image was selected by default: %v", model.selectedSpecs())
	}
	if !strings.Contains(lines[1], "[optional]") || !strings.Contains(lines[1], ".Values.metrics.enabled") {
		t.Fatalf("optional row = %q, want optional marker and flag path", lines[1])
	}
	if !strings.Contains(lines[1], "◯ ") {
		t.Fatalf("optional row = %q, want unchecked row", lines[1])
	}

	model.toggleAll()
	if model.selectedCount() != 1 {
		t.Fatalf("toggleAll() selectedCount() = %d, want 1", model.selectedCount())
	}
}

func TestReadKey(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  key
	}{
		{name: "carriage return", input: "\r", want: keyConfirm},
		{name: "newline", input: "\n", want: keyConfirm},
		{name: "space", input: " ", want: keyToggle},
		{name: "a key", input: "a", want: keyToggleAll},
		{name: "q key", input: "q", want: keyCancel},
		{name: "j key", input: "j", want: keyDown},
		{name: "k key", input: "k", want: keyUp},
		{name: "ctrl-c", input: "\x03", want: keyCancel},
		{name: "arrow up", input: "\x1b[A", want: keyUp},
		{name: "arrow down", input: "\x1b[B", want: keyDown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := bufio.NewReader(strings.NewReader(tt.input))
			got, err := readKey(r)
			if err != nil {
				t.Fatalf("readKey() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("readKey() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestReadKeyArrowRight(t *testing.T) {
	// Arrow right should return keyNone
	r := bufio.NewReader(strings.NewReader("\x1b[C"))
	got, err := readKey(r)
	if err != nil {
		t.Fatalf("readKey() error = %v", err)
	}
	if got != keyNone {
		t.Fatalf("readKey() for arrow right = %v, want keyNone", got)
	}
}

func TestReadKeyArrowLeft(t *testing.T) {
	// Arrow left should return keyNone
	r := bufio.NewReader(strings.NewReader("\x1b[D"))
	got, err := readKey(r)
	if err != nil {
		t.Fatalf("readKey() error = %v", err)
	}
	if got != keyNone {
		t.Fatalf("readKey() for arrow left = %v, want keyNone", got)
	}
}

func TestReadKeyEOF(t *testing.T) {
	r := bufio.NewReader(strings.NewReader(""))
	_, err := readKey(r)
	if err == nil {
		t.Fatalf("readKey() on EOF error = nil, want error")
	}
}

func TestSelectModelRenderScrolling(t *testing.T) {
	items := []classifiedImage{
		newClassifiedImageForTest(pushspec.ArchiveSpec{Image: "image1", Target: "target1"}, statusPushable),
		newClassifiedImageForTest(pushspec.ArchiveSpec{Image: "image2", Target: "target2"}, statusPushable),
		newClassifiedImageForTest(pushspec.ArchiveSpec{Image: "image3", Target: "target3"}, statusPushable),
		newClassifiedImageForTest(pushspec.ArchiveSpec{Image: "image4", Target: "target4"}, statusPushable),
		newClassifiedImageForTest(pushspec.ArchiveSpec{Image: "image5", Target: "target5"}, statusPushable),
	}

	// Height 2 means only 2 items visible plus header and footer
	model := newSelectModel(items, 2, "")

	// Move to last item
	for i := 0; i < 4; i++ {
		model.moveDown()
	}

	lines := model.render()

	// Should have header, 2 visible items, footer = 4 lines
	if len(lines) != 4 {
		t.Fatalf("render() returned %d lines, want 4", len(lines))
	}

	// Should not contain image1 or image2
	rendered := strings.Join(lines, "\n")
	if strings.Contains(rendered, "image1") || strings.Contains(rendered, "image2") {
		t.Fatalf("rendered viewport contains scrolled-off items: %s", rendered)
	}

	// Should contain image4 and image5
	if !strings.Contains(rendered, "image4") || !strings.Contains(rendered, "image5") {
		t.Fatalf("rendered viewport missing expected items: %s", rendered)
	}
}

func TestFitRenderLines(t *testing.T) {
	lines := []string{
		"short",
		"this line is definitely too long",
	}

	fitted := fitRenderLines(lines, 10)
	if len(fitted) != len(lines) {
		t.Fatalf("fitRenderLines() len = %d, want %d", len(fitted), len(lines))
	}
	if fitted[0] != "short" {
		t.Fatalf("fitRenderLines() first line = %q, want %q", fitted[0], "short")
	}
	if len(fitted[1]) > 9 {
		t.Fatalf("fitRenderLines() second line length = %d, want <= 9", len(fitted[1]))
	}

	unbounded := fitRenderLines(lines, 0)
	if unbounded[1] != lines[1] {
		t.Fatalf("fitRenderLines() width 0 altered line: got %q want %q", unbounded[1], lines[1])
	}
}

func TestColorizeRenderLines(t *testing.T) {
	model := newSelectModel([]classifiedImage{
		newClassifiedImageForTest(pushspec.ArchiveSpec{Image: "a", Target: "a"}, statusPushable),
		newClassifiedImageForTest(pushspec.ArchiveSpec{Image: "b", Target: "b"}, statusConflict),
	}, 10, "")
	model.checked[0] = true

	lines := model.render()
	styled := colorizeRenderLines(lines, model)

	if !strings.Contains(styled[0], termstyle.Cyan) {
		t.Fatalf("header not colorized: %q", styled[0])
	}
	if !strings.Contains(styled[1], termstyle.Yellow) || !strings.Contains(styled[1], termstyle.Bold) {
		t.Fatalf("selected missing row style mismatch: %q", styled[1])
	}
	if !strings.Contains(styled[2], termstyle.Red) {
		t.Fatalf("conflict row style mismatch: %q", styled[2])
	}
	if !strings.Contains(styled[len(styled)-1], termstyle.Bold) {
		t.Fatalf("footer style mismatch: %q", styled[len(styled)-1])
	}
}
