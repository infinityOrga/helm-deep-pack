package push

import (
	"runtime"
)

func PushBinaryName() string {
	if runtime.GOOS == "windows" {
		return "push_images.exe"
	}
	return "push_images"
}
