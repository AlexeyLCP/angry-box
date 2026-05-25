package api

import (
	"encoding/json"
	"net/http"
)

// LogEntry represents a single event in the system log.
type LogEntry struct {
	Timestamp string `json:"timestamp"`
	ChainID   string `json:"chain_id"`
	ChainName string `json:"chain_name"`
	Event     string `json:"event"` // "apply", "rollback", "create", "delete"
	Status    string `json:"status"`
}

func (h *Handlers) handleLogs() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		chains, _ := h.Store.ListChains()
		entries := make([]LogEntry, 0)

		for _, c := range chains {
			if c.Status == "active" && c.AppliedAt != nil {
				entries = append(entries, LogEntry{
					Timestamp: c.AppliedAt.Format("2006-01-02T15:04:05Z"),
					ChainID:   c.ID,
					ChainName: c.Name,
					Event:     "apply",
					Status:    "success",
				})
			}
			// creation event
			entries = append(entries, LogEntry{
				Timestamp: c.CreatedAt.Format("2006-01-02T15:04:05Z"),
				ChainID:   c.ID,
				ChainName: c.Name,
				Event:     "create",
				Status:    "success",
			})
		}

		// Sort by timestamp descending (naive — show recent first)
		for i := 0; i < len(entries); i++ {
			for j := i + 1; j < len(entries); j++ {
				if entries[j].Timestamp > entries[i].Timestamp {
					entries[i], entries[j] = entries[j], entries[i]
				}
			}
		}

		// Cap at 100
		if len(entries) > 100 {
			entries = entries[:100]
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(entries)
	}
}
