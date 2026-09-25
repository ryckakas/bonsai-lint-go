package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

const glibc235 = "ldd (Ubuntu GLIBC 2.35-0ubuntu3.8) 2.35\nCopyright (C) 2022 Free Software Foundation, Inc.\n"

var fakeBinary = []byte("#!/bin/sh\necho fake bonsai-lint\n")

type fixture struct {
	t        *testing.T
	server   *httptest.Server
	requests atomic.Int64
	files    map[string][]byte
	env      map[string]string
	stderr   lockedBuffer
	ldd      string
	lddCalls atomic.Int64
}

// Concurrent resolves stand in for concurrent processes, which each have their own stderr.
type lockedBuffer struct {
	mu sync.Mutex
	bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.Write(p)
}

func newFixture(t *testing.T) *fixture {
	f := &fixture{t: t, files: map[string][]byte{}, ldd: glibc235}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.requests.Add(1)
		body, ok := f.files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(body)
	}))
	t.Cleanup(f.server.Close)
	f.env = map[string]string{
		"BONSAI_LINT_CACHE":        t.TempDir(),
		"BONSAI_LINT_DOWNLOAD_URL": f.server.URL + "/releases/v9.9.9",
	}
	return f
}

// serve publishes an archive and returns the entry a release.go would hold for it.
func (f *fixture) serve(triple, name, binary string, body []byte) archive {
	f.files["/releases/v9.9.9/"+name] = body
	sum := sha256.Sum256(body)
	return archive{triple: triple, name: name, sha256: hex.EncodeToString(sum[:]), binary: binary}
}

func (f *fixture) launcher(platform string, entries map[string]archive) *launcher {
	return &launcher{
		version:      "9.9.9",
		archives:     entries,
		platform:     platform,
		getenv:       func(key string) string { return f.env[key] },
		userCacheDir: func() (string, error) { return "", errors.New("no home") },
		ldd:          func() string { f.lddCalls.Add(1); return f.ldd },
		client:       f.server.Client(),
		stderr:       &f.stderr,
	}
}

func tarGz(t *testing.T, entries map[string][]byte) []byte {
	var out bytes.Buffer
	zipped := gzip.NewWriter(&out)
	writer := tar.NewWriter(zipped)
	for name, body := range entries {
		header := &tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}
		if err := writer.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		writer.Write(body)
	}
	writer.Close()
	zipped.Close()
	return out.Bytes()
}

func zipped(t *testing.T, entries map[string][]byte) []byte {
	var out bytes.Buffer
	writer := zip.NewWriter(&out)
	for name, body := range entries {
		file, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		file.Write(body)
	}
	writer.Close()
	return out.Bytes()
}

// A dist unix archive: the binary and the docs inside one directory named after the archive.
func distTarGz(t *testing.T, triple string) []byte {
	stem := "bonsai-lint-" + triple
	return tarGz(t, map[string][]byte{
		stem + "/bonsai-lint":  fakeBinary,
		stem + "/README.md":    []byte("readme"),
		stem + "/CHANGELOG.md": []byte("changelog"),
	})
}

func linuxRelease(f *fixture) map[string]archive {
	triple := "x86_64-unknown-linux-gnu"
	entry := f.serve(triple, "bonsai-lint-"+triple+".tar.gz", "bonsai-lint", distTarGz(f.t, triple))
	return map[string]archive{"linux/amd64": entry}
}

// Both Linux builds, as a release publishes them.
func linuxReleases(f *fixture) map[string]archive {
	entries := linuxRelease(f)
	triple := "x86_64-unknown-linux-musl"
	entries["linux/amd64/musl"] = f.serve(triple, "bonsai-lint-"+triple+".tar.gz", "bonsai-lint", distTarGz(f.t, triple))
	return entries
}

func tripleOf(binary string) string {
	return filepath.Base(filepath.Dir(binary))
}

