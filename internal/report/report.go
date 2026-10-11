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
)

// Write claims a new owner-only report file in dir named after appName and
// writes contents to it, returning the report path.
func Write(dir, appName, contents string) (string, error) {
	sanitized := strings.Map(func(r rune) rune {
		if illegalRune(r) {
			return '_'
		}
		if r == ' ' {
			return '-'
		}
		return r
	}, strings.Trim(appName, " ."))
	name := strings.Trim(sanitized, " .")
	if name == "" {
		name = "app-registration"
	}
	if reservedName(name) {
		name = "_" + name
	}

	for suffix := uint64(0); ; suffix++ {
		filename := name + ".txt"
		if suffix != 0 {
			filename = name + "-" + strconv.FormatUint(suffix, 10) + ".txt"
		}
		path := filepath.Join(dir, filename)
		// O_EXCL never follows symlinks. Some platforms report an occupied
		// directory as a permission error; Lstat also sees dangling symlinks.
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			occupied := errors.Is(err, fs.ErrExist)
			if !occupied && errors.Is(err, fs.ErrPermission) {
				_, statErr := os.Lstat(path)
				occupied = statErr == nil
			}
			if !occupied {
				return "", err
			}
			continue
		}
		// Once claimed, write/close failures are genuine failures, not
		// collisions. Leave the owner-only file in place; never retry.
		if err := writeContents(file, contents); err != nil {
			return "", err
		}
		return path, nil
	}
}

func writeContents(w io.WriteCloser, contents string) error {
	if _, err := io.WriteString(w, contents); err != nil {
		_ = w.Close()
		return err
	}
	return w.Close()
}
