package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go-mls/internal/state"
)

func TestHandleExportFormat(t *testing.T) {
	store := state.NewStore()
	store.AddInput(&state.Input{StreamPath: "test1", RemoteURL: "rtmp://source1"})
	store.AddOutput(&state.Output{StreamPath: "test1", OutputID: "out1", RemoteURL: "rtmp://dest1"})
	store.AddInput(&state.Input{StreamPath: "test2", RemoteURL: "rtmp://source2"})
	store.AddOutput(&state.Output{StreamPath: "test2", OutputID: "out2", RemoteURL: "rtmp://dest2"})

	s := &Server{store: store}
	req := httptest.NewRequest(http.MethodGet, "/system/export", nil)
	w := httptest.NewRecorder()
	s.handleExport(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	contentType := w.Header().Get("Content-Type")
	if !strings.Contains(contentType, "application/json") {
		t.Errorf("expected application/json, got %s", contentType)
	}

	disp := w.Header().Get("Content-Disposition")
	if !strings.Contains(disp, "relay_config.json") {
		t.Errorf("expected filename relay_config.json, got %s", disp)
	}

	var relays []exportRelay
	if err := json.Unmarshal(w.Body.Bytes(), &relays); err != nil {
		t.Fatalf("failed to parse export: %v", err)
	}

	if len(relays) != 2 {
		t.Errorf("expected 2 relays, got %d", len(relays))
	}

	for _, relay := range relays {
		if len(relay.Outputs) != 1 {
			t.Errorf("expected 1 output per relay, got %d for %s", len(relay.Outputs), relay.InputName)
		}
		if relay.InputURL == "" || relay.InputName == "" {
			t.Error("relay missing input_url or input_name")
		}
	}
}

func TestParseImportRelay(t *testing.T) {
	inputJSON := `[
		{
			"input_url": "rtmp://source1",
			"input_name": "stream1",
			"outputs": [
				{
					"output_url": "rtmp://dest1",
					"output_name": "out1",
					"platform_preset": "YouTube",
					"ffmpeg_options": {
						"video_codec": "libx264",
						"resolution": "1920x1080"
					}
				}
			]
		},
		{
			"input_name": "push_stream",
			"outputs": [
				{
					"output_url": "rtmp://dest2",
					"output_name": "out2"
				}
			]
		}
	]`

	var relays []importRelay
	if err := json.Unmarshal([]byte(inputJSON), &relays); err != nil {
		t.Fatalf("failed to parse import: %v", err)
	}

	if len(relays) != 2 {
		t.Errorf("expected 2 relays, got %d", len(relays))
	}

	relay := relays[0]
	if relay.InputURL != "rtmp://source1" {
		t.Errorf("expected input_url rtmp://source1, got %s", relay.InputURL)
	}
	if relay.InputName != "stream1" {
		t.Errorf("expected input_name stream1, got %s", relay.InputName)
	}
	if len(relay.Outputs) != 1 {
		t.Fatalf("expected 1 output, got %d", len(relay.Outputs))
	}

	out := relay.Outputs[0]
	if out.OutputURL != "rtmp://dest1" {
		t.Errorf("expected output_url rtmp://dest1, got %s", out.OutputURL)
	}
	if out.OutputName != "out1" {
		t.Errorf("expected output_name out1, got %s", out.OutputName)
	}
	if out.PlatformPreset != "YouTube" {
		t.Errorf("expected platform_preset YouTube, got %s", out.PlatformPreset)
	}
	if out.FFmpegOptions == nil {
		t.Error("expected ffmpeg_options")
	}
	if out.FFmpegOptions["video_codec"] != "libx264" {
		t.Errorf("expected video_codec libx264, got %s", out.FFmpegOptions["video_codec"])
	}

	relay2 := relays[1]
	if relay2.InputURL != "" {
		t.Errorf("expected empty input_url for push mode, got %s", relay2.InputURL)
	}
	if relay2.InputName != "push_stream" {
		t.Errorf("expected input_name push_stream, got %s", relay2.InputName)
	}
}

func TestPresetToArgs(t *testing.T) {
	preset, ok := state.GetPreset("YouTube")
	if !ok {
		t.Fatal("YouTube preset not found")
	}

	videoArgs, audioArgs := preset.ToArgs()

	foundVideoCodec := false
	for i, arg := range videoArgs {
		if arg == "-c:v" && i+1 < len(videoArgs) && videoArgs[i+1] == "libx264" {
			foundVideoCodec = true
			break
		}
	}
	if !foundVideoCodec {
		t.Error("expected -c:v libx264 in video args")
	}

	foundAudioCodec := false
	for i, arg := range audioArgs {
		if arg == "-c:a" && i+1 < len(audioArgs) && audioArgs[i+1] == "aac" {
			foundAudioCodec = true
			break
		}
	}
	if !foundAudioCodec {
		t.Error("expected -c:a aac in audio args")
	}
}
