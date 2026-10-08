//go:build linux

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func configureTraining(c *exec.Cmd) { c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }
func stopTraining(c *exec.Cmd) {
	if c.Process != nil {
		_ = syscall.Kill(-c.Process.Pid, syscall.SIGTERM)
	}
}
func killTraining(c *exec.Cmd) {
	if c.Process != nil {
		_ = syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
	}
}
func lockTraining(dir string) (func(), error) {
	f, e := os.OpenFile(filepath.Join(dir, ".training.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	if e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		f.Close()
		return nil, e
	}
	return func() { syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close() }, nil
}
func launchTraining(cfg config, id string, resume bool, command string) error {
	exe, e := os.Executable()
	if e != nil {
		return e
	}
	unit := "campus-training-" + id
	if command != "" {
		unit += "-" + strings.NewReplacer(".", "-", ":", "-").Replace(command)
	}
	args := []string{"--user", "--collect", "--unit=" + unit, "--property=KillMode=control-group", exe, "--training-run", "--config", cfg.ConfigPath, "--id", id}
	if resume {
		args = append(args, "--resume", "--command", command)
	}
	c := exec.Command("systemd-run", args...)
	c.Stdout = nil
	c.Stderr = nil
	return c.Run()
}
func trainingResources(pid int, start time.Time) (float64, float64) {
	b, e := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if e != nil {
		return 0, 0
	}
	n := strings.LastIndexByte(string(b), ')')
	if n < 0 {
		return 0, 0
	}
	f := strings.Fields(string(b)[n+1:])
	if len(f) < 22 {
		return 0, 0
	}
	user, _ := strconv.ParseFloat(f[11], 64)
	system, _ := strconv.ParseFloat(f[12], 64)
	rss, _ := strconv.ParseFloat(f[21], 64)
	return (user + system) / 100 / time.Since(start).Seconds() * 100, rss * float64(os.Getpagesize()) / 1048576
}
