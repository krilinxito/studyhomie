package update

import (
	"errors"
	"os"
	"os/exec"
	"os/signal"
)

// Restart runs exe again with the same arguments. Windows cannot replace a running process, so
// a new one is started and this one waits for it and exits with its code: the console stays
// attached and Ctrl+C reaches the new process.
func Restart(exe string) error {
	cmd := exec.Command(exe, os.Args[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.Env = append(os.Environ(), RestartedEnv+"=1")
	signal.Ignore(os.Interrupt) // the child handles Ctrl+C; this one just waits for it
	if err := cmd.Start(); err != nil {
		signal.Reset(os.Interrupt)
		return err
	}
	err := cmd.Wait()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		os.Exit(exit.ExitCode())
	}
	if err != nil {
		os.Exit(1)
	}
	os.Exit(0)
	return nil
}
