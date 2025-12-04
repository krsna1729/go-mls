package stream

import (
	"sync"
)

// ConsumerCleanupHandler handles cleanup when a consumer fails.
// This interface is implemented by StreamManager and passed to consumers on failure.
type ConsumerCleanupHandler interface {
	OnConsumerDone(inputURL, consumerID string, err error) error
}

// Consumer represents any component that consumes from an input stream.
// Consumers include OutputRelay, Recording, and HLS sessions.
type Consumer interface {
	// Stop stops consuming from the given input
	Stop(inputName string) error

	// OnFailure is called when the consumer fails
	// The handler parameter provides cleanup logic via dependency injection
	OnFailure(handler ConsumerCleanupHandler) error

	// GetConsumerType returns the type of consumer (e.g., "output", "recording", "hls")
	GetConsumerType() string

	// GetConsumerID returns unique identifier for this consumer instance
	GetConsumerID() string
}

// ConsumerRegistry tracks all active consumers per input stream.
type ConsumerRegistry struct {
	consumers map[string][]Consumer // inputURL -> []Consumer
	mu        sync.RWMutex
}

// NewConsumerRegistry creates a new consumer registry.
func NewConsumerRegistry() *ConsumerRegistry {
	return &ConsumerRegistry{
		consumers: make(map[string][]Consumer),
	}
}

// Register adds a consumer to the registry for the given input URL.
func (cr *ConsumerRegistry) Register(inputURL string, consumer Consumer) {
	cr.mu.Lock()
	defer cr.mu.Unlock()
	cr.consumers[inputURL] = append(cr.consumers[inputURL], consumer)
}

// Unregister removes a consumer from the registry.
// Returns true if consumer was found and removed.
func (cr *ConsumerRegistry) Unregister(inputURL, consumerID string) bool {
	cr.mu.Lock()
	defer cr.mu.Unlock()

	consumers, exists := cr.consumers[inputURL]
	if !exists {
		return false
	}

	for i, c := range consumers {
		if c.GetConsumerID() == consumerID {
			// Remove element at index i
			cr.consumers[inputURL] = append(consumers[:i], consumers[i+1:]...)
			// fmt.Printf("Unregister: consumer %s removed from %s\n", consumerID, inputURL)
			// If the list becomes empty after removal, delete the key from the map
			if len(cr.consumers[inputURL]) == 0 {
				delete(cr.consumers, inputURL)
			}
			return true
		}
	}
	return false
}

// GetConsumers returns all consumers for the given input URL.
func (cr *ConsumerRegistry) GetConsumers(inputURL string) []Consumer {
	cr.mu.RLock()
	defer cr.mu.RUnlock()

	consumers := cr.consumers[inputURL]
	// Return a copy to avoid race conditions
	result := make([]Consumer, len(consumers))
	copy(result, consumers)
	return result
}

// GetConsumerCount returns the number of consumers for the given input URL.
func (cr *ConsumerRegistry) GetConsumerCount(inputURL string) int {
	cr.mu.RLock()
	defer cr.mu.RUnlock()
	return len(cr.consumers[inputURL])
}

// GetAllInputs returns all input URLs that have active consumers.
func (cr *ConsumerRegistry) GetAllInputs() []string {
	cr.mu.RLock()
	defer cr.mu.RUnlock()

	inputs := make([]string, 0, len(cr.consumers))
	for inputURL := range cr.consumers {
		inputs = append(inputs, inputURL)
	}
	return inputs
}
