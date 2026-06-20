# Feature 05: SSH Connection Pool

## Summary

Add `internal/sshops/pool.go` — a per-host SSH connection pool that eliminates
the full handshake + auth round-trip on every `/api/hosts` poll.

## Problem

The previous code called `dial()` inside every `ServiceStatuses`, `ServiceControl`,
`JournalLines`, and `Shutdown` call. SSH handshake + key exchange + auth costs
~0.5-1.5 s per host. With 6 hosts × 10 s poll cycle that's 3-9 s of blocking SSH
setup on every poll, which was the dominant latency in the already-fast Go port.

## Design

`Pool` caches `*ssh.Client` keyed by `"user@ip:keyPath"`. A single `*ssh.Client`
supports many concurrent sessions (one per command), so all goroutines hitting the
same host share one TCP connection and one SSH handshake.

Key behaviours:

- **Race-safe connect**: `getOrDial` holds the lock only to read/write the map;
  the slow `dial()` runs outside the lock. If two goroutines both miss the cache
  simultaneously, the second one closes its redundant connection.
- **Auto-reconnect**: `withClient` runs `fn(client)`, and on any connection-level
  error (`isConnErr`) evicts the stale entry and retries once with a fresh connection.
  Command errors (non-zero exit) are NOT retried.
- **Shutdown eviction**: `Shutdown` always calls `evict` after the command — the host
  is going offline and the connection would be stale anyway.
- **Stream sessions**: `openSession` (used by `StreamJournal`) also retries once on
  a stale connection before returning the session.
- **VPN repair**: uses `getOrDial` directly since it runs many sequential steps — a
  mid-sequence reconnect would restart from step 1, which is wrong.

## Expected impact

After the first poll (which still pays the handshake cost), subsequent polls reuse
the cached connections. The `/api/hosts` response time drops from ~5 s to the latency
of running `systemctl is-active` over an already-open SSH channel (~50-200 ms total
for all hosts concurrently).

## Files changed

- `internal/sshops/pool.go` — new: Pool type, DefaultPool, withClient, openSession, isConnErr
- `internal/sshops/sshops.go` — all public functions now use DefaultPool
- `internal/sshops/pool_test.go` — new: unit tests for connKey, isConnErr, Pool.evict
