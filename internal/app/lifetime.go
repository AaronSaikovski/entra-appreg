package app

import (
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/AaronSaikovski/entra-appreg/internal/cli"
)

type secretLifetime struct {
	start *time.Time // nil = starts now
	end   time.Time
}

func allDigits(s string) bool {
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// parseDate parses DD/MM/YYYY (1-2 digit day and month, 4-digit year in
// 1..9999) as midnight UTC. Dates that do not exist (e.g. 31/4) are rejected.
func parseDate(value string) (time.Time, bool) {
	parts := strings.Split(strings.TrimSpace(value), "/")
	if len(parts) != 3 {
		return time.Time{}, false
	}
	day, month, year := parts[0], parts[1], parts[2]
	if len(day) < 1 || len(day) > 2 || len(month) < 1 || len(month) > 2 || len(year) != 4 ||
		!allDigits(day) || !allDigits(month) || !allDigits(year) {
		return time.Time{}, false
	}
	y, _ := strconv.Atoi(year)
	m, _ := strconv.Atoi(month)
	d, _ := strconv.Atoi(day)
	if y < 1 || y > 9999 {
		return time.Time{}, false
	}
	date := time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC)
	if date.Year() != y || int(date.Month()) != m || date.Day() != d {
		return time.Time{}, false
	}
	return date, true
}

// resolveLifetime validates the secret options against now. A nil lifetime
// means no secret was requested. Every returned error is a *cli.UsageError.
func resolveLifetime(o *cli.Options, now time.Time) (*secretLifetime, error) {
	now = now.UTC()
	dates := o.SecretStart != nil || o.SecretEnd != nil
	if !o.CreateSecret {
		if dates || o.SecretExpiry != nil {
			return nil, &cli.UsageError{Message: "--secret-expiry, --secret-start and --secret-end require --create-secret."}
		}
		return nil, nil
	}
	mode := "180"
	if o.SecretExpiry != nil {
		mode = strings.TrimSpace(*o.SecretExpiry)
	} else if dates {
		mode = "custom"
	}
	if cli.EqualFoldASCII(mode, "custom") {
		if o.SecretEnd == nil {
			return nil, &cli.UsageError{Message: "--secret-end (DD/MM/YYYY) is required for a custom secret lifetime."}
		}
		end, ok := parseDate(*o.SecretEnd)
		if !ok {
			return nil, &cli.UsageError{Message: "--secret-end must be a date in DD/MM/YYYY format, got: " + *o.SecretEnd}
		}
		today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
		start := today
		if o.SecretStart != nil {
			if start, ok = parseDate(*o.SecretStart); !ok {
				return nil, &cli.UsageError{Message: "--secret-start must be a date in DD/MM/YYYY format, got: " + *o.SecretStart}
			}
		}
		if start.Before(today) {
			return nil, &cli.UsageError{Message: "--secret-start cannot be in the past (today is " + today.Format("02/01/2006") + " UTC)."}
		}
		if !end.After(start) {
			return nil, &cli.UsageError{Message: "--secret-end must be after --secret-start."}
		}
		life := &secretLifetime{end: end.Add(23*time.Hour + 59*time.Minute + 59*time.Second)}
		if start.After(today) {
			life.start = &start
		}
		return life, nil
	}
	days, err := strconv.ParseInt(mode, 10, 64)
	if err != nil || !slices.Contains([]int64{90, 180, 365, 545, 730}, days) {
		return nil, &cli.UsageError{Message: "--secret-expiry must be one of 90, 180, 365, 545, 730 or custom, got: " + deref(o.SecretExpiry, "")}
	}
	if dates {
		return nil, &cli.UsageError{Message: "--secret-start and --secret-end can only be used with --secret-expiry custom."}
	}
	return &secretLifetime{end: now.Add(time.Duration(days) * 24 * time.Hour)}, nil
}

// secretDescription is "{appName} secret" cut to at most 128 UTF-16 code
// units without splitting a character.
func secretDescription(appName string) string {
	var b strings.Builder
	units := 0
	for _, r := range appName + " secret" {
		units += utf16.RuneLen(r)
		if units > 128 {
			break
		}
		b.WriteRune(r)
	}
	return b.String()
}
