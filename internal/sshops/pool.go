package sshops

import (
	"fmt"
	"strings"
	"sync"

	"golang.org/x/crypto/ssh"
)

// Pool caches live *ssh.Client connections, keyed by "user@ip:keyPath".
// A single *ssh.Client supports many concurrent sessions, so all concurrent
// commands to the same host share one TCP + SSH handshake.
type Pool struct {
	mu    sync.Mutex
	conns map[string]*ssh.Client
}

// DefaultPool is the shared pool used by all exported sshops functions.
var DefaultPool = &Pool{conns: make(map[string]*ssh.Client)}

func connKey(ip, user, keyPath string) string {
	return user + "@" + ip + ":" + keyPath
}

// getOrDial returns a cached client or dials a new one.
// It must NOT be called while p.mu is held (dial is slow).
func (p *Pool) getOrDial(ip, user, keyPath string) (*ssh.Client, error) {
	key := connKey(ip, user, keyPath)

	p.mu.Lock()
	c := p.conns[key]
	p.mu.Unlock()
	if c != nil {
		return c, nil
	}

	// Dial outside the lock — takes ~1-2 s for handshake + auth.
	c, err := dial(ip, user, keyPath)
	if err != nil {
		return nil, err
	}

	p.mu.Lock()
	// Another goroutine may have connected first — use theirs.
	if existing := p.conns[key]; existing != nil {
		p.mu.Unlock()
		c.Close()
		return existing, nil
	}
	p.conns[key] = c
	p.mu.Unlock()
	return c, nil
}

// evict removes and closes the connection for the given coords.
func (p *Pool) evict(ip, user, keyPath string) {
	key := connKey(ip, user, keyPath)
	p.mu.Lock()
	c := p.conns[key]
	delete(p.conns, key)
	p.mu.Unlock()
	if c != nil {
		c.Close()
	}
}

// withClient runs fn with a pooled client. On a connection-level error it
// evicts the stale client and retries once with a fresh connection.
// fn should return the SSH error directly so the retry logic can inspect it.
func (p *Pool) withClient(ip, user, keyPath string, fn func(*ssh.Client) error) error {
	for attempt := 0; attempt < 2; attempt++ {
		c, err := p.getOrDial(ip, user, keyPath)
		if err != nil {
			return err
		}
		err = fn(c)
		if err != nil && isConnErr(err) {
			p.evict(ip, user, keyPath)
			continue
		}
		return err
	}
	return fmt.Errorf("ssh connection to %s lost (retried once)", ip)
}

// openSession returns a ready SSH session, retrying once on stale connections.
// Used by streaming functions that need direct session control.
func (p *Pool) openSession(ip, user, keyPath string) (*ssh.Session, error) {
	for attempt := 0; attempt < 2; attempt++ {
		c, err := p.getOrDial(ip, user, keyPath)
		if err != nil {
			return nil, err
		}
		sess, err := c.NewSession()
		if err != nil {
			if isConnErr(err) {
				p.evict(ip, user, keyPath)
				continue
			}
			return nil, err
		}
		return sess, nil
	}
	return nil, fmt.Errorf("could not open SSH session to %s (retried once)", ip)
}

// isConnErr returns true for errors that signal a broken SSH transport,
// as opposed to a remote command failure.
func isConnErr(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "EOF") ||
		strings.Contains(s, "connection reset") ||
		strings.Contains(s, "broken pipe") ||
		strings.Contains(s, "use of closed network connection") ||
		strings.Contains(s, "ssh: disconnect")
}
