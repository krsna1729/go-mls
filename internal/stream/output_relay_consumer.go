package stream

import (
	"fmt"
)

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
	// We pass a generic error here since we don't have the specific error context
	return handler.OnConsumerDone(orc.inputURL, orc.GetConsumerID(), fmt.Errorf("consumer failed"))
}

// GetConsumerType returns "output".
func (orc *OutputRelayConsumer) GetConsumerType() string {
	return "output"
}

// GetConsumerID returns the output URL as the unique identifier.
func (orc *OutputRelayConsumer) GetConsumerID() string {
	return orc.relay.OutputURL
}
