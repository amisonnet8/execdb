package main

import (
	"context"
	"encoding/binary"
	"io"
	"math/rand"
	"net"
	"sync"
)

// cancelKey identifies one pgwire connection for CancelRequest purposes
// (spec §8, phase 4 Step 6): the (pid, secret) pair BackendKeyData sends
// the client at startup, which a *different* connection must echo back
// exactly in a CancelRequest to cancel this one. Real PostgreSQL's own
// secret is likewise just an unpredictability guard against an unrelated
// client accidentally (or casually) canceling someone else's query, not a
// real security boundary -- math/rand is adequate here for the same
// reason it is in real PostgreSQL's own implementation.
//
// This is deliberately distinct from watchForDisconnect (pgwire.go, phase
// 2 Step 5), which cancels a query when its OWN connection disconnects
// mid-query; CancelRequest is a second, independent connection explicitly
// asking to cancel someone else's still-open connection, and requires
// this separate registry to find it by (pid, secret).
type cancelKey struct {
	pid    int32
	secret int32
}

// connCancel holds one pgwire connection's currently-cancelable query: a
// mutex-guarded context.CancelFunc, set only for the duration of whichever
// query is presently running (begin/end below) and nil while idle. This
// indirection exists because context.CancelFunc is one-shot -- a
// connection handles many queries in sequence, all sharing the connection's
// long-lived engine.Session, so reusing a single fixed CancelFunc across
// every one of them would permanently break the connection the first time
// anything canceled a single query (watchForDisconnect firing, or a
// CancelRequest arriving, would otherwise leave every later query on that
// connection failing with "context canceled" forever). Mirrors
// interrupt.go's replInterrupts.begin, which solves the identical problem
// for the REPL's Ctrl+C.
type connCancel struct {
	mu     sync.Mutex
	cancel context.CancelFunc
}

// begin registers cancel as the function to call to cancel the query about
// to run, and returns a func to unregister it once that query finishes
// (whether it completed, failed, or was itself canceled). Typical use:
// ctx, cancel := context.WithCancel(...); end := cc.begin(cancel); ...;
// end(); cancel(). Deliberately not deferred at the call site when used
// inside handleConnection's per-message loop -- a defer there would only
// run when the whole connection closes, not at the end of that one
// message, leaving a stale entry visible to a concurrent CancelRequest
// for the rest of the connection's life.
func (cc *connCancel) begin(cancel context.CancelFunc) (end func()) {
	cc.mu.Lock()
	cc.cancel = cancel
	cc.mu.Unlock()
	return func() {
		cc.mu.Lock()
		cc.cancel = nil
		cc.mu.Unlock()
	}
}

// cancelCurrent calls whatever CancelFunc is currently registered, if any
// (a no-op while the connection is idle between queries).
func (cc *connCancel) cancelCurrent() {
	cc.mu.Lock()
	cancel := cc.cancel
	cc.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// cancelRegistry tracks every currently-connected pgwire connection's
// cancelKey and connCancel, so performHandshake (pgwire.go) can look one
// up when a CancelRequest arrives.
type cancelRegistry struct {
	mu      sync.Mutex
	entries map[cancelKey]*connCancel
}

// globalCancelRegistry is process-wide: a CancelRequest can arrive on any
// TCP/UDS listener and must be able to reach a connection accepted by any
// other, so this cannot be scoped to one connection or listener.
var globalCancelRegistry = &cancelRegistry{entries: make(map[cancelKey]*connCancel)}

// register adds a new connection under a freshly generated, currently
// unused cancelKey, returning the key (to send via BackendKeyData), the
// connCancel handleConnection's per-message loop calls begin/end on for
// each query, and an unregister func the caller must run when the
// connection ends (typically deferred right after calling register).
func (r *cancelRegistry) register() (key cancelKey, cc *connCancel, unregister func()) {
	cc = &connCancel{}
	r.mu.Lock()
	defer r.mu.Unlock()
	for {
		key = cancelKey{pid: rand.Int31(), secret: rand.Int31()}
		if _, exists := r.entries[key]; !exists {
			break
		}
	}
	r.entries[key] = cc
	return key, cc, func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		delete(r.entries, key)
	}
}

// handleCancelRequest reads a CancelRequest's body (length is the Int32
// length readStartupHeader already parsed, including the 8-byte header it
// consumed) -- Int32 PID + Int32 secret -- and, if well-formed, looks up
// and cancels the matching connection's currently-running query (if any)
// in globalCancelRegistry. Malformed bodies (wrong length, or a read
// failure) are ignored: a CancelRequest never gets any response either
// way (performHandshake, pgwire.go).
func handleCancelRequest(conn net.Conn, length int32) {
	body := make([]byte, length-8)
	if _, err := io.ReadFull(conn, body); err != nil {
		return
	}
	if len(body) != 8 {
		return
	}
	key := cancelKey{
		pid:    int32(binary.BigEndian.Uint32(body[0:4])),
		secret: int32(binary.BigEndian.Uint32(body[4:8])),
	}
	globalCancelRegistry.cancel(key)
}

// cancel looks up key and calls its connCancel's cancelCurrent if found,
// reporting whether a matching connection existed. A CancelRequest naming
// an unknown key, or one whose secret does not match (cancelKey's
// equality requires both fields), or one for a connection that has
// already ended, is silently ignored -- matching real PostgreSQL, which
// never sends any response to a CancelRequest either way
// (performHandshake, pgwire.go, always closes that connection immediately
// after this call regardless of the outcome). If the target connection is
// idle (no query currently running), this is a no-op, also matching real
// PostgreSQL.
func (r *cancelRegistry) cancel(key cancelKey) bool {
	r.mu.Lock()
	cc, ok := r.entries[key]
	r.mu.Unlock()
	if ok {
		cc.cancelCurrent()
	}
	return ok
}
