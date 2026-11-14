//go:build !windows

package update

import (
	"os"
	"syscall"
)

// Restart replaces the current process image with exe and the original args.
func Restart(exe string, args []string) error {
	return syscall.Exec(exe, append([]string{exe}, args...), os.Environ())
}
