package sshops

import (
	"errors"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestConnKey(t *testing.T) {
	key := connKey("192.168.1.1", "tom", "/home/tom/.ssh/id_ed25519")
	if key != "tom@192.168.1.1:/home/tom/.ssh/id_ed25519" {
		t.Errorf("unexpected key: %q", key)
	}
}

func TestConnKey_DifferentUsersDistinct(t *testing.T) {
	k1 := connKey("192.0.2.1", "alice", "/key")
	k2 := connKey("192.0.2.1", "bob", "/key")
	if k1 == k2 {
		t.Error("different users should produce different keys")
	}
}

func TestConnKey_DifferentKeysDistinct(t *testing.T) {
	k1 := connKey("192.0.2.1", "alice", "/key1")
	k2 := connKey("192.0.2.1", "alice", "/key2")
	if k1 == k2 {
		t.Error("different key paths should produce different keys")
	}
}

func TestIsConnErr_Nil(t *testing.T) {
	if isConnErr(nil) {
		t.Error("nil error should not be a connection error")
	}
}

func TestIsConnErr_EOF(t *testing.T) {
	if !isConnErr(errors.New("read tcp: EOF")) {
		t.Error("EOF should be a connection error")
	}
}

func TestIsConnErr_ConnectionReset(t *testing.T) {
	if !isConnErr(errors.New("read tcp: connection reset by peer")) {
		t.Error("connection reset should be a connection error")
	}
}

func TestIsConnErr_BrokenPipe(t *testing.T) {
	if !isConnErr(errors.New("write tcp: broken pipe")) {
		t.Error("broken pipe should be a connection error")
	}
}

func TestIsConnErr_ClosedConn(t *testing.T) {
	if !isConnErr(errors.New("use of closed network connection")) {
		t.Error("closed connection should be a connection error")
	}
}

func TestIsConnErr_SSHDisconnect(t *testing.T) {
	if !isConnErr(errors.New("ssh: disconnect, reason 11: disconnected by user")) {
		t.Error("ssh disconnect should be a connection error")
	}
}

func TestIsConnErr_RegularError(t *testing.T) {
	if isConnErr(errors.New("exit status 1")) {
		t.Error("exit status errors should NOT be connection errors")
	}
}

func TestIsConnErr_PermissionDenied(t *testing.T) {
	if isConnErr(errors.New("permission denied (publickey)")) {
		t.Error("auth errors should NOT be connection errors")
	}
}

func TestPool_Evict_NoEntry(t *testing.T) {
	p := &Pool{conns: make(map[string]*ssh.Client)}
	// Should not panic when evicting a key that was never added.
	p.evict("192.0.2.1", "user", "/key")
}

func TestPool_Evict_RemovesEntry(t *testing.T) {
	p := &Pool{conns: make(map[string]*ssh.Client)}
	key := connKey("192.0.2.1", "user", "/key")
	// Insert a nil entry (avoids needing a real *ssh.Client).
	p.conns[key] = nil
	p.evict("192.0.2.1", "user", "/key")
	if _, ok := p.conns[key]; ok {
		t.Error("evict should remove the entry from the map")
	}
}
