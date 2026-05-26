package media

// VP8 software encoding via libvpx + ALSA audio capture.
//
// Required system packages (Ubuntu/Debian — works on both x86 and Raspberry Pi OS):
//
//	sudo apt install libvpx-dev libasound2-dev
//
// TODO(rpi-optimisation): When development moves to testing on real RPi hardware,
// swap vpx.NewVP8Params() for github.com/pion/mediadevices/pkg/codec/mmal to use
// the Broadcom VideoCore hardware H.264 encoder instead of software VP8.
// MMAL requires: sudo apt install libraspberrypi-dev  (RPi OS only, won't build on x86)

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/mattmc/tppv4/robot/config"
	"github.com/pion/mediadevices"
	"github.com/pion/mediadevices/pkg/codec/opus"
	"github.com/pion/mediadevices/pkg/codec/vpx"
	"github.com/pion/mediadevices/pkg/frame"
	"github.com/pion/mediadevices/pkg/prop"
	"github.com/pion/webrtc/v4"

	// Register V4L2 camera and ALSA microphone drivers.
	_ "github.com/pion/mediadevices/pkg/driver/camera"
	_ "github.com/pion/mediadevices/pkg/driver/microphone"
)

// Controller captures local camera + mic and can add them to a peer connection.
type Controller struct {
	cfg           config.Config
	codecSelector *mediadevices.CodecSelector
	mu            sync.Mutex
	stream        mediadevices.MediaStream
	captureMode   videoCaptureMode
}

type videoCaptureMode int

const (
	captureModeMJPEG videoCaptureMode = iota
	captureModeDefault
)

// New initialises media codec parameters. Call once at startup.
// Camera and mic are opened per-connection in AddTracksTo.
func New(cfg config.Config) (*Controller, error) {
	opusParams, err := opus.NewParams()
	if err != nil {
		return nil, fmt.Errorf("media: opus params: %w", err)
	}
	vpxParams, err := vpx.NewVP8Params()
	if err != nil {
		return nil, fmt.Errorf("media: vpx params: %w", err)
	}
	vpxParams.BitRate = cfg.VideoBitrate

	codecSelector := mediadevices.NewCodecSelector(
		mediadevices.WithVideoEncoders(&vpxParams),
		mediadevices.WithAudioEncoders(&opusParams),
	)

	log.Printf("media: codecs initialised (camera: %s)", cfg.CameraDevice)
	return &Controller{
		cfg:           cfg,
		codecSelector: codecSelector,
		captureMode:   captureModeMJPEG,
	}, nil
}

// AddTracksTo opens a fresh camera+mic stream and adds the tracks to the given peer
// connection. Any previously open stream is closed first so the driver is released
// before re-opening (mediadevices allows only one open at a time).
// onTrackEnd is called (once) if any track ends with an error — callers use this
// to close the signaling WebSocket and trigger a pilot reconnect.
func (c *Controller) AddTracksTo(pc *webrtc.PeerConnection, onTrackEnd func()) error {
	if c.codecSelector == nil {
		return nil // stub/dev mode
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	// Close the previous stream so the driver is fully released before we reopen.
	if c.stream != nil {
		for _, t := range c.stream.GetTracks() {
			t.Close()
		}
		c.stream = nil
		// Brief pause so the driver state machine can transition back to Closed.
		time.Sleep(100 * time.Millisecond)
	}

	mode := c.captureMode

	stream, err := mediadevices.GetUserMedia(mediadevices.MediaStreamConstraints{
		Video: func(mc *mediadevices.MediaTrackConstraints) {
			mc.DeviceID = prop.String(c.cfg.CameraDevice)
			mc.Width = prop.Int(640)
			mc.Height = prop.Int(480)
			mc.FrameRate = prop.Float(30)
			if mode == captureModeMJPEG {
				mc.FrameFormat = prop.FrameFormatExact(frame.FormatMJPEG)
			}
		},
		Audio: func(mc *mediadevices.MediaTrackConstraints) {
			mc.SampleRate   = prop.Int(48000)
			mc.ChannelCount = prop.Int(1)
		},
		Codec: c.codecSelector,
	})
	if err != nil {
		if mode == captureModeMJPEG {
			log.Printf("media: MJPEG capture failed (%v); retrying with default frame format", err)
			c.captureMode = captureModeDefault
			stream, err = mediadevices.GetUserMedia(mediadevices.MediaStreamConstraints{
				Video: func(mc *mediadevices.MediaTrackConstraints) {
					mc.DeviceID = prop.String(c.cfg.CameraDevice)
					mc.Width = prop.Int(640)
					mc.Height = prop.Int(480)
					mc.FrameRate = prop.Float(30)
				},
				Audio: func(mc *mediadevices.MediaTrackConstraints) {
					mc.SampleRate = prop.Int(48000)
					mc.ChannelCount = prop.Int(1)
				},
				Codec: c.codecSelector,
			})
			if err != nil {
				return fmt.Errorf("media: GetUserMedia fallback: %w", err)
			}
			mode = captureModeDefault
		} else {
			return fmt.Errorf("media: GetUserMedia: %w", err)
		}
	}
	c.stream = stream

	var onceEnd sync.Once
	for _, track := range stream.GetTracks() {
		track.OnEnded(func(err error) {
			if err != nil {
				errText := err.Error()
				log.Printf("media: track ended with error: %v", err)

				c.mu.Lock()
				switch {
				case strings.Contains(errText, "frame length") && mode != captureModeMJPEG:
					c.captureMode = captureModeMJPEG
					log.Printf("media: switching capture mode to MJPEG after raw-frame error")
				case strings.Contains(errText, "invalid JPEG") && mode == captureModeMJPEG:
					c.captureMode = captureModeDefault
					log.Printf("media: switching capture mode to default after JPEG decode error")
				}
				c.mu.Unlock()

				onceEnd.Do(onTrackEnd)
			}
		})
		if _, err := pc.AddTransceiverFromTrack(track,
			webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionSendrecv},
		); err != nil {
			return fmt.Errorf("media: add transceiver: %w", err)
		}
	}
	return nil
}

// NewStub returns an empty Controller safe to use on dev machines without camera hardware.
func NewStub() *Controller { return &Controller{} }

// PlayPilotAudio receives the pilot's audio track and plays it through the local speaker.
// TODO Phase 9: decode Opus RTP → speaker output (e.g. via gopxl/beep → ALSA).
func (c *Controller) PlayPilotAudio(_ context.Context, track *webrtc.TrackRemote) {
	log.Printf("media: pilot audio track received (codec: %s) — playback not yet implemented", track.Codec().MimeType)
	for {
		if _, _, err := track.ReadRTP(); err != nil {
			log.Printf("media: pilot audio read error: %v", err)
			return
		}
	}
}
