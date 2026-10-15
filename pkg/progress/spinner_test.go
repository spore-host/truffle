package progress

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"
)

// TestSpinnerNonTTYWritesOneStaticLine is the regression test for #189: piping
// `truffle find … 2>&1 | cat` dumped every animation frame into the stream.
// Off a terminal the spinner must write its message exactly once, with no
// frames, carriage returns or ANSI escapes.
func TestSpinnerNonTTYWritesOneStaticLine(t *testing.T) {
	var buf bytes.Buffer
	s := NewSpinner(&buf, "Finding 'm7i.large' across 3 regions")
	s.Start()
	time.Sleep(350 * time.Millisecond) // long enough for ~3 frames if animating
	s.Stop()

	got := buf.String()
	if want := "Finding 'm7i.large' across 3 regions\n"; got != want {
		t.Fatalf("non-TTY spinner wrote %q, want exactly %q", got, want)
	}
	if strings.ContainsAny(got, "\r\x1b⠋⠙⠹") {
		t.Errorf("non-TTY spinner output contains animation/control characters: %q", got)
	}
}

// A redirected *os.File (not a terminal) is the real-world case: stderr sent to
// a file or a pipe.
func TestSpinnerRedirectedFileIsStatic(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "spin")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	s := NewSpinner(f, "working")
	s.Start()
	time.Sleep(250 * time.Millisecond)
	s.Stop()

	data, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "working\n" {
		t.Errorf("spinner on a redirected file wrote %q, want %q", data, "working\n")
	}
}

// withFakeTTY makes every *os.File look like a terminal for the test's duration.
func withFakeTTY(t *testing.T) {
	t.Helper()
	orig := isTerminal
	isTerminal = func(*os.File) bool { return true }
	t.Cleanup(func() { isTerminal = orig })
}

func TestInteractive(t *testing.T) {
	if Interactive(&bytes.Buffer{}) {
		t.Error("a bytes.Buffer must never be treated as interactive")
	}

	withFakeTTY(t)
	t.Setenv("TERM", "xterm-256color")
	if !Interactive(os.Stderr) {
		t.Error("a TTY with a capable TERM should be interactive")
	}
	t.Setenv("TERM", "dumb")
	if Interactive(os.Stderr) {
		t.Error("TERM=dumb must disable animation")
	}
}

func TestColorAllowed(t *testing.T) {
	withFakeTTY(t)
	t.Setenv("TERM", "xterm-256color")

	t.Setenv("NO_COLOR", "")
	if !ColorAllowed(os.Stdout) {
		t.Error("colour should be allowed on a TTY with NO_COLOR unset")
	}
	t.Setenv("NO_COLOR", "1")
	if ColorAllowed(os.Stdout) {
		t.Error("NO_COLOR=1 must disable colour")
	}
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "dumb")
	if ColorAllowed(os.Stdout) {
		t.Error("TERM=dumb must disable colour")
	}
	if ColorAllowed(&bytes.Buffer{}) {
		t.Error("a non-terminal writer must not get colour")
	}
}
