package config

import (
	"os"
	"strconv"

	"gopkg.in/yaml.v3"
)

// Config holds all runtime configuration for the robot.
type Config struct {
	HTTPPort    int    `yaml:"http_port"`
	CameraDevice string `yaml:"camera_device"`
	VideoBitrate int    `yaml:"video_bitrate"`

	MaestroPort string `yaml:"maestro_port"`

	MotorDriver string `yaml:"motor_driver"` // "uart" | "noop"
	MotorPort   string `yaml:"motor_port"`
	MotorBaud   int    `yaml:"motor_baud"`

	// Maestro channel assignments
	MaestroPanChannel   int `yaml:"maestro_pan_channel"`
	MaestroTiltChannel  int `yaml:"maestro_tilt_channel"`
	MaestroLaserChannel int `yaml:"maestro_laser_channel"`

	// Servo calibration (microseconds)
	PanMinUS  float64 `yaml:"pan_min_us"`
	PanMaxUS  float64 `yaml:"pan_max_us"`
	TiltMinUS float64 `yaml:"tilt_min_us"`
	TiltMaxUS float64 `yaml:"tilt_max_us"`
}

// Defaults returns a Config populated with sensible defaults.
func Defaults() Config {
	return Config{
		HTTPPort:            8080,
		CameraDevice:        "/dev/video0",
		VideoBitrate:        1_000_000,
		MaestroPort:         "/dev/ttyACM0",
		MotorDriver:         "noop",
		MotorPort:           "/dev/ttyUSB0",
		MotorBaud:           115200,
		MaestroPanChannel:   0,
		MaestroTiltChannel:  1,
		MaestroLaserChannel: 2,
		PanMinUS:            992,
		PanMaxUS:            2000,
		TiltMinUS:           1000,
		TiltMaxUS:           1800,
	}
}

// Load reads config from a YAML file (if present) then overrides with env vars.
func Load(path string) (Config, error) {
	cfg := Defaults()

	if data, err := os.ReadFile(path); err == nil {
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return cfg, err
		}
	}

	overrideString(&cfg.MaestroPort, "MAESTRO_PORT")
	overrideString(&cfg.MotorDriver, "MOTOR_DRIVER")
	overrideString(&cfg.MotorPort, "MOTOR_PORT")
	overrideString(&cfg.CameraDevice, "CAMERA_DEVICE")
	overrideInt(&cfg.HTTPPort, "HTTP_PORT")
	overrideInt(&cfg.VideoBitrate, "VIDEO_BITRATE")
	overrideInt(&cfg.MotorBaud, "MOTOR_BAUD")

	return cfg, nil
}

func overrideString(dst *string, env string) {
	if v := os.Getenv(env); v != "" {
		*dst = v
	}
}

func overrideInt(dst *int, env string) {
	if v := os.Getenv(env); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			*dst = n
		}
	}
}
