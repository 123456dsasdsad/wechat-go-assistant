//go:build !linux

package codex

import "os/exec"

func configureProcess(c *exec.Cmd) {}
func terminateProcess(c *exec.Cmd) {
	if c.Process != nil {
		_ = c.Process.Kill()
	}
}
