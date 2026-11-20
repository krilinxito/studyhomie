//go:build !windows

package update

import (
	"os"
	"syscall"
)

// Restart replaces this process with exe, keeping the arguments, the terminal and the PID.
func Restart(exe string) error {
	return syscall.Exec(exe, append([]string{exe}, os.Args[1:]...), append(os.Environ(), RestartedEnv+"=1"))
}
