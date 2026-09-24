//go:build windows

package terminal

import (
	"io"
	"os"

	"golang.org/x/sys/windows"
)

func enableANSIOutput(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return true
	}
	handle := windows.Handle(f.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(handle, &mode); err != nil {
		return false
	}
	mode |= windows.ENABLE_PROCESSED_OUTPUT | windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING
	return windows.SetConsoleMode(handle, mode) == nil
}
