package api

import (
	"encoding/json"
	"net/http"
	"os"
	"runtime"
	"time"
)

var startTime = time.Now()

// Version is set at build time via ldflags: -X github.com/alexeylcp/lucx-core/internal/api.Version=1.0.0
var Version = "dev"

type StatusResponse struct {
	Version   string `json:"version"`
	GoVersion string `json:"go_version"`
	Uptime    string `json:"uptime"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
	NumCPU    int    `json:"num_cpu"`
	PID       int    `json:"pid"`
	DBPath    string `json:"db_path"`
}

func (h *Handlers) handleStatus() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		resp := StatusResponse{
			Version:   Version,
			GoVersion: runtime.Version(),
			Uptime:    time.Since(startTime).Round(time.Second).String(),
			OS:        runtime.GOOS,
			Arch:      runtime.GOARCH,
			NumCPU:    runtime.NumCPU(),
			PID:       os.Getpid(),
			DBPath:    h.Store.DBPath(),
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}
}
