// SPDX-License-Identifier: MIT

//go:build unix

package host

import (
	"os"
	"runtime"
	"syscall"
)

// detachedAttr returns the process attributes of a program that runs on
// its own (actions.md B112): a session of its own, so that a signal to the
// core, such as Ctrl+C in its terminal, does not reach the program. The
// option "show window" has no effect here (B116): programs with a window
// show it anyway, others run without a terminal.
func detachedAttr(bool) *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}

// waitedAttr returns the process attributes of a program the action waits
// for: a process group of its own, so that kill ends the programs it
// started as well (B115).
func waitedAttr(bool) *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}

// kill ends the process group of p.
func kill(p *os.Process) error {
	return syscall.Kill(-p.Pid, syscall.SIGKILL)
}

// openCommand returns the program and arguments that open path with the
// program the system assigns to it (B116).
func openCommand(path string) (string, []string) {
	if runtime.GOOS == "darwin" {
		return "open", []string{path}
	}
	return "xdg-open", []string{path}
}
