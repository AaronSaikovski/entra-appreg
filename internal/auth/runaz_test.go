package auth

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

const helperEnv = "ENTRA_APPREG_AUTH_HELPER_PROCESS"

// TestHelperProcess is not a real test: runAz tests re-execute the test
// binary with helperEnv set so it acts as a fake az with known output.
func TestHelperProcess(t *testing.T) {
	if os.Getenv(helperEnv) != "1" {
		return
	}
	fmt.Fprint(os.Stdout, tenant+"\n")
	fmt.Fprint(os.Stderr, privateOutput)
	os.Exit(3)
}

func TestRunAzReturnsStdoutAndDiscardsStderr(t *testing.T) {
	t.Setenv(helperEnv, "1")
	out, err := runAz(t.Context(), os.Args[0], "-test.run=^TestHelperProcess$")
	if string(out) != tenant+"\n" {
		t.Errorf("stdout = %q", out)
	}
	exitErr, ok := errors.AsType[*exec.ExitError](err)
	if !ok {
		t.Fatalf("err = %v, want *exec.ExitError", err)
	}
	if exitErr.ExitCode() != 3 {
		t.Errorf("exit code = %d", exitErr.ExitCode())
	}
	if len(exitErr.Stderr) != 0 || strings.Contains(fmt.Sprint(err), privateOutput) {
		t.Errorf("stderr retained: %q in %v", exitErr.Stderr, err)
	}
}

func TestRunAzReportsMissingProgram(t *testing.T) {
	_, err := runAz(t.Context(), "entra-appreg-test-no-such-program")
	if !errors.Is(err, exec.ErrNotFound) {
		t.Fatalf("err = %v, want exec.ErrNotFound", err)
	}
}