func TestTheFirstRunDownloadsVerifiesAndCaches(t *testing.T) {
	f := newFixture(t)
	l := f.launcher("linux/amd64", linuxRelease(f))

	binary, err := l.resolve()
	if err != nil {
		t.Fatal(err)
	}

	want := filepath.Join(f.env["BONSAI_LINT_CACHE"], "9.9.9", "x86_64-unknown-linux-gnu", "bonsai-lint")
	if binary != want {
		t.Fatalf("binary = %s, want %s", binary, want)
	}
	if got, _ := os.ReadFile(binary); !bytes.Equal(got, fakeBinary) {
		t.Fatalf("installed %q", got)
	}
	if info, _ := os.Stat(binary); runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("not executable: %v", info.Mode())
	}
	if !strings.Contains(f.stderr.String(), "downloading v9.9.9 for linux/amd64") {
		t.Fatalf("stderr = %q", f.stderr.String())
	}
	assertNoLeftovers(t, filepath.Dir(binary))
}

func TestACachedBinaryRunsWithoutTheNetworkOrAMessage(t *testing.T) {
	f := newFixture(t)
	l := f.launcher("linux/amd64", linuxRelease(f))
	if _, err := l.resolve(); err != nil {
		t.Fatal(err)
	}
	f.stderr.Reset()
	before := f.requests.Load()

	if _, err := l.resolve(); err != nil {
		t.Fatal(err)
	}
	if f.requests.Load() != before || f.stderr.Len() != 0 {
		t.Fatalf("second run: %d requests, stderr %q", f.requests.Load()-before, f.stderr.String())
	}
}

func TestAnArchiveThatFailsItsChecksumInstallsNothing(t *testing.T) {
	f := newFixture(t)
	entries := linuxRelease(f)
	entry := entries["linux/amd64"]
	f.files["/releases/v9.9.9/"+entry.name] = distTarGz(t, "tampered")

	_, err := f.launcher("linux/amd64", entries).resolve()

	if err == nil || !strings.Contains(err.Error(), "does not match its published checksum") {
		t.Fatalf("err = %v", err)
	}
	dir := filepath.Join(f.env["BONSAI_LINT_CACHE"], "9.9.9", entry.triple)
	if isFile(filepath.Join(dir, "bonsai-lint")) {
		t.Fatal("a tampered archive was installed")
	}
	assertNoLeftovers(t, dir)
}

func TestTheWindowsZipIsFlat(t *testing.T) {
	f := newFixture(t)
	triple := "x86_64-pc-windows-msvc"
	body := zipped(t, map[string][]byte{"bonsai-lint.exe": fakeBinary, "README.md": []byte("readme")})
	entry := f.serve(triple, "bonsai-lint-"+triple+".zip", "bonsai-lint.exe", body)

	binary, err := f.launcher("windows/amd64", map[string]archive{"windows/amd64": entry}).resolve()

	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(binary) != "bonsai-lint.exe" {
		t.Fatalf("binary = %s", binary)
	}
	if got, _ := os.ReadFile(binary); !bytes.Equal(got, fakeBinary) {
		t.Fatalf("installed %q", got)
	}
}

func TestATarGzWithoutItsTopDirectoryStillWorks(t *testing.T) {
	f := newFixture(t)
	triple := "aarch64-apple-darwin"
	body := tarGz(t, map[string][]byte{"bonsai-lint": fakeBinary})
	entry := f.serve(triple, "bonsai-lint-"+triple+".tar.gz", "bonsai-lint", body)

	if _, err := f.launcher("darwin/arm64", map[string]archive{"darwin/arm64": entry}).resolve(); err != nil {
		t.Fatal(err)
	}
}

func TestAnArchiveWithoutTheBinaryIsAnError(t *testing.T) {
	f := newFixture(t)
	triple := "x86_64-apple-darwin"
	body := tarGz(t, map[string][]byte{"bonsai-lint-" + triple + "/README.md": []byte("readme")})
	entry := f.serve(triple, "bonsai-lint-"+triple+".tar.gz", "bonsai-lint", body)

	_, err := f.launcher("darwin/amd64", map[string]archive{"darwin/amd64": entry}).resolve()

	if err == nil || !strings.Contains(err.Error(), "holds no bonsai-lint") {
		t.Fatalf("err = %v", err)
	}
}

