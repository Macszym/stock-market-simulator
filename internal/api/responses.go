package api

import (
	"encoding/json"
	"net/http"
)

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// Encode failure here means the client disconnected after we wrote the
	// status line; nothing useful left to do.
	_ = json.NewEncoder(w).Encode(body)
}
