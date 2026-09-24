package main

import (
	"context"
	"fmt"
	"os/exec"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// The Linux release binaries are built on ubuntu-22.04, so they need its glibc or newer.
const glibcMajor, glibcMinor = 2, 35

type archive struct {
	triple string
	name   string
	sha256 string
	// The executable's path inside the archive, below the one directory dist wraps unix
	// archives in.
	binary string
}

func (a archive) executable() string {
	return path.Base(a.binary)
}

func (a archive) stem() string {
	return strings.TrimSuffix(strings.TrimSuffix(a.name, ".zip"), ".tar.gz")
}

func (a archive) holds(entry string) bool {
	entry = strings.TrimPrefix(path.Clean(strings.ReplaceAll(entry, `\`, "/")), "./")
	return entry == a.binary || entry == a.stem()+"/"+a.binary
}

var glibcVersion = regexp.MustCompile(`(\d+)\.(\d+)\s*$`)

// hostProblem says why the Linux release binaries cannot run on a system whose `ldd --version`
// printed this, or returns "" when they can or when the output is not recognised.
func hostProblem(ldd string) string {
	if strings.Contains(strings.ToLower(ldd), "musl") {
		return "there is no prebuilt binary for musl Linux (Alpine and similar) yet"
	}
	first, _, _ := strings.Cut(strings.TrimSpace(ldd), "\n")
	match := glibcVersion.FindStringSubmatch(first)
	if match == nil {
		return ""
	}
	major, _ := strconv.Atoi(match[1])
	minor, _ := strconv.Atoi(match[2])
	if major > glibcMajor || (major == glibcMajor && minor >= glibcMinor) {
		return ""
	}
	return fmt.Sprintf(
		"the Linux binaries need glibc %d.%d or newer, and this system has %d.%d",
		glibcMajor, glibcMinor, major, minor,
	)
}

// musl's ldd prints its banner to stderr and exits 1, so the output counts whatever the status.
func lddVersion() string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, _ := exec.CommandContext(ctx, "ldd", "--version").CombinedOutput()
	return string(out)
}
