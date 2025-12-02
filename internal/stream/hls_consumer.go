package stream

// HLSConsumer wraps an HLS session to implement the Consumer interface.
type HLSConsumer struct {
	manager   *HLSManager
	inputName string
	inputURL  string
}

// NewHLSConsumer creates a new HLSConsumer wrapper
func NewHLSConsumer(hlsManager *HLSManager, inputName string, inputURL string) *HLSConsumer {
	return &HLSConsumer{
		manager:   hlsManager, // Changed from hlsManager: hlsManager to manager: hlsManager to match struct field name
		inputName: inputName,
		inputURL:  inputURL,
	}
}

// Start begins consuming from the specified input (no-op, already started).
func (hc *HLSConsumer) Start(inputName string, inputURL string) error {
	// HLS session is already started when created
	// This method exists to satisfy the Consumer interface
	return nil
}

// Stop stops the HLS session.
func (hc *HLSConsumer) Stop(inputName string) error {
	// HLS sessions are managed by viewer lifecycle
	// This is a no-op as sessions clean up via heartbeat timeout
	return nil
}

// OnFailure handles failure by delegating to the cleanup handler (dependency injection).
func (hc *HLSConsumer) OnFailure(handler ConsumerCleanupHandler) error {
	hc.manager.Logger.Debug("HLSConsumer failure", "inputName", hc.inputName)
	// Handler knows how to clean up - we just provide the data
	return handler.OnConsumerFailure(hc.inputURL, hc.GetConsumerID())
}

// GetConsumerType returns "hls".
func (hc *HLSConsumer) GetConsumerType() string {
	return "hls"
}

// GetConsumerID returns the HLS session ID as the unique identifier.
func (hc *HLSConsumer) GetConsumerID() string {
	return "hls-" + hc.inputName
}
