package motor

import (
	"context"
	"fmt"
	"math"

	"go.bug.st/serial"
)

// MotorController abstracts drive motor control.
// All speed values are in the range [-1.0, 1.0].
type MotorController interface {
	// SetSpeed sets left and right motor speeds independently.
	// Positive values move forward; negative values move backward.
	SetSpeed(ctx context.Context, left, right float64) error
	Stop(ctx context.Context) error
	Close() error
}

// DifferentialDrive converts a normalized x/y joystick input to left/right speeds.
// x: lateral turn (-1=left, +1=right), y: forward/backward (-1=back, +1=forward).
func DifferentialDrive(x, y float64) (left, right float64) {
	left = math.Max(-1, math.Min(1, y-x))
	right = math.Max(-1, math.Min(1, y+x))
	return
}

// NoOpMotorController satisfies MotorController without any hardware — safe for dev/testing.
type NoOpMotorController struct{}

func (n *NoOpMotorController) SetSpeed(_ context.Context, left, right float64) error {
	return nil
}
func (n *NoOpMotorController) Stop(_ context.Context) error { return nil }
func (n *NoOpMotorController) Close() error                 { return nil }

// UARTMotorController sends simple ASCII speed commands over a serial UART port.
// Protocol: "L<value> R<value>\n" where value is -100 to 100 (percent).
// Replace this with the specific framing required by your motor driver.
type UARTMotorController struct {
	port serial.Port
}

// NewUARTMotorController opens the serial port for motor control.
func NewUARTMotorController(devicePath string, baud int) (*UARTMotorController, error) {
	mode := &serial.Mode{BaudRate: baud}
	port, err := serial.Open(devicePath, mode)
	if err != nil {
		return nil, fmt.Errorf("motor: open %s: %w", devicePath, err)
	}
	return &UARTMotorController{port: port}, nil
}

func (u *UARTMotorController) SetSpeed(_ context.Context, left, right float64) error {
	l := int(math.Round(left * 100))
	r := int(math.Round(right * 100))
	cmd := fmt.Sprintf("L%d R%d\n", l, r)
	_, err := u.port.Write([]byte(cmd))
	return err
}

func (u *UARTMotorController) Stop(ctx context.Context) error {
	return u.SetSpeed(ctx, 0, 0)
}

func (u *UARTMotorController) Close() error {
	return u.port.Close()
}
