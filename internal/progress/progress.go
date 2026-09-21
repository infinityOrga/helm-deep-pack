package progress

import (
	"fmt"
	"helm-deep-pack/internal/terminal"
	"io"
	"strings"
	"sync"
	"time"
)

const minRenderInterval = 80 * time.Millisecond

// StatusWriter returns the provided status writer or io.Discard.
func StatusWriter(status ...io.Writer) io.Writer {
	if len(status) > 0 && status[0] != nil {
		return status[0]
	}
	return io.Discard
}

// NormalizeDisplayImage removes common Docker Hub registry prefixes for display.
func NormalizeDisplayImage(image string) string {
	switch {
	case strings.HasPrefix(image, "docker.io/"):
		return strings.TrimPrefix(image, "docker.io/")
	case strings.HasPrefix(image, "index.docker.io/"):
		return strings.TrimPrefix(image, "index.docker.io/")
	default:
		return image
	}
}

// HumanizeBytes renders n in binary units with compact formatting.
func HumanizeBytes(n int64) string {
	if n <= 0 {
		return "0B"
	}

	units := []string{"B", "KB", "MB", "GB", "TB", "PB", "EB"}
	value := float64(n)
	unit := 0
	for unit < len(units)-1 && value >= 1024 {
		value /= 1024
		unit++
	}

	if unit <= 2 {
		return fmt.Sprintf("%d%s", int64(value), units[unit])
	}
	return fmt.Sprintf("%.1f%s", value, units[unit])
}

type imageState struct {
	complete int64
	total    int64
	stage    string
}

type Progress struct {
	mu         sync.Mutex
	w          io.Writer
	label      string
	total      int
	completed  int
	active     []string
	states     map[string]*imageState
	terminal   bool
	lastLen    int
	lastLines  int
	width      int
	lastRender time.Time
}

// New creates a new terminal-aware progress renderer.
func New(w io.Writer, label string, total int) *Progress {
	if w == nil {
		w = io.Discard
	}
	isTerminal := terminal.IsWriter(w)
	width := 0
	if isTerminal {
		width, _ = terminal.Size(w)
	}
	return &Progress{
		w:        w,
		label:    label,
		total:    total,
		states:   make(map[string]*imageState),
		terminal: isTerminal,
		width:    width,
	}
}

