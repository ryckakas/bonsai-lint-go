package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Both far above today's sizes (about 2 MB and 7 MB); they only stop a runaway download.
const maxArchive, maxBinary = 256 << 20, 512 << 20

type launcher struct {
	version      string
	archives     map[string]archive
	platform     string
	getenv       func(string) string
	userCacheDir func() (string, error)
	ldd          func() string
	client       *http.Client
	stderr       io.Writer
}

// resolve returns the path of the binary to run, downloading it on the first run of a version.
// A cached binary is trusted as it is: whoever can write the cache can already rewrite this
// launcher, and hashing it on every run would slow each editor scan down.
func (l *launcher) resolve() (string, error) {
	if binary := l.getenv("BONSAI_LINT_BINARY"); binary != "" {
		return binary, nil
	}
	if l.version == "" {
		return "", errors.New("this launcher is an unreleased build with no binary to fetch; " +
			"install a release (go install bonsai.kauneckas.dev/bonsai-lint@latest) or set BONSAI_LINT_BINARY")
	}
	if _, ok := l.archives[l.platform]; !ok {
		return "", l.unsupported("there is no prebuilt binary for " + l.platform)
	}
	root, err := l.cacheRoot()
	if err != nil {
		return "", err
	}
	// A cached build was chosen for this host before, so ldd is asked only ahead of a download.
	// The static one runs on any Linux, so it wins if a cache shared between hosts holds both.
	for _, key := range []string{l.platform + staticSuffix, l.platform} {
		if target, ok := l.archives[key]; ok {
			if binary := l.cached(root, target); isFile(binary) {
				return binary, nil
			}
		}
	}
	key := l.platform
	if strings.HasPrefix(key, "linux/") && !glibcBuildRuns(l.ldd()) {
		key += staticSuffix
	}
	target, ok := l.archives[key]
	if !ok {
		return "", l.unsupported("this system cannot run the glibc build, and there is no static one for " + l.platform)
	}
	binary := l.cached(root, target)
	if err := l.install(key, target, binary); err != nil {
		return "", err
	}
	return binary, nil
}

func (l *launcher) cached(root string, target archive) string {
	return filepath.Join(root, l.version, target.triple, target.executable())
}

func (l *launcher) unsupported(problem string) error {
	return fmt.Errorf("bonsai-lint v%s: %s; install it with `cargo install bonsai-lint`, "+
		"or set BONSAI_LINT_BINARY to a binary you have", l.version, problem)
}

func (l *launcher) cacheRoot() (string, error) {
	if dir := l.getenv("BONSAI_LINT_CACHE"); dir != "" {
		return filepath.Abs(dir)
	}
	base, err := l.userCacheDir()
	if err != nil {
		return "", fmt.Errorf("no cache directory (%v); set BONSAI_LINT_CACHE", err)
	}
	return filepath.Join(base, "bonsai-lint"), nil
}

// The same variable dist's shell and PowerShell installers read, with the same meaning.
func (l *launcher) downloadBase() string {
	if base := l.getenv("BONSAI_LINT_DOWNLOAD_URL"); base != "" {
		return strings.TrimSuffix(base, "/")
	}
	return "https://github.com/ryckakas/bonsai-lint/releases/download/v" + l.version
}

func (l *launcher) install(key string, target archive, binary string) error {
	dir := filepath.Dir(binary)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	fmt.Fprintf(l.stderr, "bonsai-lint: downloading v%s for %s…\n", l.version, key)
	downloaded, err := l.fetch(l.downloadBase()+"/"+target.name, target.sha256, dir)
	if err != nil {
		return err
	}
	defer os.Remove(downloaded)
	extracted, err := extract(downloaded, target, dir)
	if err != nil {
		return err
	}
	return place(extracted, binary)
}

