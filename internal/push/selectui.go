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
	"unicode/utf8"

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
	width        int

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
	m.ensureVisible()
}

func (m *selectModel) moveDown() {
	if m.cursor < m.itemCount()-1 {
		m.cursor++
	}
	m.ensureVisible()
}

func (m *selectModel) rowHeight(item int) int {
	return len(wrapText(m.itemBlock(item).text, m.width, "    "))
}

func (m *selectModel) linesBetween(from, to int) int {
	total := 0
	for i := from; i <= to; i++ {
		total += m.rowHeight(i)
	}
	return total
}

// ensureVisible scrolls so the whole cursor row fits in the item budget.
func (m *selectModel) ensureVisible() {
	if m.cursor < m.top {
		m.top = m.cursor
	}
	for m.top < m.cursor && m.linesBetween(m.top, m.cursor) > m.height {
		m.top++
	}
}

// visibleEnd returns the exclusive end of the items shown from top.
func (m *selectModel) visibleEnd() int {
	end, used := m.top, 0
	for end < m.itemCount() {
		h := m.rowHeight(end)
		if end > m.top && used+h > m.height {
			break
		}
		used += h
		end++
	}
	return end
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

type block struct {
	text   string
	style  string
	indent bool
}

func (m *selectModel) itemBlock(i int) block {
	prefix := "  "
	if i == m.cursor {
		prefix = "❯ "
	}
	if m.chart != nil && i == 0 {
		checkbox := "◯ "
		style := ""
		if m.chartChecked {
			checkbox = "◉ "
			style = termstyle.Bold
		}
		chart := m.chart.Bundle
		label, color := statusDisplay(m.chart.Status)
		target := m.withRegistry(m.chartRef(chart.targetPath()))
		if m.editing && m.onChart() {
			target = m.withRegistry(m.editView() + ":" + chart.tag)
		}
		return block{prefix + checkbox + chart.name + ":" + chart.version + " → " + target + "  " + label, style + color, true}
	}
	imageIndex := m.imageIndex(i)
	image := m.images[imageIndex]
	checkbox := "◯ "
	style := ""
	if m.checked[imageIndex] {
		checkbox = "◉ "
		style = termstyle.Bold
	}
	target := m.withRegistry(image.Spec.Target)
	if m.editing && i == m.cursor {
		target = m.withRegistry(m.editView() + strings.TrimPrefix(image.Spec.Target, pushspec.TargetRepository(image.Spec.Target)))
	}
	body := progress.NormalizeDisplayImage(image.Spec.Image) + " → " + target
	if image.Spec.Optional {
		body += " [optional]"
	}
	label, color := statusDisplay(image.Status)
	return block{prefix + checkbox + body + "  " + label, style + color, true}
}

func (m *selectModel) blocks() []block {
	heading := termstyle.Bold + termstyle.Cyan
	header := "Select items to push (↑/↓ move · space toggle · a all · e retarget · enter confirm · esc cancel)"
	if m.editing {
		header = "Edit target path (←/→ move · home/end · enter save · esc discard)"
	}
	blocks := []block{{text: header, style: heading}}

	end := m.visibleEnd()
	if m.chart != nil && m.top == 0 && end > 0 {
		blocks = append(blocks, block{text: "Helm chart", style: heading}, m.itemBlock(0))
	}
	imageStart := m.top
	if m.chart != nil && imageStart < 1 {
		imageStart = 1
	}
	if imageStart < end {
		blocks = append(blocks, block{text: "Images", style: heading})
		for i := imageStart; i < end; i++ {
			blocks = append(blocks, m.itemBlock(i))
		}
	}

	if detail := m.detailLine(); detail != "" {
		blocks = append(blocks, block{text: detail, style: termstyle.Dim})
	}
	if m.editErr != "" {
		blocks = append(blocks, block{text: "error: " + m.editErr, style: termstyle.Dim})
	} else {
		style := termstyle.Dim
		if m.selectedCount() > 0 {
			style = heading
		}
		blocks = append(blocks, block{text: fmt.Sprintf("%d/%d selected", m.selectedCount(), m.itemCount()), style: style})
	}
	return blocks
}

func (m *selectModel) detailLine() string {
	if m.itemCount() == 0 || m.onChart() {
		return ""
	}
	spec := m.images[m.imageIndex(m.cursor)].Spec
	if !spec.Optional || len(spec.OptionalFlags) == 0 {
		return ""
	}
	return "optional; enabled by: " + strings.Join(spec.OptionalFlags, ", ")
}

func (m *selectModel) layout(color bool) []string {
	var lines []string
	for _, b := range m.blocks() {
		indent := ""
		if b.indent {
			indent = "    "
		}
		for _, line := range wrapText(b.text, m.width, indent) {
			if color && b.style != "" {
				line = b.style + line + termstyle.Reset
			}
			lines = append(lines, line)
		}
	}
	return lines
}

func (m *selectModel) render() []string { return m.layout(false) }

// wrapText wraps s at spaces to width-1 runes; width <= 0 disables wrapping.
// Over-long tokens break after '/', ',' or '-', then hard-break. Continuation
// lines are prefixed with indent.
func wrapText(s string, width int, indent string) []string {
	if width <= 0 || utf8.RuneCountInString(s) < width {
		return []string{s}
	}
	limit := width - 1
	contLimit := max(limit-utf8.RuneCountInString(indent), 1)
	type piece struct {
		text string
		sep  bool
	}
	var pieces []piece
	for _, word := range strings.Split(s, " ") {
		if utf8.RuneCountInString(word) <= contLimit {
			pieces = append(pieces, piece{word, true})
			continue
		}
		sep := true
		start := 0
		runes := []rune(word)
		for i, r := range runes {
			if r == '/' || r == ',' || r == '-' || i == len(runes)-1 {
				pieces = append(pieces, piece{string(runes[start : i+1]), sep})
				sep, start = false, i+1
			}
		}
	}
	var lines []string
	cur, curW, has := "", 0, false
	flush := func() {
		lines = append(lines, strings.TrimRight(cur, " "))
		cur, curW, has = indent, utf8.RuneCountInString(indent), false
	}
	for _, p := range pieces {
		lim := limit
		for _, chunk := range hardSplit(p.text, contLimit) {
			w := utf8.RuneCountInString(chunk)
			add := w
			if has && p.sep {
				add++
			}
			if has && curW+add > lim {
				flush()
				if chunk == "" {
					continue
				}
				add = w
			}
			if has && p.sep {
				cur += " "
			}
			cur += chunk
			curW += add
			has = true
			p.sep = false
		}
	}
	if has || len(lines) == 0 {
		lines = append(lines, cur)
	}
	return lines
}

// hardSplit cuts s into chunks of at most n runes.
func hardSplit(s string, n int) []string {
	runes := []rune(s)
	if len(runes) <= n {
		return []string{s}
	}
	var chunks []string
	for len(runes) > n {
		chunks = append(chunks, string(runes[:n]))
		runes = runes[n:]
	}
	return append(chunks, string(runes))
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

// fitViewport sets the item budget from the terminal height, reserving the
// header, section headings, footer and detail line.
func (m *selectModel) fitViewport(termHeight, sectionCount int) {
	reserved := 1 + sectionCount + 1 + 1
	reserved += max(len(wrapText("Select items to push (↑/↓ move · space toggle · a all · e retarget · enter confirm · esc cancel)", m.width, ""))-1, 0)
	if m.editErr != "" {
		reserved += len(wrapText("error: "+m.editErr, m.width, "")) - 1
	}
	detail := 0
	for _, image := range m.images {
		if len(image.Spec.OptionalFlags) > 0 {
			flags := "optional; enabled by: " + strings.Join(image.Spec.OptionalFlags, ", ")
			detail = max(detail, len(wrapText(flags, m.width, "")))
		}
	}
	m.height = max(termHeight-reserved-detail, 1)
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

	model := newSelectModel(images, chart, itemCount, registry)

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
		width, termHeight := terminal.Size(out)
		model.width = width
		if colorOutput {
			model.fitViewport(termHeight, sectionCount)
		}
		model.ensureVisible()
		lines := model.layout(colorOutput)
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
