package model

type BikeProfile struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	Type              string `json:"type"`
	CurrentOdometerKM int    `json:"currentOdometerKm"`
	RidingStyle       string `json:"ridingStyle"`
}

type BikeCheckResponse struct {
	BikeProfile    BikeProfile `json:"bikeProfile"`
	Recommendation string      `json:"recommendation"`
	Priority       string      `json:"priority"`
}

type MessageResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	TraceID string `json:"trace_id,omitempty"`
}

type HealthResponse struct {
	Service string `json:"service"`
	Status  string `json:"status"`
}

type ErrorResponse struct {
	Error   string `json:"error"`
	TraceID string `json:"trace_id,omitempty"`
}
