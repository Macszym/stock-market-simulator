package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWriteJSON_OKWithObject(t *testing.T) {
	rr := httptest.NewRecorder()
	writeJSON(rr, http.StatusOK, map[string]string{"status": "ok"})

	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, "application/json", rr.Header().Get("Content-Type"))

	var got map[string]string
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	require.Equal(t, map[string]string{"status": "ok"}, got)
}

// GET /wallets/{id}/stocks/{name} returns a bare JSON number (e.g. 99),
// so writeJSON must serialize scalars correctly, not just objects.
func TestWriteJSON_BareIntegerBody(t *testing.T) {
	rr := httptest.NewRecorder()
	writeJSON(rr, http.StatusOK, int64(42))

	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, "42\n", rr.Body.String())
}

func TestWriteError_PopulatesEnvelope(t *testing.T) {
	rr := httptest.NewRecorder()
	writeError(rr, http.StatusBadRequest, "INVALID_REQUEST", "malformed JSON body")

	require.Equal(t, http.StatusBadRequest, rr.Code)
	require.Equal(t, "application/json", rr.Header().Get("Content-Type"))

	var got errorResponse
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	require.Equal(t, errorResponse{Error: "malformed JSON body", Code: "INVALID_REQUEST"}, got)
}
