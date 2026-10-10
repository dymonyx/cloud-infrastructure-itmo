package httpx

import (
	"encoding/json"
	"net/http"

	"chainwise-lab2/internal/model"
)

func WriteJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func WriteError(w http.ResponseWriter, status int, message string, traceID string) {
	WriteJSON(w, status, model.ErrorResponse{Error: message, TraceID: traceID})
}
