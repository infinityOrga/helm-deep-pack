package push

import (
	"bufio"
	"bytes"
	"helm-deep-pack/internal/pushspec"
	"helm-deep-pack/internal/termstyle"
	"strings"
	"testing"
)

func newClassifiedImageForTest(spec pushspec.ArchiveSpec, status targetStatus) classifiedImage {
	return classifiedImage{
		Spec:             spec,
		classifiedTarget: classifiedTarget{Status: status},
	}
}

func TestSelectModelToggleAndSelectedSpecs(t *testing.T) {
	items := []classifiedImage{
		newClassifiedImageForTest(
			pushspec.ArchiveSpec{Image: "busybox:1.36", Target: "library/busybox:1.36"},
			statusNewRepository,
		),
		newClassifiedImageForTest(
			pushspec.ArchiveSpec{Image: "alpine:3.18", Target: "library/alpine:3.18"},
			statusMirrored,
		),
		newClassifiedImageForTest(
			pushspec.ArchiveSpec{Image: "nginx:latest", Target: "library/nginx:latest"},
			statusNewTag,
		),
	}

	model := newSelectModel(items, nil, 10, "")

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
		newClassifiedImageForTest(pushspec.ArchiveSpec{Image: "image1", Target: "target1"}, statusNewRepository),
		newClassifiedImageForTest(pushspec.ArchiveSpec{Image: "image2", Target: "target2"}, statusNewRepository),
		newClassifiedImageForTest(pushspec.ArchiveSpec{Image: "image3", Target: "target3"}, statusNewRepository),
		newClassifiedImageForTest(pushspec.ArchiveSpec{Image: "image4", Target: "target4"}, statusNewRepository),
		newClassifiedImageForTest(pushspec.ArchiveSpec{Image: "image5", Target: "target5"}, statusNewRepository),
	}

	model := newSelectModel(items, nil, 2, "")

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
		newClassifiedImageForTest(pushspec.ArchiveSpec{Image: "image1", Target: "target1"}, statusNewRepository),
		newClassifiedImageForTest(pushspec.ArchiveSpec{Image: "image2", Target: "target2"}, statusNewRepository),
		newClassifiedImageForTest(pushspec.ArchiveSpec{Image: "image3", Target: "target3"}, statusNewRepository),
	}

	model := newSelectModel(items, nil, 10, "")

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
			statusNewRepository,
		),
		newClassifiedImageForTest(
			pushspec.ArchiveSpec{Image: "nginx:latest", Target: "library/nginx:latest"},
			statusConflict,
		),
	}

	model := newSelectModel(items, nil, 10, "registry.local:5000/")

	lines := model.render()

	// Should have header, section title, 2 items, footer
	if len(lines) != 5 {
		t.Fatalf("render() returned %d lines, want 5", len(lines))
	}

	// First line should be header
	if !strings.Contains(lines[0], "Select items to push") {
		t.Fatalf("header line = %q", lines[0])
	}
	if lines[1] != "Images" {
		t.Fatalf("section title = %q, want Images", lines[1])
	}

	// Check item line contains cursor, checkbox, body, status
	itemLine := lines[2]
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
	if !strings.Contains(itemLine, "[new repository]") {
		t.Fatalf("item line missing status: %q", itemLine)
	}

	// Last line should be footer with count
	footer := lines[4]
	if footer != "0/2 selected" {
		t.Fatalf("footer = %q, want \"0/2 selected\"", footer)
	}

	// Toggle first item
	model.toggle()
	lines = model.render()
	itemLine = lines[2]

	// Now should have checked checkbox and updated footer
	if !strings.Contains(itemLine, "◉ ") {
		t.Fatalf("after toggle: item line missing checked checkbox: %q", itemLine)
	}

	footer = lines[4]
	if footer != "1/2 selected" {
		t.Fatalf("after toggle: footer = %q, want \"1/2 selected\"", footer)
	}
}

