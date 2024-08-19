package rpicamera

type params struct {
	LogLevel          string
	CameraID          int
	Width             int
	Height            int
	HFlip             bool
	VFlip             bool
	Brightness        float64
	Contrast          float64
	Saturation        float64
	Sharpness         float64
	Exposure          string
	AWB               string
	AWBGainRed        float64
	AWBGainBlue       float64
	Denoise           string
	Shutter           int
	Metering          string
	Gain              float64
	EV                float64
	ROI               string
	HDR               bool
	TuningFile        string
	Mode              string
	FPS               float64
	IDRPeriod         int
	Bitrate           int
	Profile           string
	Level             string
	AfMode            string
	AfRange           string
	AfSpeed           string
	LensPosition      float64
	AfWindow          string
	FlickerPeriod     int
	TextOverlayEnable bool
	TextOverlay       string
}
