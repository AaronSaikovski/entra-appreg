package app

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestLifetimeUsesFrozenUTCClockAndInclusiveCustomEnd(t *testing.T) {
	now := frozenNow
	for _, days := range []int{90, 180, 365, 545, 730} {
		life, err := resolveLifetime(options(t, "--create-secret", "--secret-expiry", strconv.Itoa(days)), now)
		if err != nil || life == nil {
			t.Fatalf("%d days: %v", days, err)
		}
		if life.end.Sub(now) != time.Duration(days)*24*time.Hour || life.start != nil {
			t.Errorf("%d days: end %v start %v", days, life.end, life.start)
		}
	}
	life, err := resolveLifetime(options(t, "--create-secret"), now)
	if err != nil || life.end.Sub(now) != 180*24*time.Hour {
		t.Errorf("default lifetime: %v %v", life, err)
	}
	life, err = resolveLifetime(options(t,
		"--create-secret", "--secret-start", "29/2/2028", "--secret-end", "1/3/2028"), now)
	if err != nil {
		t.Fatal(err)
	}
	if got := life.start.Format(time.RFC3339); got != "2028-02-29T00:00:00Z" {
		t.Errorf("start = %s", got)
	}
	if got := life.end.Format(time.RFC3339); got != "2028-03-01T23:59:59Z" {
		t.Errorf("end = %s", got)
	}
	life, err = resolveLifetime(options(t,
		"--create-secret", "--secret-start", "28/02/2028", "--secret-end", "29/02/2028"), now)
	if err != nil || life.start != nil {
		t.Errorf("today's start must be omitted: %v %v", life, err)
	}
	for _, args := range [][]string{
		{"--secret-expiry", "90"},
		{"--create-secret", "--secret-expiry", "custom"},
		{"--create-secret", "--secret-expiry", "90", "--secret-end", "1/3/2028"},
		{"--create-secret", "--secret-start", "27/2/2028", "--secret-end", "1/3/2028"},
		{"--create-secret", "--secret-end", "28/2/2028"},
		{"--create-secret", "--secret-start", "1/3/2028", "--secret-end", "29/2/2028"},
		{"--create-secret", "--secret-end", "29/2/2029"},
	} {
		if _, err := resolveLifetime(options(t, args...), now); err == nil {
			t.Errorf("%q accepted", args)
		}
	}
	for _, date := range []string{"1/1/0000", "1/1/10000", "001/1/2028", "+1/1/2028", "1/1/28", "31/4/2028", "1/1/2028/"} {
		if _, ok := parseDate(date); ok {
			t.Errorf("%q parsed", date)
		}
	}
	if _, ok := parseDate(" 1/1/0001 "); !ok {
		t.Error("1/1/0001 rejected")
	}
	if _, ok := parseDate("31/12/9999"); !ok {
		t.Error("31/12/9999 rejected")
	}
}

func TestDescriptionNeverSplitsAstralScalar(t *testing.T) {
	if got := secretDescription(strings.Repeat("a", 127) + "\U0001F600"); got != strings.Repeat("a", 127) {
		t.Errorf("got %q", got)
	}
	name := strings.Repeat("a", 126) + "\U0001F600"
	if got := secretDescription(name); got != name {
		t.Errorf("got %q", got)
	}
	if got := secretDescription("App"); got != "App secret" {
		t.Errorf("got %q", got)
	}
}
