//go:build !linux

package main

import (
	"errors"
	"os/exec"
	"time"
)

func configureTraining(c *exec.Cmd) {}
func stopTraining(c *exec.Cmd) {
	if c.Process != nil {
		c.Process.Kill()
	}
}
func killTraining(c *exec.Cmd)                { stopTraining(c) }
func lockTraining(dir string) (func(), error) { return func() {}, nil }
func launchTraining(config, string, bool, string) error {
	return errors.New("managed_training_requires_linux")
}
func trainingResources(int, time.Time) (float64, float64) { return 0, 0 }
