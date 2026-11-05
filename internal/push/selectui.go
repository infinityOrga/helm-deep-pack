package push

import (
	"bufio"
	"fmt"
	"helm-deep-pack/internal/progress"
	"helm-deep-pack/internal/pushspec"
	"helm-deep-pack/internal/terminal"
	"helm-deep-pack/internal/termstyle"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

type selectModel struct {
	images       []classifiedImage
	chart        *classifiedChart
	registry     string
	cursor       int
	checked      []bool
	chartChecked bool
	top          int
	height       int

	editing     bool
	editBuf     string
	editPos     int
	editErr     string
	retargeted  bool
	chartTarget string
}

func newSelectModel(images []classifiedImage, chart *classifiedChart, height int, registry string) *selectModel {
	count := len(images)
	if chart != nil {
		count++
	}
	if height < 1 {
		if count > 0 {
			height = count
		} else {
			height = 1
		}
	}
	return &selectModel{
		images:       images,
		chart:        chart,
		registry:     strings.TrimRight(registry, "/"),
		checked:      make([]bool, len(images)),
		chartChecked: chart != nil,
		height:       height,
	}
}

func (m *selectModel) itemCount() int {
	count := len(m.images)
	if m.chart != nil {
		count++
	}
	return count
}

func (m *selectModel) imageIndex(item int) int {
	if m.chart != nil {
		return item - 1
	}
	return item
}

func (m *selectModel) moveUp() {
	if m.cursor > 0 {
		m.cursor--
	}
	if m.cursor < m.top {
		m.top = m.cursor
	}
}

func (m *selectModel) moveDown() {
	if m.cursor < m.itemCount()-1 {
		m.cursor++
	}
	if m.cursor >= m.top+m.height {
		m.top = m.cursor - m.height + 1
	}
}

func (m *selectModel) toggle() {
	if m.onChart() {
		m.chartChecked = !m.chartChecked
	} else if m.itemCount() > 0 {
		imageIndex := m.imageIndex(m.cursor)
		m.checked[imageIndex] = !m.checked[imageIndex]
	}
}

func (m *selectModel) toggleAll() {
	allChecked := m.chart == nil || m.chartChecked
	for _, c := range m.checked {
		if !c {
			allChecked = false
			break
		}
	}
	for i := range m.checked {
		m.checked[i] = !allChecked
	}
	if m.chart != nil {
		m.chartChecked = !allChecked
	}
}

func (m *selectModel) selectedSpecs() []pushspec.ArchiveSpec {
	var result []pushspec.ArchiveSpec
	for i, checked := range m.checked {
		if checked {
			result = append(result, m.images[i].Spec)
		}
	}
	return result
}

func (m *selectModel) selection() pushSelection {
	sel := pushSelection{Images: m.selectedSpecs(), Chart: m.chartChecked}
	if m.retargeted {
		for _, image := range m.images {
			sel.Retargeted = append(sel.Retargeted, image.Spec)
		}
	}
	sel.ChartTarget = m.chartTarget
	return sel
}

func (m *selectModel) onChart() bool {
	return m.chart != nil && m.cursor == 0
}

func (m *selectModel) selectedCount() int {
	count := 0
	if m.chart != nil && m.chartChecked {
		count++
	}
	for _, c := range m.checked {
		if c {
			count++
		}
	}
	return count
}

func (m *selectModel) render() []string {
	lines := make([]string, 0)

	if m.editing {
		lines = append(lines, "Edit target path (←/→ move · home/end · enter save · esc discard)")
	} else {
		lines = append(lines, "Select items to push (↑/↓ move · space toggle · a all · e retarget · enter confirm · esc cancel)")
	}

	end := min(m.top+m.height, m.itemCount())
	if m.chart != nil && m.top == 0 && end > 0 {
		lines = append(lines, "Helm chart")
		prefix := "  "
		if m.onChart() {
			prefix = "❯ "
		}
		checkbox := "◯ "
		if m.chartChecked {
			checkbox = "◉ "
		}
		chart := m.chart.Bundle
		label, _ := statusDisplay(m.chart.Status)
		target := m.withRegistry(m.chartRef(chart.targetPath()))
		if m.editing && m.onChart() {
			target = m.withRegistry(m.editView() + ":" + chart.tag)
		}
		lines = append(lines, prefix+checkbox+chart.name+":"+chart.version+" → "+target+"  "+label)
	}

	imageStart := m.top
	if m.chart != nil && imageStart < 1 {
		imageStart = 1
	}
	if imageStart < end {
		lines = append(lines, "Images")
		for i := imageStart; i < end; i++ {
			imageIndex := m.imageIndex(i)
			image := m.images[imageIndex]
			prefix := "  "
			if i == m.cursor {
				prefix = "❯ "
			}
			checkbox := "◯ "
			if m.checked[imageIndex] {
				checkbox = "◉ "
			}
			target := m.withRegistry(image.Spec.Target)
			if m.editing && i == m.cursor {
				target = m.withRegistry(m.editView() + strings.TrimPrefix(image.Spec.Target, pushspec.TargetRepository(image.Spec.Target)))
			}
			body := progress.NormalizeDisplayImage(image.Spec.Image) + " → " + target
			if image.Spec.Optional {
				body += " [optional]"
				if len(image.Spec.OptionalFlags) > 0 {
					body += ": " + strings.Join(image.Spec.OptionalFlags, ", ")
				}
			}
			label, _ := statusDisplay(image.Status)
			lines = append(lines, prefix+checkbox+body+"  "+label)
		}
	}

	if m.editErr != "" {
		lines = append(lines, "error: "+m.editErr)
	} else {
		lines = append(lines, fmt.Sprintf("%d/%d selected", m.selectedCount(), m.itemCount()))
	}

	return lines
}

func (m *selectModel) withRegistry(ref string) string {
	if m.registry != "" {
		return m.registry + "/" + ref
	}
	return ref
}

func statusDisplay(status targetStatus) (label, color string) {
	switch status {
	case statusNewRepository:
		return "[new repository]", termstyle.Yellow
	case statusNewTag:
		return "[new tag]", termstyle.Yellow
	case statusMirrored:
		return "[exists]", termstyle.Green
	case statusTagExists:
		return "[tag exists]", termstyle.Green
	case statusMissing:
		return "[missing]", termstyle.Yellow
	case statusConflict:
		return "[conflict]", termstyle.Red
	case statusUnknown:
		return "[unknown]", termstyle.Magenta
	default:
		return "", ""
	}
}

type key int

const (
	keyNone key = iota
	keyUp
	keyDown
	keyToggle
	keyToggleAll
	keyConfirm
	keyCancel
	keyEdit
)

func readKey(r *bufio.Reader) (key, error) {
	b, err := r.ReadByte()
	if err != nil {
		return keyNone, err
	}

	switch b {
	case '\r', '\n':
		return keyConfirm, nil
	case ' ':
		return keyToggle, nil
	case 'a':
		return keyToggleAll, nil
	case 'e':
		return keyEdit, nil
	case 'q':
		return keyCancel, nil
	case 0x03:
		return keyCancel, nil
	case 'k':
		return keyUp, nil
	case 'j':
		return keyDown, nil
	case 0x1b:
		if r.Buffered() == 0 {
			return keyCancel, nil
		}
		next, err := r.ReadByte()
		if err != nil {
			return keyCancel, err
		}
		if next != '[' && next != 'O' {
			return keyCancel, nil
		}
		third, err := r.ReadByte()
		if err != nil {
			return keyCancel, err
		}
		switch third {
		case 'A':
			return keyUp, nil
		case 'B':
			return keyDown, nil
		case 'C', 'D':
			return keyNone, nil
		default:
			return keyCancel, nil
		}
	default:
		return keyNone, nil
	}
}

func viewportHeight(out io.Writer, count, sectionCount int) int {
	if !terminal.IsWriter(out) {
		return count
	}
	_, height := terminal.Size(out)
	if height < 3+sectionCount {
		return count
	}
	viewport := height - 3 - sectionCount
	if viewport < 1 {
		viewport = 1
	}
	if viewport > count {
		viewport = count
	}
	return viewport
}

func fitRenderLines(lines []string, width int) []string {
	if width <= 0 {
		return lines
	}
	fitted := make([]string, len(lines))
	for i, line := range lines {
		fitted[i] = terminal.Truncate(line, width-1)
	}
	return fitted
}

func colorizeRenderLines(lines []string, model *selectModel) []string {
	if len(lines) == 0 || model == nil {
		return lines
	}

	styled := append([]string(nil), lines...)
	styled[0] = termstyle.Bold + termstyle.Cyan + styled[0] + termstyle.Reset

	end := min(model.top+model.height, model.itemCount())
	lineIdx := 1
	if model.chart != nil && model.top == 0 && end > 0 {
		styled[lineIdx] = termstyle.Bold + termstyle.Cyan + styled[lineIdx] + termstyle.Reset
		lineIdx++
		_, prefix := statusDisplay(model.chart.Status)
		if model.chartChecked {
			prefix = termstyle.Bold + prefix
		}
		styled[lineIdx] = prefix + styled[lineIdx] + termstyle.Reset
		lineIdx++
	}

	imageStart := model.top
	if model.chart != nil && imageStart < 1 {
		imageStart = 1
	}
	if imageStart < end {
		styled[lineIdx] = termstyle.Bold + termstyle.Cyan + styled[lineIdx] + termstyle.Reset
		lineIdx++
		for item := imageStart; item < end && lineIdx < len(styled)-1; item++ {
			imageIndex := model.imageIndex(item)
			_, prefix := statusDisplay(model.images[imageIndex].Status)
			if model.checked[imageIndex] {
				prefix = termstyle.Bold + prefix
			}
			styled[lineIdx] = prefix + styled[lineIdx] + termstyle.Reset
			lineIdx++
		}
	}

	footerColor := termstyle.Dim
	if model.selectedCount() > 0 {
		footerColor = termstyle.Bold + termstyle.Cyan
	}
	styled[len(styled)-1] = footerColor + styled[len(styled)-1] + termstyle.Reset
	return styled
}

func runSelect(in io.Reader, out io.Writer, images []classifiedImage, chart *classifiedChart, registry string, reclassify func(pushspec.ArchiveSpec) classifiedImage, reclassifyChart func(*chartBundle) classifiedChart) (selected pushSelection, cancelled bool, err error) {
	itemCount := len(images)
	sectionCount := 0
	if chart != nil {
		itemCount++
		sectionCount++
	}
	if len(images) > 0 {
		sectionCount++
	}
	if itemCount == 0 {
		return pushSelection{}, false, nil
	}

	height := viewportHeight(out, itemCount, sectionCount)
	model := newSelectModel(images, chart, height, registry)

	var state *term.State
	var inputFD int
	hasRawTerminal := false
	if f, ok := in.(*os.File); ok {
		if terminal.IsWriter(out) {
			inputFD = int(f.Fd())
			st, makeRawErr := term.MakeRaw(inputFD)
			if makeRawErr != nil {
				return pushSelection{}, false, fmt.Errorf("enable raw terminal mode: %w", makeRawErr)
			}
			state = st
			hasRawTerminal = true
		}
	}
	if hasRawTerminal {
		defer func() {
			restoreErr := term.Restore(inputFD, state)
			if err == nil && restoreErr != nil {
				err = fmt.Errorf("restore terminal mode: %w", restoreErr)
			}
		}()
	}

	br := bufio.NewReader(in)
	lastLines := 0
	colorOutput := terminal.IsWriter(out)

	for {
		width, _ := terminal.Size(out)
		lines := fitRenderLines(model.render(), width)
		if colorOutput {
			lines = colorizeRenderLines(lines, model)
		}
		if lastLines > 0 {
			if err := terminal.ClearBlock(out, lastLines); err != nil {
				return pushSelection{}, false, fmt.Errorf("clear interactive output: %w", err)
			}
		}
		for i, line := range lines {
			if i > 0 {
				if _, err := io.WriteString(out, "\r\n"); err != nil {
					return pushSelection{}, false, fmt.Errorf("write interactive output: %w", err)
				}
			}
			if _, err := io.WriteString(out, "\r"+line); err != nil {
				return pushSelection{}, false, fmt.Errorf("write interactive output: %w", err)
			}
		}
		lastLines = len(lines)

		if model.editing {
			b, err := br.ReadByte()
			if err != nil && err != io.EOF {
				return pushSelection{}, false, err
			}
			if err != nil || model.editKey(b, br, reclassify, reclassifyChart) {
				if _, err := io.WriteString(out, "\r\n"); err != nil {
					return pushSelection{}, false, fmt.Errorf("write interactive output: %w", err)
				}
				return pushSelection{}, true, nil
			}
			continue
		}

		k, err := readKey(br)
		if err != nil {
			if err == io.EOF {
				if _, err := io.WriteString(out, "\r\n"); err != nil {
					return pushSelection{}, false, fmt.Errorf("write interactive output: %w", err)
				}
				return pushSelection{}, true, nil
			}
			return pushSelection{}, false, err
		}

		switch k {
		case keyUp:
			model.moveUp()
		case keyDown:
			model.moveDown()
		case keyToggle:
			model.toggle()
		case keyToggleAll:
			model.toggleAll()
		case keyEdit:
			model.startEdit()
		case keyConfirm:
			selected := model.selection()
			if _, err := io.WriteString(out, "\r\n"); err != nil {
				return pushSelection{}, false, fmt.Errorf("write interactive output: %w", err)
			}
			return selected, false, nil
		case keyCancel:
			if _, err := io.WriteString(out, "\r\n"); err != nil {
				return pushSelection{}, false, fmt.Errorf("write interactive output: %w", err)
			}
			return pushSelection{}, true, nil
		}
	}
}
