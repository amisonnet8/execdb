package main

import (
	"bufio"
	"context"
	"strings"
	"testing"
	"time"
)

// newTestInterrupts builds a replInterrupts directly, bypassing
// newReplInterrupts (which also wires up a real os/signal registration
// that these tests don't need -- per the plan, the state machine and
// actual signal delivery are tested separately). exitFunc is swapped for
// a recorder so exercising the "second consecutive press" path doesn't
// terminate the test binary.
func newTestInterrupts() (ri *replInterrupts, exitCodes *[]int) {
	codes := []int{}
	ri = &replInterrupts{
		idleSig:  make(chan struct{}, 1),
		exitFunc: func(code int) { codes = append(codes, code) },
	}
	return ri, &codes
}

func TestInterruptsIdleFirstPressSignalsDiscardNotExit(t *testing.T) {
	ri, codes := newTestInterrupts()

	ri.onInterrupt()

	if len(*codes) != 0 {
		t.Errorf("exitFunc called on a first idle press: %v", *codes)
	}
	select {
	case <-ri.idleSig:
	default:
		t.Error("expected a pending idle signal after a first idle press")
	}
}

func TestInterruptsSecondConsecutiveIdlePressExits(t *testing.T) {
	ri, codes := newTestInterrupts()

	ri.onInterrupt()
	ri.onInterrupt()

	if len(*codes) != 1 || (*codes)[0] != 1 {
		t.Errorf("exitFunc calls = %v, want [1]", *codes)
	}
}

func TestInterruptsResetOnNewLineAllowsAFreshFirstPress(t *testing.T) {
	ri, codes := newTestInterrupts()

	ri.onInterrupt()
	ri.resetOnNewLine()
	ri.onInterrupt()

	if len(*codes) != 0 {
		t.Errorf("exitFunc called after a reset in between two presses: %v", *codes)
	}
}

func TestInterruptsCancelsRunningStatementInsteadOfExiting(t *testing.T) {
	ri, codes := newTestInterrupts()

	_, cancel := context.WithCancel(context.Background())
	canceled := false
	end := ri.begin(func() { canceled = true; cancel() })
	defer end()

	ri.onInterrupt()

	if !canceled {
		t.Error("onInterrupt did not call the registered cancel func")
	}
	if len(*codes) != 0 {
		t.Errorf("exitFunc called on a first press while a statement was running: %v", *codes)
	}
	select {
	case <-ri.idleSig:
		t.Error("an idle signal should not fire when a statement was running")
	default:
	}
}

func TestInterruptsSecondPressWhileRunningStillExits(t *testing.T) {
	ri, codes := newTestInterrupts()

	_, cancel := context.WithCancel(context.Background())
	end := ri.begin(cancel)
	defer end()

	ri.onInterrupt()
	ri.onInterrupt()

	if len(*codes) != 1 || (*codes)[0] != 1 {
		t.Errorf("exitFunc calls = %v, want [1] (a second consecutive press must force-quit even mid-query)", *codes)
	}
}

func TestInterruptsEndClearsCancel(t *testing.T) {
	ri, codes := newTestInterrupts()

	called := false
	end := ri.begin(func() { called = true })
	end()

	ri.onInterrupt()

	if called {
		t.Error("cancel was called after end() unregistered it")
	}
	if len(*codes) != 0 {
		t.Errorf("exitFunc called unexpectedly: %v", *codes)
	}
	select {
	case <-ri.idleSig:
	default:
		t.Error("expected an idle signal once cancel was unregistered")
	}
}

func TestLineReaderForwardsLinesAndClosesOnEOF(t *testing.T) {
	scanner := bufio.NewScanner(strings.NewReader("a\nb\nc\n"))
	lr := startLineReader(scanner)

	var got []string
	timeout := time.After(5 * time.Second)
	for {
		select {
		case line, ok := <-lr.lines:
			if !ok {
				if err := <-lr.err; err != nil {
					t.Fatalf("unexpected scanner error: %v", err)
				}
				if want := []string{"a", "b", "c"}; !equalStrings(got, want) {
					t.Errorf("got %v, want %v", got, want)
				}
				return
			}
			got = append(got, line)
		case <-timeout:
			t.Fatal("timed out waiting for lineReader")
		}
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
