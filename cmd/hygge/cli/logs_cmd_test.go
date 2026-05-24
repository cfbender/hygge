package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLogsCommandPrintsLogFile(t *testing.T) {
	home := hermeticHome(t)
	writeHermeticLog(t, home, strings.Join([]string{
		`time=2026-05-24T13:00:00.000Z level=INFO msg="started"`,
		`time=2026-05-24T13:01:00.000Z level=WARN msg="careful"`,
	}, "\n")+"\n")

	stdout, stderr, err := executeLogsCommand("logs")
	if err != nil {
		t.Fatalf("logs returned error: %v\nstderr: %s", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	want := "time=2026-05-24T13:00:00.000Z level=INFO msg=\"started\"\n" +
		"time=2026-05-24T13:01:00.000Z level=WARN msg=\"careful\"\n"
	if stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
}

func TestLogsCommandLimitsLines(t *testing.T) {
	home := hermeticHome(t)
	writeHermeticLog(t, home, strings.Join([]string{
		`time=2026-05-24T13:00:00.000Z level=DEBUG msg="debug"`,
		`time=2026-05-24T13:01:00.000Z level=INFO msg="info"`,
		`time=2026-05-24T13:02:00.000Z level=ERROR msg="error"`,
	}, "\n")+"\n")

	stdout, stderr, err := executeLogsCommand("logs", "-n", "2")
	if err != nil {
		t.Fatalf("logs returned error: %v\nstderr: %s", err, stderr)
	}
	want := "time=2026-05-24T13:01:00.000Z level=INFO msg=\"info\"\n" +
		"time=2026-05-24T13:02:00.000Z level=ERROR msg=\"error\"\n"
	if stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
}

func TestLogsCommandZeroLinesPrintsNothing(t *testing.T) {
	home := hermeticHome(t)
	writeHermeticLog(t, home, `time=2026-05-24T13:00:00.000Z level=INFO msg="started"`+"\n")

	stdout, stderr, err := executeLogsCommand("logs", "-n", "0")
	if err != nil {
		t.Fatalf("logs returned error: %v\nstderr: %s", err, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
}

func TestLogsCommandFiltersByLevelThreshold(t *testing.T) {
	home := hermeticHome(t)
	writeHermeticLog(t, home, strings.Join([]string{
		`time=2026-05-24T13:00:00.000Z level=DEBUG msg="debug"`,
		`time=2026-05-24T13:01:00.000Z level=INFO msg="info"`,
		`time=2026-05-24T13:02:00.000Z level=WARN msg="warn"`,
		`time=2026-05-24T13:03:00.000Z level=ERROR msg="error"`,
	}, "\n")+"\n")

	stdout, stderr, err := executeLogsCommand("logs", "--level", "warn")
	if err != nil {
		t.Fatalf("logs returned error: %v\nstderr: %s", err, stderr)
	}
	want := "time=2026-05-24T13:02:00.000Z level=WARN msg=\"warn\"\n" +
		"time=2026-05-24T13:03:00.000Z level=ERROR msg=\"error\"\n"
	if stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
}

func TestLogsCommandFiltersBeforeLimitingLines(t *testing.T) {
	home := hermeticHome(t)
	writeHermeticLog(t, home, strings.Join([]string{
		`time=2026-05-24T13:00:00.000Z level=WARN msg="first"`,
		`time=2026-05-24T13:01:00.000Z level=INFO msg="ignored"`,
		`time=2026-05-24T13:02:00.000Z level=ERROR msg="second"`,
	}, "\n")+"\n")

	stdout, stderr, err := executeLogsCommand("logs", "--level", "warn", "-n", "1")
	if err != nil {
		t.Fatalf("logs returned error: %v\nstderr: %s", err, stderr)
	}
	want := "time=2026-05-24T13:02:00.000Z level=ERROR msg=\"second\"\n"
	if stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
}

func TestLogsCommandRejectsInvalidFlags(t *testing.T) {
	hermeticHome(t)

	_, stderr, err := executeLogsCommand("logs", "--level", "verbose")
	if err == nil {
		t.Fatal("logs --level verbose returned nil error")
	}
	if !strings.Contains(stderr, `unknown level "verbose"`) {
		t.Fatalf("stderr = %q, want invalid level message", stderr)
	}

	_, stderr, err = executeLogsCommand("logs", "-n", "-1")
	if err == nil {
		t.Fatal("logs -n -1 returned nil error")
	}
	if !strings.Contains(stderr, "-n must be >= 0") {
		t.Fatalf("stderr = %q, want invalid lines message", stderr)
	}
}

func TestLogsCommandMissingFilePrintsNothing(t *testing.T) {
	hermeticHome(t)

	stdout, stderr, err := executeLogsCommand("logs")
	if err != nil {
		t.Fatalf("logs returned error: %v\nstderr: %s", err, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestLogsCommandReturnsScannerErrors(t *testing.T) {
	home := hermeticHome(t)
	writeHermeticLog(t, home, strings.Repeat("x", 65*1024)+"\n")

	stdout, _, err := executeLogsCommand("logs")
	if err == nil {
		t.Fatal("logs returned nil error for overlong log line")
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(err.Error(), "hygge logs: scan") {
		t.Fatalf("error = %v, want scan error", err)
	}
}

func executeLogsCommand(args ...string) (string, string, error) {
	cmd := NewRootCmd()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return stdout.String(), stderr.String(), err
}

func writeHermeticLog(t *testing.T, home, body string) {
	t.Helper()
	path := filepath.Join(home, ".local", "state", "hygge", "hygge.log")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir log dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write log file: %v", err)
	}
}
