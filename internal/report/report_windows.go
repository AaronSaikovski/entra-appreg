//go:build windows

package report

import "strings"

func illegalRune(r rune) bool {
	return r <= 0x1f || strings.ContainsRune("<>:\"/\\|?*", r)
}

// reservedName reports whether name would open a Windows device (NUL, CON,
// COM1, ...): the text before the first '.', without trailing spaces.
func reservedName(name string) bool {
	stem, _, _ := strings.Cut(name, ".")
	stem = strings.ToUpper(strings.TrimRight(stem, " "))
	switch stem {
	case "CON", "PRN", "AUX", "NUL",
		"COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
		"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9",
		"COM\u00b9", "COM\u00b2", "COM\u00b3", "LPT\u00b9", "LPT\u00b2", "LPT\u00b3":
		return true
	}
	return false
}
