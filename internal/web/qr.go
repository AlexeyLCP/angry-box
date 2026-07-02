package web

// qr.go — QR code image handler (extracted from ui.go as part of the M11 split).

import (
	"net/http"

	"github.com/alexeylcp/angry-box/internal/i18n"
	qrcode "github.com/skip2/go-qrcode"
)

// handleQRImage generates a QR code PNG for the given data query parameter.
func (s *Server) handleQRImage(w http.ResponseWriter, r *http.Request) {
	data := r.URL.Query().Get("data")
	if data == "" {
		http.Error(w, i18n.T(r.Context(), "missing data parameter"), http.StatusBadRequest)
		return
	}

	png, err := qrcode.Encode(data, qrcode.Medium, 256)
	if err != nil {
		http.Error(w, i18n.T(r.Context(), "qr generation failed"), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Write(png)
}