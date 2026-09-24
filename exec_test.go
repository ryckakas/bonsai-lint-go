package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// With the helper variable set, the test binary stands in for bonsai-lint: it reports what it
// was started with and exits 3, a code the launcher must pass on untouched.
func TestMain(m *testing.M) {
	if os.Getenv("BONSAI_LINT_TEST_HELPER") == "1" {
		in, _ := io.ReadAll(os.Stdin)
		name := strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe")
		fmt.Printf("argv0=%s args=%s stdin=%s\n", name, strings.Join(os.Args[1:], "|"), in)
		os.Exit(3)
	}
	os.Exit(m.Run())
}

func TestTheLauncherIsTransparent(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the launcher")
	}
	launcher := filepath.Join(t.TempDir(), "bonsai-lint")
	if runtime.GOOS == "windows" {
		launcher += ".exe"
	}
	if out, err := exec.Command("go", "build", "-o", launcher, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	helper, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	command := exec.Command(launcher, "--format", "json", "src dir")
	command.Env = append(os.Environ(), "BONSAI_LINT_BINARY="+helper, "BONSAI_LINT_TEST_HELPER=1")
	command.Stdin = strings.NewReader("buffer")
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err = command.Run()

	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 3 {
		t.Fatalf("exit: %v", err)
	}
	if want := "argv0=bonsai-lint args=--format|json|src dir stdin=buffer\n"; stdout.String() != want {
		t.Fatalf("stdout = %q, want %q", stdout.String(), want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("the launcher wrote to stderr: %q", stderr.String())
	}
}
