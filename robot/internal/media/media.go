package media

import (
	"context"
	"fmt"
	"log"

	"github.com/mattmc/tppv4/robot/config"
	"github.com/pion/mediadevices"
	"github.com/pion/mediadevices/pkg/codec/opus"
	"github.com/pion/mediadevices/pkg/codec/vpx"
	"github.com/pion/mediadevices/pkg/prop"
	"github.com/pion/webrtc/v4"

	// Register V4L2 camera and ALSA microphone drivers.
	_ "github.com/pion/mediadevices/pkg/driver/camera"
	_ "github.com/pion/mediadevices/pkg/driver/microphone"
)

// Controller captures local camera + mic and can add them to a peer connection.
type Controller struct {
	cfg          config.Config
	stream       mediadevices.MediaStream
	codecSelector *mediadevices.CodecSelector
}

// New initialises media capture. Call once at startup.
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

	stream, err := mediadevices.GetUserMedia(mediadevices.MediaStreamConstraints{
		Video: func(c *mediadevices.MediaTrackConstraints) {
			c.DeviceID = prop.String(cfg.CameraDevice)
		},
		Audio: func(c *mediadevices.MediaTrackConstraints) {
			c.SampleRate   = prop.Int(48000)
			c.ChannelCount = prop.Int(1)
		},
		Codec: codecSelector,
	})
	if err != nil {
		return nil, fmt.Errorf("media: GetUserMedia: %w", err)
	}

	log.Printf("media: camera and microphone opened (%s)", cfg.CameraDevice)
	return &Controller{cfg: cfg, stream: stream, codecSelector: codecSelector}, nil
}

// AddTracksTo adds the robot's camera and mic tracks to the given peer connection.
// No-ops if the stream is nil (stub/dev mode).
func (c *Controller) AddTracksTo(pc *webrtc.PeerConnection) error {
	if c.stream == nil {
		return nil
	}
	for _, track := range c.stream.GetTracks() {
		track.OnEnded(func(err error) {
			if err != nil {
				log.Printf("media: track ended with error: %v", err)
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
// This is a stub — in production, decode Opus RTP packets and pipe to ALSA via gopxl/beep.
func (c *Controller) PlayPilotAudio(_ context.Context, track *webrtc.TrackRemote) {
	log.Printf("media: pilot audio track received (codec: %s) — playback not yet implemented", track.Codec().MimeType)
	// TODO Phase 9: decode Opus RTP → gopxl/beep → ALSA speaker output.
	for {
		if _, _, err := track.ReadRTP(); err != nil {
			log.Printf("media: pilot audio read error: %v", err)
			return
		}
	}
}
