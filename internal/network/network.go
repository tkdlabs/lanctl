package network

import (
	"encoding/hex"
	"fmt"
	"net"
	"strings"
	"time"
)

// SendMagicPacket sends a Wake-on-LAN magic packet via UDP broadcast.
func SendMagicPacket(mac, broadcast string, port int) error {
	macHex := strings.NewReplacer(":", "", "-", "").Replace(mac)
	macBytes, err := hex.DecodeString(macHex)
	if err != nil || len(macBytes) != 6 {
		return fmt.Errorf("invalid MAC address: %s", mac)
	}

	packet := make([]byte, 102)
	for i := 0; i < 6; i++ {
		packet[i] = 0xff
	}
	for i := 0; i < 16; i++ {
		copy(packet[6+i*6:], macBytes)
	}

	pc, err := net.ListenPacket("udp4", "")
	if err != nil {
		return fmt.Errorf("listen UDP: %w", err)
	}
	defer pc.Close()

	addr, err := net.ResolveUDPAddr("udp4", fmt.Sprintf("%s:%d", broadcast, port))
	if err != nil {
		return fmt.Errorf("resolve broadcast addr: %w", err)
	}

	_, err = pc.WriteTo(packet, addr)
	return err
}

// CheckSSHPort returns true if port 22 on host accepts a TCP connection within timeout.
func CheckSSHPort(host string, timeout time.Duration) bool {
	conn, err := net.DialTimeout("tcp", host+":22", timeout)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}
