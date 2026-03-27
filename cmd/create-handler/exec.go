package main

import "os/exec"

// runOSExec executes a binary with the given args and env using os/exec.
// Called by execRunCommand in main.go; separated to keep os/exec out of
// the main file and enable clean test injection via RunCommandFunc.
func runOSExec(cmd string, args []string, env []string) ([]byte, error) {
	c := exec.Command(cmd, args...)
	c.Env = env
	return c.CombinedOutput()
}
