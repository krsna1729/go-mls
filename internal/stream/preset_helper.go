package stream

// applyPresetAndOptions applies platform preset and FFmpeg options with proper priority:
// 1. Apply platform preset if provided
// 2. Override with manual FFmpeg options if provided
// 3. If neither, try stored config (only if inputURL and outputURL are provided)
func (rm *RelayManager) applyPresetAndOptions(preset string, manualOpts map[string]string, inputURL, outputURL string) (*FFmpegOptions, string) {
	var opts *FFmpegOptions
	usedPreset := preset

	// Step 1: Apply platform preset if provided
	if preset != "" {
		if presetData, exists := PlatformPresets[preset]; exists {
			opts = &FFmpegOptions{
				VideoCodec: presetData.Options.VideoCodec,
				AudioCodec: presetData.Options.AudioCodec,
				Resolution: presetData.Options.Resolution,
				Framerate:  presetData.Options.Framerate,
				Bitrate:    presetData.Options.Bitrate,
				Rotation:   presetData.Options.Rotation,
			}
			rm.Logger.Debug("Applied platform preset", "preset", preset)
		} else {
			rm.Logger.Warn("Unknown platform preset", "preset", preset)
		}
	}

	// Step 2: Override with manual FFmpeg options if provided
	if manualOpts != nil {
		if opts == nil {
			opts = &FFmpegOptions{}
		}
		// Override only non-empty values
		if manualOpts["video_codec"] != "" {
			opts.VideoCodec = manualOpts["video_codec"]
		}
		if manualOpts["audio_codec"] != "" {
			opts.AudioCodec = manualOpts["audio_codec"]
		}
		if manualOpts["resolution"] != "" {
			opts.Resolution = manualOpts["resolution"]
		}
		if manualOpts["framerate"] != "" {
			opts.Framerate = manualOpts["framerate"]
		}
		if manualOpts["bitrate"] != "" {
			opts.Bitrate = manualOpts["bitrate"]
		}
		if manualOpts["rotation"] != "" {
			opts.Rotation = manualOpts["rotation"]
		}
		rm.Logger.Debug("Applied manual options overrides")
	}

	// Step 3: If still no options and no preset, try stored config (only for API calls with URLs)
	if opts == nil && preset == "" && inputURL != "" && outputURL != "" {
		storedPreset, storedOpts, err := rm.GetEndpointConfig(inputURL, outputURL)
		if err == nil {
			usedPreset = storedPreset
			opts = storedOpts
			rm.Logger.Debug("Using stored config", "preset", storedPreset)
		}
	}

	return opts, usedPreset
}
