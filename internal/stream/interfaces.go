package stream

// StreamProvider defines the interface for providing input streams to consumers.
// This decouples consumers (Recording, HLS) from the concrete RelayManager.
type StreamProvider interface {
	// GetStream requests a stream for the given input name.
	// It returns the local RTSP URL for the stream.
	// The provider should increment a reference count for this consumer.
	GetStream(inputName string) (url string, err error)

	// ReleaseStream notifies the provider that a consumer has finished using the stream.
	// The provider should decrement the reference count.
	ReleaseStream(inputName string)
}
