// SPDX-License-Identifier: MIT

//go:build windows

package host

import (
	"os"
	"syscall"

	"golang.org/x/sys/windows"
)

// windowFlags returns the process attributes for the option "show window"
// (actions.md B116): without it, the program gets no console window.
func windowFlags(show bool, flags uint32) *syscall.SysProcAttr {
	if !show {
		flags |= windows.CREATE_NO_WINDOW
	}
	return &syscall.SysProcAttr{HideWindow: !show, CreationFlags: flags}
}

// detachedAttr returns the process attributes of a program that runs on
// its own (B112): a process group of its own, so that Ctrl+C in the
// terminal of the core does not reach it.
func detachedAttr(show bool) *syscall.SysProcAttr {
	return windowFlags(show, windows.CREATE_NEW_PROCESS_GROUP)
}

// waitedAttr returns the process attributes of a program the action waits
// for.
func waitedAttr(show bool) *syscall.SysProcAttr {
	return windowFlags(show, 0)
}

// kill ends p. Programs it started keep running; Windows would need a job
// object to end them too.
func kill(p *os.Process) error {
	return p.Kill()
}

// openCommand returns the program and arguments that open path with the
// program the system assigns to it (B116), without a shell.
func openCommand(path string) (string, []string) {
	return "rundll32.exe", []string{"url.dll,FileProtocolHandler", path}
}
