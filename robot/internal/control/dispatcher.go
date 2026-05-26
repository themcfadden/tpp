package control

import (
	"context"
	"encoding/json"
	"log"
	"math"

	"github.com/mattmc/tppv4/robot/config"
	"github.com/mattmc/tppv4/robot/internal/hardware/maestro"
	"github.com/mattmc/tppv4/robot/internal/hardware/motor"
	"github.com/pion/webrtc/v4"
)

// cmd is the generic envelope for all pilot→robot data channel messages.
type cmd struct {
	Type string  `json:"type"`
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
	Pan  float64 `json:"pan"`
	Tilt float64 `json:"tilt"`
	On   bool    `json:"on"`
}

// statusMsg is sent back to the pilot on the reliable status channel.
type statusMsg struct {
	Type  string `json:"type"`
	Laser bool   `json:"laser"`
}

// Dispatcher routes WebRTC data channel messages to hardware controllers.
type Dispatcher struct {
	cfg      config.Config
	maestro  *maestro.Maestro
	motors   motor.MotorController
	statusDC *webrtc.DataChannel

	// current servo state (degrees)
	panDeg  float64
	tiltDeg float64
	laserPanDeg float64
	laserTiltDeg float64
	laserOn bool
}

// New creates a Dispatcher. statusDC may be nil during initial setup (set later via SetStatusChannel).
func New(cfg config.Config, m *maestro.Maestro, mc motor.MotorController) *Dispatcher {
	return &Dispatcher{
		cfg:     cfg,
		maestro: m,
		motors:  mc,
		laserTiltDeg: 45,
	}
}

// SetStatusChannel registers the reliable data channel used to send state back to the pilot.
func (d *Dispatcher) SetStatusChannel(dc *webrtc.DataChannel) {
	d.statusDC = dc
}

// HandleDriveChannel wires an unreliable data channel to drive commands.
func (d *Dispatcher) HandleDriveChannel(dc *webrtc.DataChannel) {
	dc.OnOpen(func() {
		log.Printf("control: drive channel open")
	})
	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		var c cmd
		if err := json.Unmarshal(msg.Data, &c); err != nil {
			log.Printf("control: bad drive message: %v", err)
			return
		}
		ctx := context.Background()
		switch c.Type {
		case "drive":
			left, right := motor.DifferentialDrive(c.X, c.Y)
			log.Printf("control: drive x=%.2f y=%.2f → L=%.2f R=%.2f", c.X, c.Y, left, right)
			if err := d.motors.SetSpeed(ctx, left, right); err != nil {
				log.Printf("control: motor SetSpeed: %v", err)
			}
		case "stop":
			log.Printf("control: stop")
			if err := d.motors.Stop(ctx); err != nil {
				log.Printf("control: motor Stop: %v", err)
			}
		}
	})
}

// HandleServoChannel wires an unreliable data channel to servo commands.
func (d *Dispatcher) HandleServoChannel(dc *webrtc.DataChannel) {
	dc.OnOpen(func() {
		log.Printf("control: servo channel open")
	})
	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		var c cmd
		if err := json.Unmarshal(msg.Data, &c); err != nil {
			log.Printf("control: bad servo message: %v", err)
			return
		}
		switch c.Type {
		case "servo":
			d.panDeg = clampAngle(c.Pan, -90, 90)
			d.tiltDeg = clampAngle(c.Tilt, -45, 45)
		case "servo_delta":
			d.panDeg = clampAngle(d.panDeg+c.Pan, -90, 90)
			d.tiltDeg = clampAngle(d.tiltDeg+c.Tilt, -45, 45)
		case "center_head":
			d.panDeg = 0
			d.tiltDeg = 0
		default:
			return
		}
		// log.Printf("control: servo pan=%.1f° tilt=%.1f°", d.panDeg, d.tiltDeg)
		if err := d.maestro.SetServo(
			uint8(d.cfg.MaestroPanChannel), d.panDeg,
			d.cfg.PanMinUS, d.cfg.PanMaxUS,
		); err != nil {
			log.Printf("control: maestro pan: %v", err)
		}
		if err := d.maestro.SetServo(
			uint8(d.cfg.MaestroTiltChannel), d.tiltDeg,
			d.cfg.TiltMinUS, d.cfg.TiltMaxUS,
		); err != nil {
			log.Printf("control: maestro tilt: %v", err)
		}
	})
}

// HandleLaserChannel wires a reliable data channel to laser commands.
func (d *Dispatcher) HandleLaserChannel(dc *webrtc.DataChannel) {
	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		var c cmd
		if err := json.Unmarshal(msg.Data, &c); err != nil {
			log.Printf("control: bad laser message: %v", err)
			return
		}
		switch c.Type {
		case "laser_aim":
			d.laserPanDeg = clampAngle(c.Pan, -90, 90)
			d.laserTiltDeg = clampAngle(c.Tilt, -45, 45)
			if err := d.maestro.SetServo(
				uint8(d.cfg.MaestroLaserPanChannel), d.laserPanDeg,
				d.cfg.PanMinUS, d.cfg.PanMaxUS,
			); err != nil {
				log.Printf("control: maestro laser pan: %v", err)
			}
			if err := d.maestro.SetServo(
				uint8(d.cfg.MaestroLaserTiltChannel), d.laserTiltDeg,
				d.cfg.TiltMinUS, d.cfg.TiltMaxUS,
			); err != nil {
				log.Printf("control: maestro laser tilt: %v", err)
			}
		case "laser":
			d.laserOn = c.On
			if err := d.maestro.SetLaser(uint8(d.cfg.MaestroLaserChannel), d.laserOn); err != nil {
				log.Printf("control: maestro laser: %v", err)
			}
			d.sendStatus()
		}
	})
}

// EmergencyStop immediately halts the drive motors. Called on connection loss.
func (d *Dispatcher) EmergencyStop() error {
	return d.motors.Stop(context.Background())
}

func (d *Dispatcher) sendStatus() {
	if d.statusDC == nil || d.statusDC.ReadyState() != webrtc.DataChannelStateOpen {
		return
	}
	data, _ := json.Marshal(statusMsg{Type: "status", Laser: d.laserOn})
	_ = d.statusDC.SendText(string(data))
}

func clampAngle(v, min, max float64) float64 {
	return math.Max(min, math.Min(max, v))
}
