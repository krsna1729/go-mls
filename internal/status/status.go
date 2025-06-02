package status

type ServerStatus struct {
	CPU float64 `json:"cpu"`
	Mem uint64  `json:"mem"`
	PID int     `json:"pid"`
}

type EndpointStatus struct {
	OutputURL string  `json:"output_url"`
	Running   bool    `json:"running"`
	Bitrate   float64 `json:"bitrate"`
	PID       int     `json:"pid"`
	CPU       float64 `json:"cpu"`
	Mem       uint64  `json:"mem"`
}

type RelayStatus struct {
	InputURL  string           `json:"input_url"`
	Endpoints []EndpointStatus `json:"endpoints"`
}

type FullStatus struct {
	Server ServerStatus  `json:"server"`
	Relays []RelayStatus `json:"relays"`
}