func TestSelectModelShowsPreselectedChartBeforeImages(t *testing.T) {
	chart := &classifiedChart{
		Bundle:           &chartBundle{name: "demo", version: "1.2.3", tag: "1.2.3"},
		classifiedTarget: classifiedTarget{Status: statusNewRepository},
	}
	model := newSelectModel([]classifiedImage{
		newClassifiedImageForTest(pushspec.ArchiveSpec{Image: "nginx:1.27", Target: "library/nginx:1.27"}, statusNewTag),
	}, chart, 10, "registry.example.com/team")

	lines := model.render()
	if lines[1] != "Helm chart" || lines[3] != "Images" {
		t.Fatalf("section titles = %q and %q, want Helm chart then Images", lines[1], lines[3])
	}
	if !strings.Contains(lines[2], "❯ ◉ demo:1.2.3") || !strings.Contains(lines[2], "[new repository]") {
		t.Fatalf("chart row = %q, want first, selected Helm chart with repository status", lines[2])
	}
	if !strings.Contains(lines[4], "[new tag]") {
		t.Fatalf("image row = %q, want new-tag status", lines[4])
	}
	if lines[5] != "1/2 selected" {
		t.Fatalf("selection summary = %q, want 1/2 selected", lines[5])
	}

	selected := model.selection()
	if !selected.Chart || len(selected.Images) != 0 {
		t.Fatalf("initial selection = %#v, want only the chart", selected)
	}
	model.toggleAll()
	if got := model.selectedCount(); got != 2 {
		t.Fatalf("toggleAll() selectedCount() = %d, want 2 including chart", got)
	}
	model.toggleAll()
	if got := model.selectedCount(); got != 0 {
		t.Fatalf("second toggleAll() selectedCount() = %d, want 0", got)
	}
}

func TestSelectModelRenderMarksOptionalImagesAndLeavesThemUnchecked(t *testing.T) {
	model := newSelectModel([]classifiedImage{
		newClassifiedImageForTest(pushspec.ArchiveSpec{
			Image:         "quay.io/example/metrics:v1",
			Target:        "example/metrics:v1",
			Optional:      true,
			OptionalFlags: []string{".Values.metrics.enabled"},
		}, statusNewTag),
	}, nil, 10, "")

	lines := model.render()
	if model.selectedCount() != 0 {
		t.Fatalf("optional image was selected by default: %v", model.selectedSpecs())
	}
	if !strings.Contains(lines[2], "[optional]") || !strings.Contains(lines[2], ".Values.metrics.enabled") {
		t.Fatalf("optional row = %q, want optional marker and flag path", lines[2])
	}
	if !strings.Contains(lines[2], "◯ ") {
		t.Fatalf("optional row = %q, want unchecked row", lines[2])
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
		newClassifiedImageForTest(pushspec.ArchiveSpec{Image: "image1", Target: "target1"}, statusNewRepository),
		newClassifiedImageForTest(pushspec.ArchiveSpec{Image: "image2", Target: "target2"}, statusNewRepository),
		newClassifiedImageForTest(pushspec.ArchiveSpec{Image: "image3", Target: "target3"}, statusNewRepository),
		newClassifiedImageForTest(pushspec.ArchiveSpec{Image: "image4", Target: "target4"}, statusNewRepository),
		newClassifiedImageForTest(pushspec.ArchiveSpec{Image: "image5", Target: "target5"}, statusNewRepository),
	}

	// Height 2 means only 2 items visible plus header and footer
	model := newSelectModel(items, nil, 2, "")

	// Move to last item
	for i := 0; i < 4; i++ {
		model.moveDown()
	}

	lines := model.render()

	// Should have header, section title, 2 visible items, footer = 5 lines
	if len(lines) != 5 {
		t.Fatalf("render() returned %d lines, want 5", len(lines))
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
		newClassifiedImageForTest(pushspec.ArchiveSpec{Image: "a", Target: "a"}, statusNewRepository),
		newClassifiedImageForTest(pushspec.ArchiveSpec{Image: "b", Target: "b"}, statusConflict),
	}, nil, 10, "")
	model.checked[0] = true

	lines := model.render()
	styled := colorizeRenderLines(lines, model)

	if !strings.Contains(styled[0], termstyle.Cyan) {
		t.Fatalf("header not colorized: %q", styled[0])
	}
	if !strings.Contains(styled[2], termstyle.Yellow) || !strings.Contains(styled[2], termstyle.Bold) {
		t.Fatalf("selected missing row style mismatch: %q", styled[2])
	}
	if !strings.Contains(styled[3], termstyle.Red) {
		t.Fatalf("conflict row style mismatch: %q", styled[3])
	}
	if !strings.Contains(styled[len(styled)-1], termstyle.Bold) {
		t.Fatalf("footer style mismatch: %q", styled[len(styled)-1])
	}
}

