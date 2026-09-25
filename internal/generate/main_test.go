package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func load(t *testing.T, name string) manifest {
	t.Helper()
	text, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	var m manifest
	if err := json.Unmarshal(text, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// The last release before the archive switch: its .tar.xz archives are exactly what the
// launcher cannot open, so the generator must refuse rather than publish a broken version.
func TestAnXzReleaseIsRefused(t *testing.T) {
	_, err := fromManifest(filepath.Join("testdata", "dist-manifest-0.2.1.json"))

	if err == nil || !strings.Contains(err.Error(), "needs a .tar.gz archive") {
		t.Fatalf("err = %v", err)
	}
}

func TestEveryPlatformGetsItsArchiveAndChecksum(t *testing.T) {
	source, err := fromManifest(filepath.Join("testdata", "dist-manifest-targz.json"))
	if err != nil {
		t.Fatal(err)
	}
	golden, err := os.ReadFile(filepath.Join("testdata", "release.golden"))
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		err = os.WriteFile(filepath.Join("testdata", "release.golden"), source, 0o644)
	}
	if err != nil {
		t.Fatal(err)
	}
	if string(source) != string(golden) && os.Getenv("UPDATE_GOLDEN") != "1" {
		t.Fatalf("generated source differs from testdata/release.golden:\n%s", source)
	}
	for platform := range platforms {
		if !strings.Contains(string(source), `"`+platform+`"`) {
			t.Errorf("no entry for %s", platform)
		}
	}
}

func TestWindowsOnArmGetsTheX64Archive(t *testing.T) {
	_, entries, err := archives(load(t, "dist-manifest-targz.json"))
	if err != nil {
		t.Fatal(err)
	}
	byPlatform := map[string]entry{}
	for _, e := range entries {
		byPlatform[e.platform] = e
	}
	arm, x64 := byPlatform["windows/arm64"], byPlatform["windows/amd64"]
	arm.platform, x64.platform = "", ""

	if arm != x64 || x64.name != "bonsai-lint-x86_64-pc-windows-msvc.zip" {
		t.Fatalf("windows/arm64 = %+v, windows/amd64 = %+v", arm, x64)
	}
}

func TestEachLinuxPlatformAlsoGetsItsStaticBuild(t *testing.T) {
	_, entries, err := archives(load(t, "dist-manifest-targz.json"))
	if err != nil {
		t.Fatal(err)
	}
	byPlatform := map[string]entry{}
	for _, e := range entries {
		byPlatform[e.platform] = e
	}
	for platform, triple := range map[string]string{
		"linux/amd64/musl": "x86_64-unknown-linux-musl",
		"linux/arm64/musl": "aarch64-unknown-linux-musl",
	} {
		if got := byPlatform[platform]; got.triple != triple || got.name != "bonsai-lint-"+triple+".tar.gz" {
			t.Errorf("%s = %+v", platform, got)
		}
	}
}

func TestTheVersionMustBeTheTag(t *testing.T) {
	m := load(t, "dist-manifest-targz.json")
	m.AnnouncementTag = "v9.9.9"

	if _, _, err := archives(m); err == nil || !strings.Contains(err.Error(), "does not match version") {
		t.Fatalf("err = %v", err)
	}
}

func TestAMissingPlatformIsAnError(t *testing.T) {
	m := load(t, "dist-manifest-targz.json")
	delete(m.Artifacts, "bonsai-lint-x86_64-pc-windows-msvc.zip")

	if _, _, err := archives(m); err == nil || !strings.Contains(err.Error(), "no archive for x86_64-pc-windows-msvc") {
		t.Fatalf("err = %v", err)
	}
}

func TestAMissingChecksumIsAnError(t *testing.T) {
	m := load(t, "dist-manifest-targz.json")
	name := "bonsai-lint-aarch64-apple-darwin.tar.gz"
	a := m.Artifacts[name]
	a.Checksums = nil
	m.Artifacts[name] = a

	if _, _, err := archives(m); err == nil || !strings.Contains(err.Error(), "no sha256 checksum") {
		t.Fatalf("err = %v", err)
	}
}

// The generator writes source for another package, so compile it there: the launcher's files
// with the generated release.go in place of the placeholder.
func TestTheGeneratedFileBuildsWithTheLauncher(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the launcher")
	}
	dir := t.TempDir()
	launcher, _ := filepath.Glob(filepath.Join("..", "..", "*.go"))
	for _, file := range append(launcher, filepath.Join("..", "..", "go.mod")) {
		name := filepath.Base(file)
		if name == "release.go" || strings.HasSuffix(name, "_test.go") {
			continue
		}
		copyFile(t, file, filepath.Join(dir, name))
	}
	copyFile(t, filepath.Join("testdata", "release.golden"), filepath.Join(dir, "release.go"))

	build := exec.Command("go", "vet", ".")
	build.Dir = dir
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go vet: %v\n%s", err, out)
	}
}

func TestThePlaceholderHasNoArchives(t *testing.T) {
	source, err := render("", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(source), `const version = ""`) ||
		!strings.Contains(string(source), "var archives = map[string]archive{}") {
		t.Fatalf("placeholder:\n%s", source)
	}
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	body, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, body, 0o644); err != nil {
		t.Fatal(err)
	}
}
