package state

type FFmpegOptions struct {
	VideoCodec string   `json:"video_codec,omitempty"`
	AudioCodec string   `json:"audio_codec,omitempty"`
	Resolution string   `json:"resolution,omitempty"`
	Framerate  string   `json:"framerate,omitempty"`
	Bitrate    string   `json:"bitrate,omitempty"`
	Rotation   string   `json:"rotation,omitempty"`
	ExtraArgs  []string `json:"extra_args,omitempty"`
}

type PlatformPreset struct {
	Name    string        `json:"name"`
	Options FFmpegOptions `json:"options"`
}

var PlatformPresets = map[string]PlatformPreset{
	"YouTube": {
		Name: "YouTube",
		Options: FFmpegOptions{
			VideoCodec: "libx264",
			AudioCodec: "aac",
			Resolution: "1920x1080",
			Framerate:  "30",
			Bitrate:    "4500k",
		},
	},
	"Facebook": {
		Name: "Facebook",
		Options: FFmpegOptions{
			VideoCodec: "libx264",
			AudioCodec: "aac",
			Resolution: "1280x720",
			Framerate:  "30",
			Bitrate:    "2500k",
		},
	},
	"Twitch": {
		Name: "Twitch",
		Options: FFmpegOptions{
			VideoCodec: "libx264",
			AudioCodec: "aac",
			Resolution: "1920x1080",
			Framerate:  "60",
			Bitrate:    "6000k",
		},
	},
	"Instagram": {
		Name: "Instagram",
		Options: FFmpegOptions{
			VideoCodec: "libx264",
			AudioCodec: "aac",
			Resolution: "720x1280",
			Framerate:  "30",
			Bitrate:    "3500k",
			Rotation:   "transpose=1",
		},
	},
	"Custom": {
		Name:    "Custom",
		Options: FFmpegOptions{},
	},
}

func ListPresets() []PlatformPreset {
	presets := make([]PlatformPreset, 0, len(PlatformPresets))
	for _, p := range PlatformPresets {
		presets = append(presets, p)
	}
	return presets
}

func GetPreset(name string) (PlatformPreset, bool) {
	p, ok := PlatformPresets[name]
	return p, ok
}

func (p *PlatformPreset) ToArgs() (videoArgs, audioArgs []string) {
	opts := p.Options
	if opts.VideoCodec != "" {
		videoArgs = append(videoArgs, "-c:v", opts.VideoCodec)
	}
	if opts.Resolution != "" {
		videoArgs = append(videoArgs, "-s", opts.Resolution)
	}
	if opts.Framerate != "" {
		videoArgs = append(videoArgs, "-r", opts.Framerate)
	}
	if opts.Bitrate != "" {
		videoArgs = append(videoArgs, "-b:v", opts.Bitrate)
	}
	if opts.Rotation != "" {
		videoArgs = append(videoArgs, "-vf", opts.Rotation)
	}
	if opts.AudioCodec != "" {
		audioArgs = append(audioArgs, "-c:a", opts.AudioCodec)
	}
	if len(opts.ExtraArgs) > 0 {
		videoArgs = append(videoArgs, opts.ExtraArgs...)
	}
	return
}
