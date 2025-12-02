package stream

// RecordingConsumer wraps a Recording to implement the Consumer interface.
type RecordingConsumer struct {
	manager   *RecordingManager
	inputName string
	inputURL  string
}

// NewRecordingConsumer creates a new consumer for a recording.
func NewRecordingConsumer(manager *RecordingManager, inputName string, inputURL string) *RecordingConsumer {
	return &RecordingConsumer{
		manager:   manager,
		inputName: inputName,
		inputURL:  inputURL,
	}
}

// Start begins consuming from the specified input (no-op, already started).
func (rc *RecordingConsumer) Start(inputName string, inputURL string) error {
	// Recording is already started when created
	// This method exists to satisfy the Consumer interface
	return nil
}

// Stop stops the recording.
func (rc *RecordingConsumer) Stop(inputName string) error {
	// Stop the recording
	rc.manager.StopRecording(rc.inputName, rc.inputURL)
	return nil
}

// OnFailure handles failure by delegating to the cleanup handler (dependency injection).
func (rc *RecordingConsumer) OnFailure(handler ConsumerCleanupHandler) error {
	rc.manager.Logger.Debug("RecordingConsumer failure", "inputName", rc.inputName)
	// Handler knows how to clean up - we just provide the data
	return handler.OnConsumerFailure(rc.inputURL, rc.GetConsumerID())
}

// GetConsumerType returns "recording".
func (rc *RecordingConsumer) GetConsumerType() string {
	return "recording"
}

// GetConsumerID returns the recording name as the unique identifier.
func (rc *RecordingConsumer) GetConsumerID() string {
	return "recording-" + rc.inputName
}
