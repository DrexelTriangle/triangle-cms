package handlers

import (
	"net"
	"net/url"
	"testing"
)

// TestIsPublicUnicast covers the SSRF guard's classification directly. The
// internal cases are the ones that matter: each is an address a pasted image URL
// could resolve to in order to make the server fetch something on the caller's
// behalf.
func TestIsPublicUnicast(t *testing.T) {
	tests := []struct {
		name string
		ip   string
		want bool
	}{
		{"public v4", "93.184.216.34", true},
		{"public v6", "2606:2800:220:1:248:1893:25c8:1946", true},
		{"loopback v4", "127.0.0.1", false},
		{"loopback v4 alternate", "127.19.8.6", false},
		{"loopback v6", "::1", false},
		{"private 10/8", "10.0.0.5", false},
		{"private 172.16/12", "172.16.31.4", false},
		{"private 192.168/16", "192.168.1.1", false},
		{"unspecified", "0.0.0.0", false},
		// The cloud metadata endpoint is the classic SSRF target, and it is
		// covered by the link-local check rather than by a rule of its own.
		{"cloud metadata", "169.254.169.254", false},
		{"link-local v6", "fe80::1", false},
		{"multicast", "224.0.0.1", false},
		{"carrier-grade NAT low", "100.64.0.1", false},
		{"carrier-grade NAT high", "100.127.255.254", false},
		// 100.63 and 100.128 sit just outside 100.64.0.0/10 and must stay
		// reachable; the mask is easy to get wrong by a byte.
		{"below carrier-grade NAT", "100.63.255.255", true},
		{"above carrier-grade NAT", "100.128.0.1", true},
		{"unique-local v6", "fd00::1", false},
		{"unique-local v6 low", "fc00::1", false},
		// fe00::/8 shares its first nibble with fc00::/7 but is not unique-local.
		{"above unique-local v6", "fe00::1", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ip := net.ParseIP(tt.ip)
			if ip == nil {
				t.Fatalf("bad test fixture: %q is not an IP", tt.ip)
			}
			if got := isPublicUnicast(ip); got != tt.want {
				t.Fatalf("isPublicUnicast(%s) = %v, want %v", tt.ip, got, tt.want)
			}
		})
	}
}

// TestSafeDialControl checks the dialer hook itself, which is what actually
// blocks the connection. A v4-mapped v6 form is included because that is how a
// resolver can hand back an internal v4 address on a dual-stack host.
func TestSafeDialControl(t *testing.T) {
	tests := []struct {
		name    string
		address string
		wantErr bool
	}{
		{"public", "93.184.216.34:443", false},
		{"loopback", "127.0.0.1:80", true},
		{"private", "10.1.2.3:8080", true},
		{"metadata", "169.254.169.254:80", true},
		{"v4-mapped loopback", "[::ffff:127.0.0.1]:80", true},
		{"no port", "93.184.216.34", true},
		{"unresolved hostname", "example.com:443", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := safeDialControl("tcp", tt.address, nil)
			if tt.wantErr && err == nil {
				t.Fatalf("safeDialControl(%q) = nil, want error", tt.address)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("safeDialControl(%q) = %v, want nil", tt.address, err)
			}
		})
	}
}

func TestRemoteFileName(t *testing.T) {
	tests := []struct {
		raw  string
		want string
	}{
		{"https://example.com/wp-content/uploads/2026/07/photo.jpg", "photo.jpg"},
		{"https://example.com/image", "image"},
		{"https://example.com/", "pasted-image"},
		{"https://example.com", "pasted-image"},
		{"https://example.com/a/b/c.png?v=2", "c.png"},
	}

	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			u, err := url.Parse(tt.raw)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if got := remoteFileName(u); got != tt.want {
				t.Fatalf("remoteFileName(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}
