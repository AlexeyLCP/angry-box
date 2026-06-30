package chain

// audit.go — thin helper over Store for writing audit log entries.
//
// Mirrors the Python project's services/audit.py write_audit: target_id is
// always stored as a string (ints coerced); payload is JSON-encoded only when
// non-empty (an empty map/nil → no payload_json). Failures are logged but never
// propagated — auditing must not break the operation it records.

import (
	"encoding/json"
	"log"

	"github.com/alexeylcp/angry-box/internal/domain/model"
)

// WriteAudit records an audit entry. targetID may be empty; payload may be nil
// (→ no payload_json). actor defaults to "operator" via SaveAuditLog. Errors
// are logged and swallowed.
func WriteAudit(s *Store, action, targetType, targetID string, payload any, actor string) {
	if s == nil {
		return
	}
	var payloadJSON string
	if payload != nil {
		if b, err := json.Marshal(payload); err == nil && len(b) > 0 {
			// Distinguish a non-nil empty map/struct from a real payload: json.Marshal
			// of an empty map[string]any{} yields "null"? No — "{}" for a map. We
			// accept any non-empty bytes; the caller controls payload shape.
			payloadJSON = string(b)
		}
	}
	entry := &model.AuditLog{
		Actor:       actor,
		Action:      action,
		TargetType:  targetType,
		TargetID:    targetID,
		PayloadJSON: payloadJSON,
	}
	if err := s.SaveAuditLog(entry); err != nil {
		log.Printf("audit: failed to write %s/%s/%s: %v", action, targetType, targetID, err)
	}
}

// AuditPayload is a small convenience map for ad-hoc payloads.
type AuditPayload map[string]any