func runSelectScript(t *testing.T, script string, images []classifiedImage, chart *classifiedChart) (pushSelection, bool, string) {
	t.Helper()
	var out bytes.Buffer
	reclassify := func(spec pushspec.ArchiveSpec) classifiedImage {
		return newClassifiedImageForTest(spec, statusNewRepository)
	}
	sel, cancelled, err := runSelect(bytes.NewReader([]byte(script)), &out, images, chart, "", reclassify, nil)
	if err != nil {
		t.Fatalf("runSelect() error = %v", err)
	}
	return sel, cancelled, out.String()
}

func retargetFixture() []classifiedImage {
	return []classifiedImage{
		newClassifiedImageForTest(pushspec.ArchiveSpec{Image: "busybox:1.36", Target: "library/busybox:1.36"}, statusMirrored),
		newClassifiedImageForTest(pushspec.ArchiveSpec{Image: "alpine:3.18", Target: "library/alpine:1.36"}, statusMirrored),
	}
}

func TestRunSelectRetargetAndConfirm(t *testing.T) {
	sel, cancelled, out := runSelectScript(t, "e"+strings.Repeat("\x7f", 20)+"team/bb\r \r", retargetFixture(), nil)
	if cancelled {
		t.Fatal("cancelled = true")
	}
	if !strings.Contains(out, "enter save") {
		t.Fatalf("output missing edit hint: %q", out)
	}
	if len(sel.Retargeted) != 2 || sel.Retargeted[0].Target != "team/bb:1.36" || sel.Retargeted[1].Target != "library/alpine:1.36" {
		t.Fatalf("Retargeted = %+v", sel.Retargeted)
	}
	if len(sel.Images) != 1 || sel.Images[0].Target != "team/bb:1.36" {
		t.Fatalf("Images = %+v", sel.Images)
	}
}

func TestRunSelectRetargetReprobesAndKeepsChecked(t *testing.T) {
	var out bytes.Buffer
	reclassify := func(spec pushspec.ArchiveSpec) classifiedImage {
		return newClassifiedImageForTest(spec, statusConflict)
	}
	script := " e" + strings.Repeat("\x7f", 20) + "xx\r\r"
	_, _, err := runSelect(strings.NewReader(script), &out, retargetFixture(), nil, "", reclassify, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "[conflict]") {
		t.Fatalf("output missing reprobed status: %q", out.String())
	}
}

func TestRunSelectEscDiscardsEdit(t *testing.T) {
	sel, _, _ := runSelectScript(t, "ex\x1b\r", retargetFixture(), nil)
	if sel.Retargeted != nil {
		t.Fatalf("Retargeted = %+v, want nil", sel.Retargeted)
	}
}

func TestRunSelectRejectsDuplicateTarget(t *testing.T) {
	script := "e" + strings.Repeat("\x7f", 20) + "library/alpine\r\x1b\r"
	sel, _, out := runSelectScript(t, script, retargetFixture(), nil)
	if !strings.Contains(out, "library/alpine:1.36") || !strings.Contains(out, "duplicate") {
		t.Fatalf("output missing duplicate error: %q", out)
	}
	if sel.Retargeted != nil {
		t.Fatalf("Retargeted = %+v, want nil", sel.Retargeted)
	}
}

