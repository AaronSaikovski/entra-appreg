// Package report writes one-time app-registration reports without overwriting
// existing files.
package report

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// fallbackStem names the report when nothing usable remains of the app name.
const fallbackStem = "app-registration"

// Write claims a new owner-only report file in dir named after appName and
// writes contents to it, returning dir joined with the claimed file name.
// Existing files are never overwritten: an occupied name gets a -1, -2, …
// suffix instead.
func Write(dir, appName, contents string) (string, error) {
	stem := fileStem(appName)
	for n := uint64(0); ; n++ {
		path := filepath.Join(dir, candidateName(stem, n))
		// O_EXCL never follows symlinks, so an existing link is a collision
		// rather than a write target.
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			if occupied(path, err) {
				continue
			}
			return "", err
		}
		// Once claimed, write/close failures are genuine failures, not
		// collisions. Leave the owner-only file in place; never retry.
		if err := writeContents(file, contents); err != nil {
			return "", err
		}
		return path, nil
	}
}

// fileStem derives a file name stem from appName. Outer spaces and dots are
// trimmed first because Windows silently drops trailing ones; afterwards
// neither can reach either end, since inner spaces become '-' and characters
// the platform forbids become '_'.
func fileStem(appName string) string {
	stem := strings.Map(func(r rune) rune {
		switch {
		case illegalRune(r):
			return '_'
		case r == ' ':
			return '-'
		}
		return r
	}, strings.Trim(appName, " ."))
	switch {
	case stem == "":
		return fallbackStem
	case reservedName(stem):
		return "_" + stem
	}
	return stem
}

// candidateName returns the n-th file name to try for stem.
func candidateName(stem string, n uint64) string {
	if n == 0 {
		return stem + ".txt"
	}
	return stem + "-" + strconv.FormatUint(n, 10) + ".txt"
}

// occupied reports whether the open error err means path is already taken.
// Windows reports an existing directory as EISDIR (Go maps CreateFile's
// ERROR_ACCESS_DENIED to it), and some platforms report it as a permission
// error, so a permission error counts when Lstat finds an entry at path;
// Lstat also sees dangling symlinks.
func occupied(path string, err error) bool {
	if errors.Is(err, fs.ErrExist) || errors.Is(err, syscall.EISDIR) {
		return true
	}
	if !errors.Is(err, fs.ErrPermission) {
		return false
	}
	_, statErr := os.Lstat(path)
	return statErr == nil
}

// writeContents writes contents and closes w, returning the first failure.
func writeContents(w io.WriteCloser, contents string) error {
	if _, err := io.WriteString(w, contents); err != nil {
		_ = w.Close()
		return err
	}
	return w.Close()
}
