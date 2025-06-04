package status

type ServerStatus struct {
	CPU float64 `json:"cpu"`
	Mem uint64  `json:"mem"`
}

type EndpointStatus struct {
	OutputURL  string  `json:"output_url"`
	OutputName string  `json:"output_name"`
	Status     string  `json:"status"`
	Bitrate    float64 `json:"bitrate"`
	CPU        float64 `json:"cpu"`
	Mem        uint64  `json:"mem"`
}

type RelayStatus struct {
	InputURL  string           `json:"input_url"`
	InputName string           `json:"input_name"`
	Endpoints []EndpointStatus `json:"endpoints"`
}

type FullStatus struct {
	Server ServerStatus  `json:"server"`
	Relays []RelayStatus `json:"relays"`
}
