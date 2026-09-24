//go:build !windows

package terminal

import "io"

func enableANSIOutput(io.Writer) bool { return true }
