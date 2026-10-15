package progress

import (
	"fmt"
	"io"
	"sync"
	"time"
)

// Spinner shows an animated progress indicator on an interactive terminal.
//
// When the writer is not an interactive terminal (piped, redirected, CI log,
// TERM=dumb) animation would just dump every frame into the stream (#189), so
// the spinner instead writes its message once, as a single static line, on
// Start and nothing on Stop.
type Spinner struct {
	interactive bool
	frames      []string
	current     int
	message     string
	writer      io.Writer
	stopCh      chan struct{}
	wg          sync.WaitGroup
	mu          sync.Mutex
}

func NewSpinner(w io.Writer, message string) *Spinner {
	return &Spinner{
		interactive: Interactive(w),
		frames:      []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"},
		message:     message,
		writer:      w,
		stopCh:      make(chan struct{}),
	}
}

func (s *Spinner) Start() {
	if !s.interactive {
		s.mu.Lock()
		_, _ = fmt.Fprintln(s.writer, s.message)
		s.mu.Unlock()
		return
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-s.stopCh:
				return
			case <-ticker.C:
				s.mu.Lock()
				_, _ = fmt.Fprintf(s.writer, "\r%s %s", s.frames[s.current], s.message)
				s.current = (s.current + 1) % len(s.frames)
				s.mu.Unlock()
			}
		}
	}()
}

func (s *Spinner) Stop() {
	close(s.stopCh)
	s.wg.Wait()
	if !s.interactive {
		return
	}
	s.mu.Lock()
	_, _ = fmt.Fprint(s.writer, "\r\033[K") // Clear line
	s.mu.Unlock()
}

func (s *Spinner) UpdateMessage(msg string) {
	s.mu.Lock()
	s.message = msg
	s.mu.Unlock()
}
