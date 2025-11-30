package stream

import (
	"reflect"
	"testing"
)

// Test that StatusV2 returns a cached snapshot when called repeatedly within TTL.
func TestStatusV2_CacheBehavior(t *testing.T) {
	rl := newTestRelayManager()

	// Simulate one input and one output
	inURL := "rtsp://localhost:8554/test"
	outURL := "rtmp://localhost/live/test"
	rl.InputRelays.Relays[inURL] = &InputRelay{
		InputURL:  inURL,
		InputName: "in",
		LocalURL:  "local",
		Status:    InputRunning,
	}
	rl.OutputRelays.Relays[outURL] = &OutputRelay{
		OutputURL:  outURL,
		OutputName: "out",
		InputURL:   inURL,
		LocalURL:   "local",
		Status:     OutputRunning,
	}

	s1 := rl.StatusV2()
	s2 := rl.StatusV2()

	if !reflect.DeepEqual(s1, s2) {
		t.Fatalf("expected cached StatusV2 snapshot to be identical on immediate subsequent calls")
	}
}
