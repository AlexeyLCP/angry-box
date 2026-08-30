package ssh

import (
	"fmt"
	"strings"
)

// QuotePOSIX wraps s in POSIX single quotes so it is safe to interpolate into
// a remote shell command. Rejects empty strings and control characters that
// would still break a quoted word (NUL / newline / CR).
func QuotePOSIX(s string) (string, error) {
	if s == "" {
		return "", fmt.Errorf("ssh: empty string cannot be shell-quoted")
	}
	if strings.ContainsAny(s, "\x00\n\r") {
		return "", fmt.Errorf("ssh: string contains control characters")
	}
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'", nil
}
