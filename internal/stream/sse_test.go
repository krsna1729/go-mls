package stream

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestSSEBroker_NewSSEBroker(t *testing.T) {
	broker := NewSSEBroker()
	assert.NotNil(t, broker)
	assert.NotNil(t, broker.clients)
	assert.Equal(t, 0, len(broker.clients))
}

func TestSSEBroker_Broadcast(t *testing.T) {
	broker := NewSSEBroker()

	sub, unsubscribe := broker.Subscribe()
	defer unsubscribe()

	broker.Broadcast("test message")

	select {
	case msg := <-sub:
		assert.Equal(t, "test message", msg)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timeout waiting for message")
	}
}

func TestSSEBroker_Subscribe(t *testing.T) {
	broker := NewSSEBroker()

	sub, unsubscribe := broker.Subscribe()
	assert.NotNil(t, sub)
	assert.NotNil(t, unsubscribe)

	broker.mu.Lock()
	assert.Equal(t, 1, len(broker.clients))
	broker.mu.Unlock()

	unsubscribe()

	broker.mu.Lock()
	assert.Equal(t, 0, len(broker.clients))
	broker.mu.Unlock()
}

func TestSSEBroker_Unsubscribe(t *testing.T) {
	broker := NewSSEBroker()

	_, unsubscribe := broker.Subscribe()
	unsubscribe()

	broker.mu.Lock()
	assert.Equal(t, 0, len(broker.clients))
	broker.mu.Unlock()
}

func TestSSEBroker_MultipleSubscribers(t *testing.T) {
	broker := NewSSEBroker()

	sub1, unsub1 := broker.Subscribe()
	sub2, unsub2 := broker.Subscribe()
	defer func() {
		unsub1()
		unsub2()
	}()

	broker.mu.Lock()
	assert.Equal(t, 2, len(broker.clients))
	broker.mu.Unlock()

	broker.Broadcast("hello")

	select {
	case msg := <-sub1:
		assert.Equal(t, "hello", msg)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timeout waiting for message on sub1")
	}

	select {
	case msg := <-sub2:
		assert.Equal(t, "hello", msg)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timeout waiting for message on sub2")
	}
}
