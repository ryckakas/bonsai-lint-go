// Command bonsai-lint runs the bonsai-lint cognitive complexity linter from the Go toolchain.
//
//	go install bonsai.kauneckas.dev/bonsai-lint@latest
//	go get -tool bonsai.kauneckas.dev/bonsai-lint@latest
//
// bonsai-lint is written in Rust. This command downloads the prebuilt release binary that matches
// its own version, checks it against a checksum recorded in this module's source (and so covered by
// Go's checksum database), caches it and runs it with the arguments it was given.
//
// See https://bonsai.kauneckas.dev for the linter itself.
package main

import (
	"fmt"
	"net/http"
	"os"
	"runtime"
	"time"
)

func main() {
	l := &launcher{
		version:      version,
		archives:     archives,
		platform:     runtime.GOOS + "/" + runtime.GOARCH,
		getenv:       os.Getenv,
		userCacheDir: os.UserCacheDir,
		ldd:          lddVersion,
		client:       &http.Client{Timeout: 5 * time.Minute},
		stderr:       os.Stderr,
	}
	binary, err := l.resolve()
	if err != nil {
		fmt.Fprintf(os.Stderr, "bonsai-lint: %v\n", err)
		os.Exit(1)
	}
	os.Exit(run(binary, os.Args[1:]))
}
