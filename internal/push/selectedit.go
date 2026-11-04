package push

import (
	"bufio"
	"fmt"
	"helm-deep-pack/internal/pushspec"
	"unicode/utf8"
)

func (m *selectModel) startEdit() {
	if m.itemCount() == 0 {
		return
	}
	if m.onChart() {
		m.editing, m.editBuf, m.editErr = true, m.chart.Bundle.targetPath(), ""
		return
	}
	target := pushspec.TargetRepository(m.images[m.imageIndex(m.cursor)].Spec.Target)
	m.editing, m.editBuf, m.editErr = true, target, ""
}

func (m *selectModel) chartRef(path string) string {
	return path + ":" + m.chart.Bundle.tag
}

// acceptEdit ends the edit, or keeps it open showing conflict when non-empty.
func (m *selectModel) acceptEdit(conflict string) bool {
	if conflict != "" {
		m.editErr = conflict
		return false
	}
	m.editing = false
	return true
}

func (m *selectModel) saveChartEdit(reclassifyChart func(*chartBundle) classifiedChart) {
	path, err := pushspec.ValidateTargetPath(m.editBuf)
	if err != nil {
		m.editErr = err.Error()
		return
	}
	if path == m.chart.Bundle.targetPath() {
		m.editing = false
		return
	}
	ref := m.chartRef(path)
	conflict := ""
	for _, image := range m.images {
		if image.Spec.Target == ref {
			conflict = fmt.Sprintf("duplicate target %s (chart and %s)", ref, image.Spec.Image)
			break
		}
	}
	if !m.acceptEdit(conflict) {
		return
	}
	bundle := *m.chart.Bundle
	bundle.target = path
	chart := reclassifyChart(&bundle)
	m.chart, m.chartTarget = &chart, path
}

func (m *selectModel) saveEdit(reclassify func(pushspec.ArchiveSpec) classifiedImage, reclassifyChart func(*chartBundle) classifiedChart) {
	if m.onChart() {
		m.saveChartEdit(reclassifyChart)
		return
	}
	index := m.imageIndex(m.cursor)
	spec, err := pushspec.Retarget(m.images[index].Spec, m.editBuf)
	if err != nil {
		m.editErr = err.Error()
		return
	}
	if spec.Target == m.images[index].Spec.Target {
		m.editing = false
		return
	}
	specs := make([]pushspec.ArchiveSpec, len(m.images))
	for i, image := range m.images {
		specs[i] = image.Spec
	}
	specs[index] = spec
	conflict := ""
	if a, b, found := pushspec.DuplicateTarget(specs); found {
		conflict = fmt.Sprintf("duplicate target %s (%s and %s)", a.Target, a.Image, b.Image)
	} else if m.chart != nil && spec.Target == m.chartRef(m.chart.Bundle.targetPath()) {
		conflict = fmt.Sprintf("duplicate target %s (chart and %s)", spec.Target, spec.Image)
	}
	if !m.acceptEdit(conflict) {
		return
	}
	m.images[index] = reclassify(spec)
	m.retargeted = true
}

func (m *selectModel) editKey(b byte, r *bufio.Reader, reclassify func(pushspec.ArchiveSpec) classifiedImage, reclassifyChart func(*chartBundle) classifiedChart) (cancel bool) {
	switch b {
	case 0x03:
		return true
	case '\r', '\n':
		m.saveEdit(reclassify, reclassifyChart)
	case 0x1b:
		if r.Buffered() == 0 {
			m.editing = false
			return false
		}
		if next, _ := r.ReadByte(); next == '[' || next == 'O' {
			_, _ = r.ReadByte()
		}
	case 0x7f, 0x08:
		if m.editBuf != "" {
			_, size := utf8.DecodeLastRuneInString(m.editBuf)
			m.editBuf = m.editBuf[:len(m.editBuf)-size]
		}
	default:
		if b >= 0x20 {
			m.editBuf += string([]byte{b})
		}
	}
	return false
}
