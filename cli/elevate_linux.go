//go:build linux

package cli

import (
	"os"
	"os/exec"
)

// maybeElevate re-executes mkwfc with administrator rights when a desktop user
// starts the server without the privileges Linux requires to bind 53/80/443.
// It returns (exitCode, true) when it handled the launch by elevating (the
// caller exits with that code) or (0, false) to keep running unprivileged.
//
// A graphical password prompt (pkexec) is preferred so a double-clicked launch
// works; a terminal launch falls back to sudo. The euid==0 guard stops any
// re-exec loop even though pkexec deliberately scrubs the environment, so the
// MKWFC_ELEVATED marker is only a secondary signal.
func maybeElevate(args []string) (int, bool) {
	if os.Geteuid() == 0 || os.Getenv("MKWFC_ELEVATED") != "" || !wantsRun(args) {
		return 0, false
	}
	exe, err := os.Executable()
	if err != nil {
		return 0, false
	}

	graphical := os.Getenv("DISPLAY") != "" || os.Getenv("WAYLAND_DISPLAY") != ""
	var elevator string
	if path, err := exec.LookPath("pkexec"); err == nil && graphical {
		elevator = path
	} else if path, err := exec.LookPath("sudo"); err == nil {
		elevator = path
	} else {
		// No way to elevate; let the normal path run and surface the clear
		// "run as root" bind error with instructions.
		return 0, false
	}

	forwarded := append([]string{exe}, args[1:]...)
	cmd := exec.Command(elevator, forwarded...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.Env = append(os.Environ(), "MKWFC_ELEVATED=1")
	if err := cmd.Run(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			return exit.ExitCode(), true
		}
		return 1, true
	}
	return 0, true
}

// wantsRun reports whether these process arguments start the server: either an
// explicit "run" command or a bare double-click launch with no subcommand.
func wantsRun(args []string) bool {
	if len(args) <= 1 {
		return true
	}
	return args[1] == "run"
}
