package maestro

import (
	"fmt"
	"log"
	"math"

	"go.bug.st/serial"
)

// Maestro controls a Pololu Maestro USB servo controller via its Compact Protocol.
// https://www.pololu.com/docs/0J40/5.e
type Maestro struct {
	port serial.Port
}

// New opens the serial port to the Maestro at the given device path.
func New(devicePath string) (*Maestro, error) {
	mode := &serial.Mode{BaudRate: 9600}
	port, err := serial.Open(devicePath, mode)
	if err != nil {
		return nil, fmt.Errorf("maestro: open %s: %w", devicePath, err)
	}
	return &Maestro{port: port}, nil
}

// SetTarget sets a channel to a target pulse width in microseconds.
// The Maestro Compact Protocol encodes target as quarter-microseconds (×4).
// target is clamped to valid servo range (500–2500 µs).
// Returns nil without error if the port is not open (safe for dev/stub use).
func (m *Maestro) SetTarget(channel uint8, pulseUS float64) error {
	if m.port == nil {
		return nil
	}
	pulseUS = math.Max(500, math.Min(2500, pulseUS))
	target := uint16(pulseUS * 4) // convert µs → quarter-µs

	// Compact Protocol: 0x84, channel, low 7 bits, high 7 bits
	cmd := []byte{
		0x84,
		channel,
		byte(target & 0x7F),
		byte((target >> 7) & 0x7F),
	}
	_, err := m.port.Write(cmd)
	if err != nil {
		return fmt.Errorf("maestro: set target ch%d: %w", channel, err)
	}
	log.Printf("maestro: ch%d → %.0f µs (target=%d)", channel, pulseUS, target)
	return nil
}

// SetServo sets a channel to an angle in degrees, mapped to the provided µs range.
func (m *Maestro) SetServo(channel uint8, angleDeg, minUS, maxUS float64) error {
	// Clamp angle to [-90, 90]
	angleDeg = math.Max(-90, math.Min(90, angleDeg))
	// Map -90..90 → minUS..maxUS
	t := (angleDeg + 90) / 180.0
	pulseUS := minUS + t*(maxUS-minUS)
	return m.SetTarget(channel, pulseUS)
}

// SetLaser turns the laser on or off via a servo channel.
// Uses full-low (1000 µs) for OFF and full-high (2000 µs) for ON.
func (m *Maestro) SetLaser(channel uint8, on bool) error {
	pulseUS := 1000.0
	if on {
		pulseUS = 2000.0
	}
	return m.SetTarget(channel, pulseUS)
}

// Center moves a servo channel to its center position (1500 µs).
func (m *Maestro) Center(channel uint8) error {
	return m.SetTarget(channel, 1500)
}

// Close releases the serial port. Safe to call on a nil-port Maestro.
func (m *Maestro) Close() error {
	if m.port == nil {
		return nil
	}
	return m.port.Close()
}
