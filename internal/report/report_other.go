//go:build !windows

package report

// illegalRune reports whether r cannot appear in a file name. POSIX file
// systems forbid only NUL and the path separator.
func illegalRune(r rune) bool { return r == 0 || r == '/' }

// reservedName always reports false: only Windows has device names.
func reservedName(string) bool { return false }
