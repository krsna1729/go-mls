package state

import (
	"testing"
)

func TestListPresets(t *testing.T) {
	presets := ListPresets()
	if len(presets) < 4 {
		t.Errorf("expected at least 4 presets, got %d", len(presets))
	}

	presetMap := make(map[string]PlatformPreset)
	for _, p := range presets {
		presetMap[p.Name] = p
	}

	expectedPresets := []string{"YouTube", "Facebook", "Twitch", "Instagram", "Custom"}
	for _, name := range expectedPresets {
		if _, ok := presetMap[name]; !ok {
			t.Errorf("expected preset %q not found", name)
		}
	}
}

func TestGetPreset(t *testing.T) {
	preset, ok := GetPreset("YouTube")
	if !ok {
		t.Fatal("expected to find YouTube preset")
	}
	if preset.Options.VideoCodec != "libx264" {
		t.Errorf("expected video_codec libx264, got %s", preset.Options.VideoCodec)
	}
	if preset.Options.Bitrate != "4500k" {
		t.Errorf("expected bitrate 4500k, got %s", preset.Options.Bitrate)
	}

	_, ok = GetPreset("Nonexistent")
	if ok {
		t.Error("expected not to find nonexistent preset")
	}
}

func TestPresetToArgs(t *testing.T) {
	preset, _ := GetPreset("Twitch")
	videoArgs, audioArgs := preset.ToArgs()

	expectedVideoArgs := []string{"-c:v", "libx264", "-s", "1920x1080", "-r", "60", "-b:v", "6000k"}
	for i, arg := range expectedVideoArgs {
		if i >= len(videoArgs) || videoArgs[i] != arg {
			t.Errorf("expected video arg %d to be %q, got %v", i, arg, videoArgs)
		}
	}

	expectedAudioArgs := []string{"-c:a", "aac"}
	for i, arg := range expectedAudioArgs {
		if i >= len(audioArgs) || audioArgs[i] != arg {
			t.Errorf("expected audio arg %d to be %q, got %v", i, arg, audioArgs)
		}
	}
}

func TestPresetToArgsCustom(t *testing.T) {
	preset, _ := GetPreset("Custom")
	videoArgs, audioArgs := preset.ToArgs()
	if len(videoArgs) != 0 {
		t.Errorf("expected no video args for Custom preset, got %v", videoArgs)
	}
	if len(audioArgs) != 0 {
		t.Errorf("expected no audio args for Custom preset, got %v", audioArgs)
	}
}
