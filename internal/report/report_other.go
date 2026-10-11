//go:build !windows

package report

func illegalRune(r rune) bool { return r == 0 || r == '/' }

func reservedName(string) bool { return false }
