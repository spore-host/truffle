package progress

import (
	"io"
	"os"

	"github.com/mattn/go-isatty"
)

// isTerminal reports whether f is attached to a terminal. A package variable so
// tests can simulate a TTY without allocating a pty.
var isTerminal = func(f *os.File) bool {
	fd := f.Fd()
	return isatty.IsTerminal(fd) || isatty.IsCygwinTerminal(fd)
}

// Interactive reports whether w is a terminal that can render in-place
// animation (carriage returns, ANSI erase-line). It is false for anything that
// is not an *os.File (buffers, pipes wrapped in writers), for a file that is not
// a TTY (redirected to a file, piped to `cat`, captured by CI), and when
// TERM=dumb, which by convention means the terminal understands no control
// sequences (#189).
func Interactive(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok || f == nil {
		return false
	}
	if os.Getenv("TERM") == "dumb" {
		return false
	}
	return isTerminal(f)
}

// ColorAllowed reports whether ANSI colour may be written to w: it must be
// Interactive and NO_COLOR (https://no-color.org) must be unset or empty.
func ColorAllowed(w io.Writer) bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	return Interactive(w)
}