func TestConcurrentFirstRunsAllSucceed(t *testing.T) {
	f := newFixture(t)
	l := f.launcher("linux/amd64", linuxRelease(f))

	var wg sync.WaitGroup
	paths := make([]string, 8)
	errs := make([]error, 8)
	for i := range paths {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			paths[i], errs[i] = l.resolve()
		}(i)
	}
	wg.Wait()

	for i := range paths {
		if errs[i] != nil || paths[i] != paths[0] {
			t.Fatalf("run %d: %s, %v", i, paths[i], errs[i])
		}
	}
	if got, _ := os.ReadFile(paths[0]); !bytes.Equal(got, fakeBinary) {
		t.Fatalf("installed %q", got)
	}
	assertNoLeftovers(t, filepath.Dir(paths[0]))
}

func TestBinaryOverrideSkipsTheDownloadEvenUnreleased(t *testing.T) {
	f := newFixture(t)
	f.env["BONSAI_LINT_BINARY"] = "/opt/bonsai-lint"
	l := f.launcher("plan9/amd64", nil)
	l.version = ""

	binary, err := l.resolve()

	if err != nil || binary != "/opt/bonsai-lint" || f.requests.Load() != 0 {
		t.Fatalf("binary %s, err %v, %d requests", binary, err, f.requests.Load())
	}
}

func TestAnUnreleasedLauncherSaysSo(t *testing.T) {
	f := newFixture(t)
	l := f.launcher("linux/amd64", nil)
	l.version = ""

	_, err := l.resolve()

	if err == nil || !strings.Contains(err.Error(), "unreleased build") {
		t.Fatalf("err = %v", err)
	}
}

func TestAnUnsupportedPlatformNamesItselfAndTheFallback(t *testing.T) {
	f := newFixture(t)

	_, err := f.launcher("plan9/amd64", linuxRelease(f)).resolve()

	if err == nil || !strings.Contains(err.Error(), "plan9/amd64") ||
		!strings.Contains(err.Error(), "cargo install bonsai-lint") {
		t.Fatalf("err = %v", err)
	}
	if f.requests.Load() != 0 {
		t.Fatal("downloaded for an unsupported platform")
	}
}

func TestCurrentGlibcGetsTheGlibcBuild(t *testing.T) {
	f := newFixture(t)

	binary, err := f.launcher("linux/amd64", linuxReleases(f)).resolve()

	if err != nil || tripleOf(binary) != "x86_64-unknown-linux-gnu" {
		t.Fatalf("binary %s, err %v", binary, err)
	}
}

func TestMuslOldGlibcAndAnUnrecognisedLddGetTheStaticBuild(t *testing.T) {
	for _, ldd := range []string{
		"musl libc (x86_64)\nVersion 1.2.4\nDynamic Program Loader\n",
		"ldd (Debian GLIBC 2.31-13+deb11u11) 2.31\n",
		"",
	} {
		f := newFixture(t)
		f.ldd = ldd

		binary, err := f.launcher("linux/amd64", linuxReleases(f)).resolve()

		if err != nil || tripleOf(binary) != "x86_64-unknown-linux-musl" {
			t.Fatalf("ldd %q: binary %s, err %v", ldd, binary, err)
		}
		if !strings.Contains(f.stderr.String(), "downloading v9.9.9 for linux/amd64/musl") {
			t.Fatalf("ldd %q: stderr %q", ldd, f.stderr.String())
		}
	}
}

func TestACachedBuildStartsWithoutAskingLdd(t *testing.T) {
	for _, ldd := range []string{glibc235, "musl libc (x86_64)\nVersion 1.2.4\n"} {
		f := newFixture(t)
		f.ldd = ldd
		l := f.launcher("linux/amd64", linuxReleases(f))
		first, err := l.resolve()
		if err != nil {
			t.Fatal(err)
		}
		asked := f.lddCalls.Load()

		again, err := l.resolve()

		if err != nil || again != first || f.lddCalls.Load() != asked {
			t.Fatalf("ldd %q: %s, then %s (err %v), asking ldd %d more times",
				ldd, first, again, err, f.lddCalls.Load()-asked)
		}
	}
}

