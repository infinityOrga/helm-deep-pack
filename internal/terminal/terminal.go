package terminal

import (
	"io"
	"os"

	"golang.org/x/term"
)

// IsWriter reports whether w is a terminal-backed writer.
// Kept as a var so tests can override terminal detection.
var IsWriter = func(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// IsReader reports whether r is a terminal-backed reader.
// Kept as a var so tests can override terminal detection.
var IsReader = func(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// Size returns terminal width and height for w, or zeros when unavailable.
func Size(w io.Writer) (int, int) {
	f, ok := w.(*os.File)
	if !ok {
		return 0, 0
	}
	width, height, err := term.GetSize(int(f.Fd()))
	if err != nil {
		return 0, 0
	}
	return width, height
}

// Width returns terminal width for w, or 0 when unavailable.
func Width(w io.Writer) int {
	width, _ := Size(w)
	return width
}

// Truncate shortens line to width bytes when width is positive.
func Truncate(line string, width int) string {
	if width <= 0 {
		return line
	}
	if len(line) <= width {
		return line
	}
	return line[:width]
}

// WriteString writes a string to w.
func WriteString(w io.Writer, value string) error {
	_, err := io.WriteString(w, value)
	return err
}

// ClearBlock clears an N-line terminal block in-place.
func ClearBlock(w io.Writer, lines int) error {
	if lines <= 0 {
		return nil
	}
	if err := WriteString(w, "\r\x1b[2K"); err != nil {
		return err
	}
	for i := 1; i < lines; i++ {
		if err := WriteString(w, "\x1b[1A\r\x1b[2K"); err != nil {
			return err
		}
	}
	return nil
}
