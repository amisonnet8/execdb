package main

import (
	"bufio"
	"context"
	"os"
	"os/signal"
	"sync"
	"syscall"
)

// replInterrupts tracks Ctrl+C (SIGINT) state for one REPL run, matching
// sqlite3's own shell.c interrupt_handler: every SIGINT increments a
// counter that is reset whenever a new input line is read; a second
// consecutive press (with no line read in between) force-quits the
// process, while a first press cancels whatever statement is currently
// executing (a no-op if none is) and, only when the REPL was otherwise
// idle, tells the main loop to discard a partially-typed multi-line
// statement and redraw the prompt rather than exit.
//
// State is guarded by a mutex and touched from both the signal-handling
// goroutine and the main REPL loop -- the same shape as pgwire.go's
// watchForDisconnect.
type replInterrupts struct {
	mu       sync.Mutex
	count    int
	cancel   context.CancelFunc // set while a statement is executing; nil while idle
	idleSig  chan struct{}      // buffered(1): a pending value means "an idle interrupt happened, discard input"
	exitFunc func(int)          // os.Exit, except in tests
}

// newReplInterrupts starts listening for SIGINT and returns the tracker
// plus a stop func that releases the signal.Notify registration. Only
// meaningful for an interactive session (spec §9/§10): a non-interactive
// (piped) stdin gets no handler registered at all, so SIGINT keeps its
// default behavior of terminating the process immediately -- deliberately,
// so a script that launched ExecDB can still Ctrl+C it
// (.claude/rules/cli-output.md).
func newReplInterrupts() (*replInterrupts, func()) {
	ri := &replInterrupts{idleSig: make(chan struct{}, 1), exitFunc: os.Exit}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-sig:
				ri.onInterrupt()
			case <-done:
				signal.Stop(sig)
				return
			}
		}
	}()
	return ri, func() { close(done) }
}

func (ri *replInterrupts) onInterrupt() {
	ri.mu.Lock()
	ri.count++
	count := ri.count
	cancel := ri.cancel
	ri.mu.Unlock()

	if count > 1 {
		ri.exitFunc(1)
		return
	}
	if cancel != nil {
		cancel()
		return
	}
	select {
	case ri.idleSig <- struct{}{}:
	default: // a notification is already pending; nothing more to do
	}
}

// resetOnNewLine clears the consecutive-interrupt counter. sqlite3's own
// shell.c does this at the top of its input loop, right before reading
// each new line, so a single stray Ctrl+C doesn't half-arm a force-quit
// that a much later, unrelated Ctrl+C then triggers.
func (ri *replInterrupts) resetOnNewLine() {
	ri.mu.Lock()
	ri.count = 0
	ri.mu.Unlock()
}

// begin registers cancel as the function to call if SIGINT arrives while
// a statement is running, and returns a func to unregister it once the
// statement finishes (whether it completed, failed, or was itself
// canceled). Typical use: defer ri.begin(cancel)().
func (ri *replInterrupts) begin(cancel context.CancelFunc) (end func()) {
	ri.mu.Lock()
	ri.cancel = cancel
	ri.mu.Unlock()
	return func() {
		ri.mu.Lock()
		ri.cancel = nil
		ri.mu.Unlock()
	}
}

// lineReader pumps a *bufio.Scanner in a background goroutine so the
// main REPL loop is never blocked inside a stdin read: it can instead
// select between "a line arrived" and "an idle interrupt arrived"
// (run(), repl.go). Once the scanner reaches EOF or errors, lines is
// closed and the terminal error (nil for a clean EOF) is sent on err.
type lineReader struct {
	lines chan string
	err   chan error
}

func startLineReader(scanner *bufio.Scanner) *lineReader {
	lr := &lineReader{lines: make(chan string), err: make(chan error, 1)}
	go func() {
		defer close(lr.lines)
		for scanner.Scan() {
			lr.lines <- scanner.Text()
		}
		lr.err <- scanner.Err()
	}()
	return lr
}
