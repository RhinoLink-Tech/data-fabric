package config

import (
	"net"
	"testing"
)

// freePort reserves an ephemeral port so the bind tests below never collide
// with a service that happens to listen on a fixed one.
func freePort(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("cannot reserve an ephemeral port: %v", err)
	}
	defer listener.Close()
	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatalf("split ephemeral address: %v", err)
	}
	return port
}

// TestHTTPListenAddressBuildsExpectedAddresses pins the address format for
// the four shapes operators configure (#127): empty host (all interfaces,
// containers), wildcard, loopback, and IPv6 loopback with brackets.
func TestHTTPListenAddressBuildsExpectedAddresses(t *testing.T) {
	tests := []struct {
		name string
		host string
		port string
		want string
	}{
		{"empty host binds all interfaces", "", "8080", ":8080"},
		{"wildcard host", "0.0.0.0", "8080", "0.0.0.0:8080"},
		{"ipv4 loopback", "127.0.0.1", "8080", "127.0.0.1:8080"},
		{"ipv6 loopback is bracketed", "::1", "8080", "[::1]:8080"},
		{"whitespace is trimmed", " 0.0.0.0 ", " 8080 ", "0.0.0.0:8080"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Config{Server: ServerConfig{Host: tt.host, Port: tt.port}}
			if got := cfg.HTTPListenAddress(); got != tt.want {
				t.Errorf("HTTPListenAddress() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestHTTPListenAddressBindsLoopback actually binds the built address: the
// kernel must accept it and the listener must sit on 127.0.0.1 (#127
// acceptance — behavior, not config-structure assertions).
func TestHTTPListenAddressBindsLoopback(t *testing.T) {
	port := freePort(t)
	cfg := Config{Server: ServerConfig{Host: "127.0.0.1", Port: port}}

	listener, err := net.Listen("tcp", cfg.HTTPListenAddress())
	if err != nil {
		t.Fatalf("net.Listen(%q) failed: %v", cfg.HTTPListenAddress(), err)
	}
	defer listener.Close()

	if got, want := listener.Addr().String(), "127.0.0.1:"+port; got != want {
		t.Errorf("listener bound %q, want %q", got, want)
	}
}

// TestHTTPListenAddressBindsIPv6Loopback binds [::1] where the environment
// has IPv6; environments without it skip rather than fail.
func TestHTTPListenAddressBindsIPv6Loopback(t *testing.T) {
	port := freePort(t)
	cfg := Config{Server: ServerConfig{Host: "::1", Port: port}}

	listener, err := net.Listen("tcp", cfg.HTTPListenAddress())
	if err != nil {
		t.Skipf("IPv6 unavailable in this environment: %v", err)
	}
	defer listener.Close()

	if got, want := listener.Addr().String(), "[::1]:"+port; got != want {
		t.Errorf("listener bound %q, want %q", got, want)
	}
}

// TestHTTPListenAddressBindsAllInterfaces covers the container shapes: an
// explicit wildcard and an empty host both have to produce a working
// listener on every interface.
func TestHTTPListenAddressBindsAllInterfaces(t *testing.T) {
	for _, host := range []string{"", "0.0.0.0"} {
		t.Run("host "+host, func(t *testing.T) {
			port := freePort(t)
			cfg := Config{Server: ServerConfig{Host: host, Port: port}}

			listener, err := net.Listen("tcp", cfg.HTTPListenAddress())
			if err != nil {
				t.Fatalf("net.Listen(%q) failed: %v", cfg.HTTPListenAddress(), err)
			}
			defer listener.Close()

			boundPort := listener.Addr().(*net.TCPAddr).Port
			if want := mustPort(t, port); boundPort != want {
				t.Errorf("listener port = %d, want %d", boundPort, want)
			}
		})
	}
}

// TestHTTPListenAddressRejectsInvalidHosts checks the startup failure path:
// an unresolvable or malformed host must fail the bind, not silently fall
// back to another interface (#127).
func TestHTTPListenAddressRejectsInvalidHosts(t *testing.T) {
	for _, host := range []string{"not a host", "999.999.999.999", "host;rm -rf"} {
		t.Run("host "+host, func(t *testing.T) {
			cfg := Config{Server: ServerConfig{Host: host, Port: "8080"}}
			listener, err := net.Listen("tcp", cfg.HTTPListenAddress())
			if err == nil {
				listener.Close()
				t.Fatalf("net.Listen(%q) unexpectedly succeeded", cfg.HTTPListenAddress())
			}
		})
	}
}

// TestServerValidateRejectsWhitespaceHost locks the config-level guard that
// runs before the listener is opened.
func TestServerValidateRejectsWhitespaceHost(t *testing.T) {
	cfg := ServerConfig{Host: "127.0.0.1 ", Port: "8080", Mode: "debug",
		ReadHeaderTimeout: 1, ReadTimeout: 1, WriteTimeout: 1, IdleTimeout: 1, MaxRequestBodyBytes: 1}
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() accepted a host containing whitespace")
	}
}

func mustPort(t *testing.T, port string) int {
	t.Helper()
	value, err := net.LookupPort("tcp", port)
	if err != nil {
		t.Fatalf("parse port %q: %v", port, err)
	}
	return value
}
