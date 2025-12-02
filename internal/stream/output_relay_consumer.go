package stream

// OutputRelayConsumer wraps an OutputRelay to implement the Consumer interface.
type OutputRelayConsumer struct {
	relay    *OutputRelay
	manager  *OutputRelayManager
	inputURL string // Stored for OnFailure
}

// NewOutputRelayConsumer creates a new consumer for an output relay.
func NewOutputRelayConsumer(relay *OutputRelay, manager *OutputRelayManager, inputURL string) *OutputRelayConsumer {
	return &OutputRelayConsumer{
		relay:    relay,
		manager:  manager,
		inputURL: inputURL,
	}
}

// Start begins consuming from the specified input (no-op for outputs, already started).
func (orc *OutputRelayConsumer) Start(inputName string, inputURL string) error {
	// Output relay is already started when created
	// This method exists to satisfy the Consumer interface
	return nil
}

// Stop stops consuming from the specified input.
func (orc *OutputRelayConsumer) Stop(inputName string) error {
	// Stop the output relay
	orc.manager.StopOutputRelay(orc.relay.OutputURL)
	return nil
}

// OnFailure handles failure by delegating to the cleanup handler (dependency injection).
func (orc *OutputRelayConsumer) OnFailure(handler ConsumerCleanupHandler) error {
	orc.manager.Logger.Debug("OutputRelayConsumer failure", "outputURL", orc.relay.OutputURL)
	// Handler knows how to clean up - we just provide the data
	return handler.OnConsumerFailure(orc.inputURL, orc.GetConsumerID())
}

// GetConsumerType returns "output".
func (orc *OutputRelayConsumer) GetConsumerType() string {
	return "output"
}

// GetConsumerID returns the output URL as the unique identifier.
func (orc *OutputRelayConsumer) GetConsumerID() string {
	return orc.relay.OutputURL
}