func TestACacheHoldingBothBuildsStartsTheStaticOne(t *testing.T) {
	f := newFixture(t)
	for _, triple := range []string{"x86_64-unknown-linux-gnu", "x86_64-unknown-linux-musl"} {
		dir := filepath.Join(f.env["BONSAI_LINT_CACHE"], "9.9.9", triple)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "bonsai-lint"), fakeBinary, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	binary, err := f.launcher("linux/amd64", linuxReleases(f)).resolve()

	if err != nil || tripleOf(binary) != "x86_64-unknown-linux-musl" || f.requests.Load() != 0 {
		t.Fatalf("binary %s, err %v, %d requests", binary, err, f.requests.Load())
	}
}

func TestWithoutAStaticBuildMuslAndOldGlibcAreRefused(t *testing.T) {
	for _, ldd := range []string{
		"musl libc (x86_64)\nVersion 1.2.4\nDynamic Program Loader\n",
		"ldd (Debian GLIBC 2.31-13+deb11u11) 2.31\n",
	} {
		f := newFixture(t)
		f.ldd = ldd

		_, err := f.launcher("linux/amd64", linuxRelease(f)).resolve()

		if err == nil || !strings.Contains(err.Error(), "cargo install bonsai-lint") || f.requests.Load() != 0 {
			t.Fatalf("ldd %q: err %v, %d requests", ldd, err, f.requests.Load())
		}
	}
}

func TestAMirrorReplacesTheGitHubURL(t *testing.T) {
	f := newFixture(t)
	entries := linuxRelease(f)
	name := entries["linux/amd64"].name
	f.files["/mirror/"+name] = f.files["/releases/v9.9.9/"+name]
	f.env["BONSAI_LINT_DOWNLOAD_URL"] = f.server.URL + "/mirror/"

	if _, err := f.launcher("linux/amd64", entries).resolve(); err != nil {
		t.Fatal(err)
	}
}

func TestADownloadErrorNamesTheURLAndStatus(t *testing.T) {
	f := newFixture(t)
	entries := linuxRelease(f)
	f.files = map[string][]byte{}

	_, err := f.launcher("linux/amd64", entries).resolve()

	if err == nil || !strings.Contains(err.Error(), "404") || !strings.Contains(err.Error(), entries["linux/amd64"].name) {
		t.Fatalf("err = %v", err)
	}
}

func TestWithoutACacheDirectoryTheVariableIsNamed(t *testing.T) {
	f := newFixture(t)
	delete(f.env, "BONSAI_LINT_CACHE")

	_, err := f.launcher("linux/amd64", linuxRelease(f)).resolve()

	if err == nil || !strings.Contains(err.Error(), "BONSAI_LINT_CACHE") {
		t.Fatalf("err = %v", err)
	}
}

func TestGlibcBuildRunsReadsLddVersions(t *testing.T) {
	for _, c := range []struct {
		ldd  string
		runs bool
	}{
		{glibc235, true},
		{"ldd (Debian GLIBC 2.36-9+deb12u10) 2.36\n", true},
		{"ldd (GNU libc) 2.39\n", true},
		{"ldd (GNU libc) 3.0\n", true},
		{"ldd (Debian GLIBC 2.31-13+deb11u11) 2.31\n", false},
		{"ldd (GNU libc) 2.28\n", false},
		{"ldd (GNU libc) 2.26\n", false},
		{"musl libc (aarch64)\nVersion 1.2.5\n", false},
		{"", false},
		{"something else entirely\n", false},
	} {
		if got := glibcBuildRuns(c.ldd); got != c.runs {
			t.Errorf("glibcBuildRuns(%q) = %v, want %v", c.ldd, got, c.runs)
		}
	}
}

func assertNoLeftovers(t *testing.T, dir string) {
	t.Helper()
	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			t.Errorf("temporary file left behind: %s", entry.Name())
		}
	}
}
