package web

// port_validation_test.go pins the port-range validation used at the inbound
// save boundary. Ports must be in 1..65535; the previous code did
// strconv.Atoi ignoring the error and passed 0 (or negative / >65535 values)
// straight into the generated sing-box config (CTO-review L6).

import "testing"

func TestValidatePort(t *testing.T) {
	tests := []struct {
		port int
		ok   bool
	}{
		{0, false},
		{-1, false},
		{1, true},
		{22, true},
		{443, true},
		{65535, true},
		{65536, false},
		{100000, false},
	}
	for _, tt := range tests {
		err := validatePort(tt.port)
		if tt.ok && err != nil {
			t.Errorf("validatePort(%d): unexpected error: %v", tt.port, err)
		}
		if !tt.ok && err == nil {
			t.Errorf("validatePort(%d): expected error, got nil", tt.port)
		}
	}
}