package stream

type PlatformPreset struct {
	Name    string     `json:"name"`
	Options FFmpegOpts `json:"options"`
}

var PlatformPresets = map[string]PlatformPreset{
	"YouTube": {
		Name: "YouTube",
		Options: FFmpegOpts{
			VideoCodec: "libx264",
			AudioCodec: "aac",
			Resolution: "1920x1080",
			Framerate:  "30",
			Bitrate:    "4500k",
		},
	},
	"Facebook": {
		Name: "Facebook",
		Options: FFmpegOpts{
			VideoCodec: "libx264",
			AudioCodec: "aac",
			Resolution: "1280x720",
			Framerate:  "30",
			Bitrate:    "2500k",
		},
	},
	"Twitch": {
		Name: "Twitch",
		Options: FFmpegOpts{
			VideoCodec: "libx264",
			AudioCodec: "aac",
			Resolution: "1920x1080",
			Framerate:  "60",
			Bitrate:    "6000k",
		},
	},
	"Instagram": {
		Name: "Instagram",
		Options: FFmpegOpts{
			VideoCodec: "libx264",
			AudioCodec: "aac",
			Resolution: "720x1280",
			Framerate:  "30",
			Bitrate:    "3500k",
		},
	},
	"Custom": {
		Name:    "Custom",
		Options: FFmpegOpts{},
	},
}
