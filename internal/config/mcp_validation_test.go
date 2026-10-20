package config

import "testing"

func TestMCPConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		config  MCPConfig
		wantErr bool
	}{
		{name: "stdio default", config: MCPConfig{Transport: "stdio"}},
		{name: "loopback behind proxy", config: MCPConfig{Transport: "streamable-http", Address: "127.0.0.1:8081"}},
		{name: "localhost behind proxy", config: MCPConfig{Transport: "streamable-http", Address: "localhost:8081"}},
		{name: "direct TLS", config: MCPConfig{Transport: "streamable-http", Address: "0.0.0.0:8081", TLSCertFile: "server.crt", TLSKeyFile: "server.key"}},
		{name: "unencrypted external listener", config: MCPConfig{Transport: "streamable-http", Address: "0.0.0.0:8081"}, wantErr: true},
		{name: "partial TLS configuration", config: MCPConfig{Transport: "streamable-http", Address: "127.0.0.1:8081", TLSCertFile: "server.crt"}, wantErr: true},
		{name: "missing address", config: MCPConfig{Transport: "streamable-http"}, wantErr: true},
		{name: "invalid address", config: MCPConfig{Transport: "streamable-http", Address: "not-an-address"}, wantErr: true},
		{name: "non-numeric port", config: MCPConfig{Transport: "streamable-http", Address: "127.0.0.1:http"}, wantErr: true},
		{name: "out-of-range port", config: MCPConfig{Transport: "streamable-http", Address: "127.0.0.1:65536"}, wantErr: true},
		{name: "unknown transport", config: MCPConfig{Transport: "sse", Address: "127.0.0.1:8081"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
