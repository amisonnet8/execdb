package main

import (
	"testing"
)

func newTestCancelRegistry() *cancelRegistry {
	return &cancelRegistry{entries: make(map[cancelKey]*connCancel)}
}

func TestCancelRegistryRegisterAndCancel(t *testing.T) {
	r := newTestCancelRegistry()
	key, cc, unregister := r.register()
	defer unregister()

	called := false
	end := cc.begin(func() { called = true })
	defer end()

	if !r.cancel(key) {
		t.Fatal("cancel(key) = false, want true (a matching connection is registered)")
	}
	if !called {
		t.Error("expected the registered cancel func to have been called")
	}
}

func TestCancelRegistryIdleConnectionIsNoOp(t *testing.T) {
	// No query currently running (begin/end never called) -- cancel must
	// still report the connection was found, but nothing happens.
	r := newTestCancelRegistry()
	key, _, unregister := r.register()
	defer unregister()

	if !r.cancel(key) {
		t.Error("cancel(key) on an idle connection = false, want true (the connection still exists)")
	}
}

func TestCancelRegistryUnknownKeyIgnored(t *testing.T) {
	r := newTestCancelRegistry()
	_, cc, unregister := r.register()
	defer unregister()
	called := false
	defer cc.begin(func() { called = true })()

	unknown := cancelKey{pid: 999999, secret: 999999}
	if r.cancel(unknown) {
		t.Error("cancel(unknown) = true, want false")
	}
	if called {
		t.Error("an unrelated registration's cancel func must not be called")
	}
}

func TestCancelRegistrySecretMismatchIgnored(t *testing.T) {
	r := newTestCancelRegistry()
	key, cc, unregister := r.register()
	defer unregister()
	called := false
	defer cc.begin(func() { called = true })()

	wrongSecret := cancelKey{pid: key.pid, secret: key.secret + 1}
	if r.cancel(wrongSecret) {
		t.Error("cancel with the right pid but wrong secret = true, want false")
	}
	if called {
		t.Error("a secret mismatch must not cancel the connection")
	}
}

func TestCancelRegistryUnregisterRemovesEntry(t *testing.T) {
	r := newTestCancelRegistry()
	key, cc, unregister := r.register()
	called := false
	cc.begin(func() { called = true })
	unregister()

	if r.cancel(key) {
		t.Error("cancel(key) after unregister = true, want false")
	}
	if called {
		t.Error("cancel func must not run after its connection unregistered")
	}
}

func TestCancelRegistryGeneratesDistinctKeys(t *testing.T) {
	r := newTestCancelRegistry()
	keyA, _, unregisterA := r.register()
	defer unregisterA()
	keyB, _, unregisterB := r.register()
	defer unregisterB()

	if keyA == keyB {
		t.Errorf("two registrations got the same key %+v", keyA)
	}
}

// TestConnCancelOnlyAffectsCurrentQuery is the core fix this step made:
// a connCancel's begin/end must scope a CancelFunc to exactly one query,
// so canceling query 1 must not leave query 2 (begun afterward) canceled
// too -- context.CancelFunc is one-shot, so naively reusing a single fixed
// one across a connection's whole lifetime would permanently break every
// later query the first time anything canceled one (this is precisely the
// bug found and fixed in phase 4 Step 6).
func TestConnCancelOnlyAffectsCurrentQuery(t *testing.T) {
	cc := &connCancel{}

	query1Canceled := false
	end1 := cc.begin(func() { query1Canceled = true })
	cc.cancelCurrent()
	end1()
	if !query1Canceled {
		t.Fatal("expected query 1's cancel func to run")
	}

	query2Canceled := false
	end2 := cc.begin(func() { query2Canceled = true })
	defer end2()
	if query2Canceled {
		t.Error("query 2 must not start out canceled just because query 1 was")
	}
}

func TestConnCancelIdleIsNoOp(t *testing.T) {
	cc := &connCancel{}
	cc.cancelCurrent() // must not panic with nothing registered
}

func TestCancelRegistryConcurrentUse(t *testing.T) {
	// A smoke test for -race: many goroutines registering, beginning a
	// query, canceling, ending, and unregistering concurrently must not
	// race on the registry's map or any connCancel's state.
	r := newTestCancelRegistry()
	done := make(chan struct{})
	const n = 50
	for i := 0; i < n; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			key, cc, unregister := r.register()
			end := cc.begin(func() {})
			r.cancel(key)
			end()
			unregister()
		}()
	}
	for i := 0; i < n; i++ {
		<-done
	}
}
