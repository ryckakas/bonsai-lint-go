//go:build unix

package main

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// The binary replaces this process, so signals, stdio and the exit status are its own.
func run(binary string, args []string) int {
	err := syscall.Exec(binary, append([]string{"bonsai-lint"}, args...), os.Environ())
	if errors.Is(err, syscall.ENOENT) && isFile(binary) {
		err = fmt.Errorf("%w (its dynamic loader is missing; is this a musl system?)", err)
	}
	fmt.Fprintf(os.Stderr, "bonsai-lint: cannot run %s: %v\n", binary, err)
	return 1
}
