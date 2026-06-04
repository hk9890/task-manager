// Command atctl is the command-line front-end for agent-tasks, a file-based
// task tracker. It is a thin wrapper over the tasks SDK, which owns all file
// access and validation.
package main

import "github.com/hk9890/agent-tasks/cmd"

func main() {
	cmd.Execute()
}
