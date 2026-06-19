package network

import (
	"testing"
)

func TestSendMagicPacket_InvalidMAC(t *testing.T) {
	tests := []struct {
		name string
		mac  string
	}{
		{"too short", "aa:bb:cc"},
		{"invalid hex", "gg:hh:ii:jj:kk:ll"},
		{"empty", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := SendMagicPacket(tt.mac, "255.255.255.255", 9)
			if err == nil {
				t.Errorf("expected error for MAC %q, got nil", tt.mac)
			}
		})
	}
}

func TestSendMagicPacket_ValidMAC(t *testing.T) {
	// Just verify packet construction doesn't error — actual UDP send will fail
	// in sandboxed environments so we don't assert on that.
	macs := []string{
		"aa:bb:cc:dd:ee:ff",
		"AA:BB:CC:DD:EE:FF",
		"aa-bb-cc-dd-ee-ff",
	}
	for _, mac := range macs {
		err := SendMagicPacket(mac, "127.0.0.1", 19999)
		// May error on network send but not on MAC parsing
		if err != nil && err.Error() == "invalid MAC address: "+mac {
			t.Errorf("MAC %q should be valid but got: %v", mac, err)
		}
	}
}

func TestCheckSSHPort_Unreachable(t *testing.T) {
	// Port 22 on 192.0.2.1 (TEST-NET) should not be reachable.
	ok := CheckSSHPort("192.0.2.1", 100)
	if ok {
		t.Error("expected CheckSSHPort to return false for unreachable host")
	}
}
