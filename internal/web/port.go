package web

// port.go provides port-range validation for the inbound save boundary.
// strconv.Atoi ignores parse errors and would otherwise pass 0 (or any
// negative / >65535 value) straight into the generated sing-box config
// (CTO-review L6).

import "fmt"

// validatePort returns an error unless port is in the valid TCP/UDP range
// 1..65535.
func validatePort(port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("invalid port %d: must be in 1..65535", port)
	}
	return nil
}