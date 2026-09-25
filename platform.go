package main

import (
	"context"
	"os/exec"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// The glibc Linux binaries are built on ubuntu-22.04, so they need its glibc or newer.
const glibcMajor, glibcMinor = 2, 35

// A Linux platform's static musl build is listed under the platform with this suffix.
const staticSuffix = "/musl"

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

// glibcBuildRuns says whether the glibc binary can run on a system whose `ldd --version` printed
// this. Whatever it cannot confirm, musl or output it does not recognise, gets the static build,
// which runs on any Linux.
func glibcBuildRuns(ldd string) bool {
	if strings.Contains(strings.ToLower(ldd), "musl") {
		return false
	}
	first, _, _ := strings.Cut(strings.TrimSpace(ldd), "\n")
	match := glibcVersion.FindStringSubmatch(first)
	if match == nil {
		return false
	}
	major, _ := strconv.Atoi(match[1])
	minor, _ := strconv.Atoi(match[2])
	return major > glibcMajor || (major == glibcMajor && minor >= glibcMinor)
}

// musl's ldd prints its banner to stderr and exits 1, so the output counts whatever the status.
func lddVersion() string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, _ := exec.CommandContext(ctx, "ldd", "--version").CombinedOutput()
	return string(out)
}
