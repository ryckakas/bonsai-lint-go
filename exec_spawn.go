//go:build !unix

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
)

// Windows has no exec, so the binary runs as a child. Ctrl+C reaches every process on the
// console; the launcher lets the child handle it and stays alive to pass on its exit code.
func run(binary string, args []string) int {
	signal.Notify(make(chan os.Signal, 1), os.Interrupt)

	command := exec.Command(binary, args...)
	command.Args[0] = "bonsai-lint"
	command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr

	err := command.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &exit):
		if code := exit.ExitCode(); code >= 0 {
			return code
		}
		return 1
	default:
		fmt.Fprintf(os.Stderr, "bonsai-lint: cannot run %s: %v\n", binary, err)
		return 1
	}
}