// fetch downloads url into dir and keeps it only if it matches the checksum this launcher was
// released with, so nothing unverified is ever unpacked.
func (l *launcher) fetch(url, want, dir string) (string, error) {
	request, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("User-Agent", "bonsai-lint-go/"+l.version)
	response, err := l.client.Do(request)
	if err != nil {
		return "", fmt.Errorf("downloading %s: %w", url, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("downloading %s: %s", url, response.Status)
	}

	file, err := os.CreateTemp(dir, ".archive-*")
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(file, hash), io.LimitReader(response.Body, maxArchive+1))
	closeErr := file.Close()
	switch {
	case copyErr != nil:
		err = fmt.Errorf("downloading %s: %w", url, copyErr)
	case closeErr != nil:
		err = closeErr
	case written > maxArchive:
		err = fmt.Errorf("downloading %s: larger than %d bytes", url, maxArchive)
	}
	if got := hex.EncodeToString(hash.Sum(nil)); err == nil && !strings.EqualFold(got, want) {
		err = fmt.Errorf("%s does not match its published checksum (expected %s, got %s); nothing was installed",
			url, want, got)
	}
	if err != nil {
		os.Remove(file.Name())
		return "", err
	}
	return file.Name(), nil
}

func extract(downloaded string, target archive, dir string) (string, error) {
	out, err := os.CreateTemp(dir, ".binary-*")
	if err != nil {
		return "", err
	}
	switch {
	case strings.HasSuffix(target.name, ".tar.gz"):
		err = fromTarGz(downloaded, target, out)
	case strings.HasSuffix(target.name, ".zip"):
		err = fromZip(downloaded, target, out)
	default:
		err = fmt.Errorf("%s: unsupported archive format", target.name)
	}
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Chmod(out.Name(), 0o755)
	}
	if err != nil {
		os.Remove(out.Name())
		return "", err
	}
	return out.Name(), nil
}

// The destination is chosen here, never taken from the archive, so an entry name cannot
// write outside the cache.
func fromTarGz(downloaded string, target archive, out io.Writer) error {
	file, err := os.Open(downloaded)
	if err != nil {
		return err
	}
	defer file.Close()
	unzipped, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("%s: %w", target.name, err)
	}
	entries := tar.NewReader(unzipped)
	for {
		header, err := entries.Next()
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("%s holds no %s", target.name, target.binary)
		}
		if err != nil {
			return fmt.Errorf("%s: %w", target.name, err)
		}
		if header.Typeflag == tar.TypeReg && target.holds(header.Name) {
			return copyBinary(out, entries, target)
		}
	}
}

func fromZip(downloaded string, target archive, out io.Writer) error {
	files, err := zip.OpenReader(downloaded)
	if err != nil {
		return fmt.Errorf("%s: %w", target.name, err)
	}
	defer files.Close()
	for _, entry := range files.File {
		if entry.FileInfo().IsDir() || !target.holds(entry.Name) {
			continue
		}
		reader, err := entry.Open()
		if err != nil {
			return fmt.Errorf("%s: %w", target.name, err)
		}
		defer reader.Close()
		return copyBinary(out, reader, target)
	}
	return fmt.Errorf("%s holds no %s", target.name, target.binary)
}

func copyBinary(out io.Writer, in io.Reader, target archive) error {
	written, err := io.Copy(out, io.LimitReader(in, maxBinary+1))
	if err != nil {
		return fmt.Errorf("%s: %w", target.name, err)
	}
	if written > maxBinary {
		return fmt.Errorf("%s: %s is larger than %d bytes", target.name, target.binary, maxBinary)
	}
	return nil
}

// place moves a verified binary into the cache. A concurrent first run may have got there first,
// and its copy is the same bytes, so an existing destination counts as success. Windows can hold a
// fresh executable open briefly (antivirus scanners do), hence the retries there.
func place(extracted, binary string) error {
	deadline := time.Now().Add(2 * time.Second)
	for {
		err := os.Rename(extracted, binary)
		if err == nil {
			return nil
		}
		if isFile(binary) {
			os.Remove(extracted)
			return nil
		}
		if runtime.GOOS != "windows" || time.Now().After(deadline) {
			os.Remove(extracted)
			return err
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}
