package stream

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// MockConsumer is a test implementation of the Consumer interface
type MockConsumer struct {
	id           string
	consumerType string
	startCalled  bool
	stopCalled   bool
}

func (mc *MockConsumer) Start(inputName string, inputURL string) error {
	mc.startCalled = true
	return nil
}

func (mc *MockConsumer) Stop(inputName string) error {
	mc.stopCalled = true
	return nil
}

func (mc *MockConsumer) OnFailure(handler ConsumerCleanupHandler) error {
	// Mock implementation - just call the handler
	return handler.OnConsumerFailure("mock-input-url", mc.id)
}

func (mc *MockConsumer) GetConsumerType() string {
	return mc.consumerType
}

func (mc *MockConsumer) GetConsumerID() string {
	return mc.id
}

func TestConsumerRegistry_RegisterAndGet(t *testing.T) {
	registry := NewConsumerRegistry()

	consumer1 := &MockConsumer{id: "consumer-1", consumerType: "output"}
	consumer2 := &MockConsumer{id: "consumer-2", consumerType: "recording"}

	inputURL := "rtsp://test/stream"

	// Register consumers
	registry.Register(inputURL, consumer1)
	registry.Register(inputURL, consumer2)

	// Get consumers
	consumers := registry.GetConsumers(inputURL)
	require.Equal(t, 2, len(consumers))
	assert.Equal(t, 2, registry.GetConsumerCount(inputURL))
}

func TestConsumerRegistry_Unregister(t *testing.T) {
	registry := NewConsumerRegistry()

	consumer1 := &MockConsumer{id: "consumer-1", consumerType: "output"}
	consumer2 := &MockConsumer{id: "consumer-2", consumerType: "recording"}

	inputURL := "rtsp://test/stream"

	// Register consumers
	registry.Register(inputURL, consumer1)
	registry.Register(inputURL, consumer2)

	// Unregister one consumer
	found := registry.Unregister(inputURL, "consumer-1")
	assert.True(t, found)
	assert.Equal(t, 1, registry.GetConsumerCount(inputURL))

	// Verify correct consumer was removed
	consumers := registry.GetConsumers(inputURL)
	require.Equal(t, 1, len(consumers))
	assert.Equal(t, "consumer-2", consumers[0].GetConsumerID())

	// Unregister last consumer
	found = registry.Unregister(inputURL, "consumer-2")
	assert.True(t, found)
	assert.Equal(t, 0, registry.GetConsumerCount(inputURL))

	// Verify input URL is removed from map when no consumers remain
	consumers = registry.GetConsumers(inputURL)
	assert.Equal(t, 0, len(consumers))
}

func TestConsumerRegistry_UnregisterNonExistent(t *testing.T) {
	registry := NewConsumerRegistry()

	inputURL := "rtsp://test/stream"

	// Try to unregister from empty registry
	found := registry.Unregister(inputURL, "nonexistent")
	assert.False(t, found)

	// Register a consumer
	consumer := &MockConsumer{id: "consumer-1", consumerType: "output"}
	registry.Register(inputURL, consumer)

	// Try to unregister non-existent consumer
	found = registry.Unregister(inputURL, "nonexistent")
	assert.False(t, found)

	// Verify original consumer still exists
	assert.Equal(t, 1, registry.GetConsumerCount(inputURL))
}

func TestConsumerRegistry_MultipleInputs(t *testing.T) {
	registry := NewConsumerRegistry()

	input1 := "rtsp://test/stream1"
	input2 := "rtsp://test/stream2"

	consumer1 := &MockConsumer{id: "consumer-1", consumerType: "output"}
	consumer2 := &MockConsumer{id: "consumer-2", consumerType: "recording"}

	// Register different consumers for different inputs
	registry.Register(input1, consumer1)
	registry.Register(input2, consumer2)

	// Verify each input has correct consumer
	assert.Equal(t, 1, registry.GetConsumerCount(input1))
	assert.Equal(t, 1, registry.GetConsumerCount(input2))

	// Get all inputs
	inputs := registry.GetAllInputs()
	assert.Equal(t, 2, len(inputs))
	assert.Contains(t, inputs, input1)
	assert.Contains(t, inputs, input2)
}

func TestConsumerRegistry_Concurrent(t *testing.T) {
	registry := NewConsumerRegistry()
	inputURL := "rtsp://test/stream"

	// Concurrently register and unregister consumers
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func(id int) {
			consumer := &MockConsumer{
				id:           string(rune('A' + id)),
				consumerType: "test",
			}
			registry.Register(inputURL, consumer)
			registry.Unregister(inputURL, consumer.GetConsumerID())
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}

	// Final count should be 0
	assert.Equal(t, 0, registry.GetConsumerCount(inputURL))
}