func TestRunSelectCancelAfterEditReturnsNothing(t *testing.T) {
	sel, cancelled, _ := runSelectScript(t, "ex\rq", retargetFixture(), nil)
	if !cancelled || sel.Retargeted != nil {
		t.Fatalf("cancelled = %v, Retargeted = %+v", cancelled, sel.Retargeted)
	}
}

func TestRenderEditRowShowsFullTarget(t *testing.T) {
	model := newSelectModel([]classifiedImage{
		newClassifiedImageForTest(pushspec.ArchiveSpec{Image: "busybox:1.36", Target: "library/busybox:1.36"}, statusNewTag),
		newClassifiedImageForTest(pushspec.ArchiveSpec{Image: "nginx", Target: "library/nginx@sha256:abc"}, statusNewTag),
	}, nil, 10, "reg.io/team")
	model.startEdit()
	model.editBuf = "x/bb"
	if got := model.render()[2]; !strings.Contains(got, "busybox:1.36 → reg.io/team/x/bb▏:1.36") {
		t.Fatalf("row = %q", got)
	}
	model.editing = false
	model.moveDown()
	model.startEdit()
	if got := model.render()[3]; !strings.Contains(got, "→ reg.io/team/library/nginx▏@sha256:abc") {
		t.Fatalf("row = %q", got)
	}
	model.registry = ""
	if got := model.render()[3]; !strings.Contains(got, "→ library/nginx▏@sha256:abc") {
		t.Fatalf("row = %q", got)
	}
}

func chartFixture() *classifiedChart {
	return &classifiedChart{Bundle: &chartBundle{name: "demo", version: "1.2.3", tag: "1.2.3"}}
}

func TestRunSelectRetargetsChart(t *testing.T) {
	var out bytes.Buffer
	var probed string
	reclassifyChart := func(b *chartBundle) classifiedChart {
		probed = b.repo()
		return classifiedChart{Bundle: b, classifiedTarget: classifiedTarget{Status: statusConflict}}
	}
	script := "e\x7f\x7f\x7f\x7fcharts/app\r\r"
	sel, cancelled, err := runSelect(strings.NewReader(script), &out, retargetFixture(), chartFixture(), "reg.io", func(s pushspec.ArchiveSpec) classifiedImage {
		return newClassifiedImageForTest(s, statusNewRepository)
	}, reclassifyChart)
	if err != nil || cancelled {
		t.Fatalf("err = %v, cancelled = %v", err, cancelled)
	}
	if sel.ChartTarget != "charts/app" || probed != "charts/app" || sel.Retargeted != nil {
		t.Fatalf("sel = %+v, probed = %q", sel, probed)
	}
	if !strings.Contains(out.String(), "reg.io/charts/app▏:1.2.3") || !strings.Contains(out.String(), "[conflict]") {
		t.Fatalf("output = %q", out.String())
	}
}

func TestRunSelectChartEditEscAndInvalid(t *testing.T) {
	var out bytes.Buffer
	re := func(b *chartBundle) classifiedChart { return classifiedChart{Bundle: b} }
	sel, _, err := runSelect(strings.NewReader("ex\x1b\r"), &out, nil, chartFixture(), "", nil, re)
	if err != nil || sel.ChartTarget != "" {
		t.Fatalf("sel = %+v, err = %v", sel, err)
	}
	out.Reset()
	sel, _, _ = runSelect(strings.NewReader("e:x\r\x1b\r"), &out, nil, chartFixture(), "", nil, re)
	if sel.ChartTarget != "" || !strings.Contains(out.String(), "error:") {
		t.Fatalf("sel = %+v, out = %q", sel, out.String())
	}
}
