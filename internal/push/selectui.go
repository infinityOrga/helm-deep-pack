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
	items    []classifiedImage
	registry string
	cursor   int
	checked  []bool
	top      int
	height   int
}

func newSelectModel(items []classifiedImage, height int, registry string) *selectModel {
	if height < 1 {
		if len(items) > 0 {
			height = len(items)
		} else {
			height = 1
		}
	}
	return &selectModel{
		items:    items,
		registry: strings.TrimRight(registry, "/"),
		cursor:   0,
		checked:  make([]bool, len(items)),
		top:      0,
		height:   height,
	}
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
	if m.cursor < len(m.items)-1 {
		m.cursor++
	}
	if m.cursor >= m.top+m.height {
		m.top = m.cursor - m.height + 1
	}
}

func (m *selectModel) toggle() {
	if len(m.items) > 0 {
		m.checked[m.cursor] = !m.checked[m.cursor]
	}
}

func (m *selectModel) toggleAll() {
	allChecked := true
	for _, c := range m.checked {
		if !c {
			allChecked = false
			break
		}
	}
	for i := range m.checked {
		m.checked[i] = !allChecked
	}
}

func (m *selectModel) selectedSpecs() []pushspec.ArchiveSpec {
	var result []pushspec.ArchiveSpec
	for i, checked := range m.checked {
		if checked {
			result = append(result, m.items[i].Spec)
		}
	}
	return result
}

func (m *selectModel) selectedCount() int {
	count := 0
	for _, c := range m.checked {
		if c {
			count++
		}
	}
	return count
}

func (m *selectModel) render() []string {
	lines := make([]string, 0)

	lines = append(lines, "Select images to push (↑/↓ move · space toggle · a all · enter confirm · esc cancel)")

	end := min(m.top+m.height, len(m.items))
	for i := m.top; i < end; i++ {
		prefix := "  "
		if i == m.cursor {
			prefix = "❯ "
		}
		checkbox := "◯ "
		if m.checked[i] {
			checkbox = "◉ "
		}
		target := m.items[i].Spec.Target
		if m.registry != "" {
			target = m.registry + "/" + target
		}
		body := progress.NormalizeDisplayImage(m.items[i].Spec.Image) + " → " + target
		status := ""
		switch m.items[i].Status {
		case statusPushable:
			status = "  [missing]"
		case statusMirrored:
			status = "  [exists]"
		case statusConflict:
			status = "  [conflict]"
		case statusUnknown:
			status = "  [unknown]"
		}
		line := prefix + checkbox + body + status
		lines = append(lines, line)
	}

	lines = append(lines, fmt.Sprintf("%d/%d selected", m.selectedCount(), len(m.items)))

	return lines
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

func viewportHeight(out io.Writer, count int) int {
	if !terminal.IsWriter(out) {
		return count
	}
	_, height := terminal.Size(out)
	if height < 4 {
		return count
	}
	viewport := height - 3
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

	end := min(model.top+model.height, len(model.items))
	for itemIdx := model.top; itemIdx < end; itemIdx++ {
		lineIdx := 1 + (itemIdx - model.top)
		if lineIdx >= len(styled)-1 {
			break
		}

		prefix := statusColor(model.items[itemIdx].Status)
		if model.checked[itemIdx] {
			prefix = termstyle.Bold + prefix
		}
		styled[lineIdx] = prefix + styled[lineIdx] + termstyle.Reset
	}

	footerColor := termstyle.Dim
	if model.selectedCount() > 0 {
		footerColor = termstyle.Bold + termstyle.Cyan
	}
	styled[len(styled)-1] = footerColor + styled[len(styled)-1] + termstyle.Reset
	return styled
}

func statusColor(status imageStatus) string {
	switch status {
	case statusPushable:
		return termstyle.Yellow
	case statusMirrored:
		return termstyle.Green
	case statusConflict:
		return termstyle.Red
	case statusUnknown:
		return termstyle.Magenta
	default:
		return ""
	}
}

func runSelect(in io.Reader, out io.Writer, items []classifiedImage, registry string) (selected []pushspec.ArchiveSpec, cancelled bool, err error) {
	if len(items) == 0 {
		return nil, false, nil
	}

	height := viewportHeight(out, len(items))
	model := newSelectModel(items, height, registry)

	var state *term.State
	var inputFD int
	hasRawTerminal := false
	if f, ok := in.(*os.File); ok {
		if terminal.IsWriter(out) {
			inputFD = int(f.Fd())
			st, makeRawErr := term.MakeRaw(inputFD)
			if makeRawErr != nil {
				return nil, false, fmt.Errorf("enable raw terminal mode: %w", makeRawErr)
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
		lines := fitRenderLines(model.render(), terminal.Width(out))
		if colorOutput {
			lines = colorizeRenderLines(lines, model)
		}
		if lastLines > 0 {
			if err := terminal.ClearBlock(out, lastLines); err != nil {
				return nil, false, fmt.Errorf("clear interactive output: %w", err)
			}
		}
		for i, line := range lines {
			if i > 0 {
				if err := terminal.WriteString(out, "\r\n"); err != nil {
					return nil, false, fmt.Errorf("write interactive output: %w", err)
				}
			}
			if err := terminal.WriteString(out, "\r"+line); err != nil {
				return nil, false, fmt.Errorf("write interactive output: %w", err)
			}
		}
		lastLines = len(lines)

		k, err := readKey(br)
		if err != nil {
			if err == io.EOF {
				if err := terminal.WriteString(out, "\r\n"); err != nil {
					return nil, false, fmt.Errorf("write interactive output: %w", err)
				}
				return nil, true, nil
			}
			return nil, false, err
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
		case keyConfirm:
			selected := model.selectedSpecs()
			if err := terminal.WriteString(out, "\r\n"); err != nil {
				return nil, false, fmt.Errorf("write interactive output: %w", err)
			}
			return selected, false, nil
		case keyCancel:
			if err := terminal.WriteString(out, "\r\n"); err != nil {
				return nil, false, fmt.Errorf("write interactive output: %w", err)
			}
			return nil, true, nil
		}
	}
}
