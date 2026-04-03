package stream

func ApplyPresetAndOptions(preset string, manualOpts map[string]string) FFmpegOpts {
	var opts FFmpegOpts

	if preset != "" {
		if presetData, exists := PlatformPresets[preset]; exists {
			opts = FFmpegOpts{
				VideoCodec: presetData.Options.VideoCodec,
				AudioCodec: presetData.Options.AudioCodec,
				Resolution: presetData.Options.Resolution,
				Framerate:  presetData.Options.Framerate,
				Bitrate:    presetData.Options.Bitrate,
			}
		}
	}

	if manualOpts != nil {
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
	}

	return opts
}
