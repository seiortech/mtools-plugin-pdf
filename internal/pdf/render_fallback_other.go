//go:build !windows

package pdf

import "os/exec"

func setCmdHideWindow(cmd *exec.Cmd) {}