// Begin marks item as active and renders progress.
func (p *Progress) Begin(item string) {
	if p == nil {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	st := p.ensureStateLocked(item)
	st.stage = "fetching"
	p.addActiveLocked(item)
	p.renderLocked(item, false)
}

// Update replaces the tracked state for item and renders progress.
func (p *Progress) Update(item string, complete, total int64, stage string) {
	if p == nil {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	st := p.ensureStateLocked(item)
	st.complete = complete
	st.total = total
	st.stage = stage
	p.renderLocked(item, false)
}

// End marks item as complete and renders progress.
func (p *Progress) End(item string) {
	if p == nil {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	st := p.ensureStateLocked(item)
	switch {
	case st.total > 0 && st.complete == 0:
		st.complete = st.total
		st.stage = "cached"
	case st.total > 0 && st.complete < st.total:
		st.complete = st.total
		st.stage = "done"
	default:
		st.stage = "done"
	}
	p.removeActiveLocked(item)
	p.completed++

	if !p.terminal {
		p.renderLocked(item, true)
		return
	}
	if p.total <= 0 {
		return
	}

	if p.lastLines > 0 {
		_ = terminal.ClearBlock(p.w, p.lastLines)
		p.lastLines = 0
	}
	summary := fmt.Sprintf("%s %s %s", NormalizeDisplayImage(item), HumanizeBytes(st.total), st.stage)
	if p.width > 0 {
		summary = terminal.Truncate(summary, p.width-1)
	}
	_, _ = fmt.Fprintln(p.w, summary)
	p.drawLiveBlockLocked()
}

func (p *Progress) drawLiveBlockLocked() {
	lines := p.formatLinesLocked()
	if p.width > 0 {
		for i, line := range lines {
			lines[i] = terminal.Truncate(line, p.width-1)
		}
	}
	for i, line := range lines {
		if i > 0 {
			_, _ = fmt.Fprint(p.w, "\n")
		}
		_, _ = fmt.Fprint(p.w, line)
	}
	p.lastLines = len(lines)
	if len(lines) > 0 {
		p.lastLen = len(lines[len(lines)-1])
	}
	p.lastRender = time.Now()
}

// Finish clears any terminal progress block.
func (p *Progress) Finish() {
	if p == nil || p.total <= 0 || !p.terminal {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.lastLines > 0 {
		_, _ = fmt.Fprintln(p.w)
	}
	p.lastLen = 0
	p.lastLines = 0
}

func (p *Progress) renderLocked(item string, finished bool) {
	if p.total <= 0 {
		return
	}
	if !p.terminal {
		if !finished {
			return
		}
		st := p.stateForLocked(item)
		_, _ = fmt.Fprintf(
			p.w,
			"%s %d/%d: %s %s/%s %s\n",
			p.label,
			p.completed,
			p.total,
			NormalizeDisplayImage(item),
			HumanizeBytes(st.complete),
			HumanizeBytes(st.total),
			st.stage,
		)
		return
	}

	if !finished && time.Since(p.lastRender) < minRenderInterval {
		return
	}
	p.lastRender = time.Now()

	lines := p.formatLinesLocked()
	if p.width > 0 {
		for i, line := range lines {
			lines[i] = terminal.Truncate(line, p.width-1)
		}
	}
	if p.lastLines > 0 {
		_ = terminal.ClearBlock(p.w, p.lastLines)
	}
	for i, line := range lines {
		if i > 0 {
			_, _ = fmt.Fprint(p.w, "\n")
		}
		_, _ = fmt.Fprint(p.w, line)
	}
	p.lastLines = len(lines)
	if len(lines) > 0 {
		p.lastLen = len(lines[len(lines)-1])
	}
}

func (p *Progress) formatLinesLocked() []string {
	barWidth := 18
	lines := []string{
		fmt.Sprintf(
			"%s [%s] %d/%d",
			p.label,
			renderBar(int64(p.completed), int64(p.total), barWidth),
			p.completed,
			p.total,
		),
	}
	for _, item := range p.active {
		st := p.stateForLocked(item)
		lines = append(lines, fmt.Sprintf(
			"%s [%s] %s/%s %s",
			NormalizeDisplayImage(item),
			renderBar(st.complete, st.total, barWidth),
			HumanizeBytes(st.complete),
			HumanizeBytes(st.total),
			st.stage,
		))
	}
	return lines
}

func renderBar(complete, total int64, width int) string {
	if width <= 0 {
		return ""
	}
	if total <= 0 {
		return strings.Repeat("-", width)
	}
	filled := int((complete * int64(width)) / total)
	if filled < 0 {
		filled = 0
	}
	if filled >= width {
		return strings.Repeat("=", width)
	}
	return strings.Repeat("=", filled) + ">" + strings.Repeat("-", width-filled-1)
}

func (p *Progress) ensureStateLocked(item string) *imageState {
	if p.states == nil {
		p.states = make(map[string]*imageState)
	}
	st := p.states[item]
	if st == nil {
		st = &imageState{}
		p.states[item] = st
	}
	return st
}

func (p *Progress) stateForLocked(item string) *imageState {
	if p == nil {
		return &imageState{}
	}
	st := p.states[item]
	if st == nil {
		return &imageState{}
	}
	return st
}

func (p *Progress) addActiveLocked(item string) {
	for _, active := range p.active {
		if active == item {
			return
		}
	}
	p.active = append(p.active, item)
}

func (p *Progress) removeActiveLocked(item string) {
	for i, active := range p.active {
		if active == item {
			p.active = append(p.active[:i], p.active[i+1:]...)
			return
		}
	}
}
