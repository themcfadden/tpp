package maestro

import (
	"bytes"
	"testing"
	"time"

	"go.bug.st/serial"
)

// mockPort implements serial.Port, capturing written bytes in a buffer.
type mockPort struct {
	buf bytes.Buffer
	// closed tracks whether Close was called.
	closed bool
}

func (m *mockPort) Write(p []byte) (int, error)           { return m.buf.Write(p) }
func (m *mockPort) Read(p []byte) (int, error)            { return 0, nil }
func (m *mockPort) Close() error                          { m.closed = true; return nil }
func (m *mockPort) SetMode(*serial.Mode) error            { return nil }
func (m *mockPort) Drain() error                          { return nil }
func (m *mockPort) ResetInputBuffer() error               { return nil }
func (m *mockPort) ResetOutputBuffer() error              { return nil }
func (m *mockPort) SetDTR(bool) error                     { return nil }
func (m *mockPort) SetRTS(bool) error                     { return nil }
func (m *mockPort) GetModemStatusBits() (*serial.ModemStatusBits, error) {
	return &serial.ModemStatusBits{}, nil
}
func (m *mockPort) SetReadTimeout(time.Duration) error { return nil }
func (m *mockPort) Break(time.Duration) error          { return nil }

func newTestMaestro() (*Maestro, *mockPort) {
	mp := &mockPort{}
	return &Maestro{port: mp}, mp
}

// TestSetTargetCompactProtocol verifies the 4-byte Compact Protocol encoding.
// Command layout: [0x84, channel, low7, high7] where target = pulseUS * 4.
func TestSetTargetCompactProtocol(t *testing.T) {
	tests := []struct {
		name    string
		channel uint8
		pulseUS float64
		want    []byte
	}{
		{
			name:    "center 1500us ch0",
			channel: 0,
			pulseUS: 1500,
			// target = 1500 * 4 = 6000 = 0x1770
			// low7  = 6000 & 0x7F = 0x70 (112)
			// high7 = (6000 >> 7) & 0x7F = 0x2E (46)
			want: []byte{0x84, 0x00, 0x70, 0x2E},
		},
		{
			name:    "500us ch1 (minimum pulse)",
			channel: 1,
			pulseUS: 500,
			// target = 500 * 4 = 2000 = 0x7D0
			// low7  = 2000 & 0x7F = 0x50 (80)
			// high7 = (2000 >> 7) & 0x7F = 0x0F (15)
			want: []byte{0x84, 0x01, 0x50, 0x0F},
		},
		{
			name:    "2000us ch2",
			channel: 2,
			pulseUS: 2000,
			// target = 2000 * 4 = 8000 = 0x1F40
			// low7  = 8000 & 0x7F = 0x40 (64)
			// high7 = (8000 >> 7) & 0x7F = 0x3E (62)
			want: []byte{0x84, 0x02, 0x40, 0x3E},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, mp := newTestMaestro()
			if err := m.SetTarget(tt.channel, tt.pulseUS); err != nil {
				t.Fatalf("SetTarget: unexpected error: %v", err)
			}
			got := mp.buf.Bytes()
			if !bytes.Equal(got, tt.want) {
				t.Errorf("bytes = %#v, want %#v", got, tt.want)
			}
		})
	}
}

// TestSetTargetClamping ensures out-of-range pulse widths are clamped.
func TestSetTargetClamping(t *testing.T) {
	tests := []struct {
		name    string
		pulseUS float64
		wantUS  float64 // expected pulse after clamping
	}{
		{"below min clamped to 500", 100, 500},
		{"above max clamped to 2500", 3000, 2500},
		{"exactly min", 500, 500},
		{"exactly max", 2500, 2500},
		{"in range", 1500, 1500},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, mp := newTestMaestro()
			if err := m.SetTarget(0, tt.pulseUS); err != nil {
				t.Fatalf("SetTarget: unexpected error: %v", err)
			}
			// Decode the actual pulse from written bytes.
			b := mp.buf.Bytes()
			if len(b) != 4 {
				t.Fatalf("expected 4 bytes, got %d", len(b))
			}
			target := uint16(b[2]) | (uint16(b[3]) << 7)
			gotUS := float64(target) / 4.0
			if gotUS != tt.wantUS {
				t.Errorf("pulse = %.1f µs, want %.1f µs", gotUS, tt.wantUS)
			}
		})
	}
}

// TestSetServoPulseMapping verifies angle → pulse width conversion.
func TestSetServoPulseMapping(t *testing.T) {
	const minUS, maxUS = 1000.0, 2000.0

	tests := []struct {
		angle   float64
		wantUS  float64
	}{
		{-90, 1000}, // min angle → minUS
		{0, 1500},   // center → midpoint
		{90, 2000},  // max angle → maxUS
		{-45, 1250}, // quarter from min
		{45, 1750},  // quarter from max
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			m, mp := newTestMaestro()
			if err := m.SetServo(0, tt.angle, minUS, maxUS); err != nil {
				t.Fatalf("SetServo(%v): %v", tt.angle, err)
			}
			b := mp.buf.Bytes()
			target := uint16(b[2]) | (uint16(b[3]) << 7)
			gotUS := float64(target) / 4.0
			// Allow ±1µs rounding tolerance from float→int conversion.
			if diff := gotUS - tt.wantUS; diff > 1 || diff < -1 {
				t.Errorf("angle %.0f° → %.1f µs, want %.1f µs", tt.angle, gotUS, tt.wantUS)
			}
		})
	}
}

// TestSetServoAngleClamping ensures SetServo clamps angles outside [-90, 90].
func TestSetServoAngleClamping(t *testing.T) {
	const minUS, maxUS = 1000.0, 2000.0

	tests := []struct {
		angle  float64
		wantUS float64
	}{
		{-180, 1000}, // over-rotated → clamped to -90° → minUS
		{180, 2000},  // over-rotated → clamped to +90° → maxUS
	}

	for _, tt := range tests {
		m, mp := newTestMaestro()
		if err := m.SetServo(0, tt.angle, minUS, maxUS); err != nil {
			t.Fatalf("SetServo(%v): %v", tt.angle, err)
		}
		b := mp.buf.Bytes()
		target := uint16(b[2]) | (uint16(b[3]) << 7)
		gotUS := float64(target) / 4.0
		if diff := gotUS - tt.wantUS; diff > 1 || diff < -1 {
			t.Errorf("angle %.0f° → %.1f µs, want %.1f µs", tt.angle, gotUS, tt.wantUS)
		}
	}
}

// TestSetLaser verifies laser on/off pulse widths.
func TestSetLaser(t *testing.T) {
	tests := []struct {
		on     bool
		wantUS float64
	}{
		{false, 1000},
		{true, 2000},
	}

	for _, tt := range tests {
		m, mp := newTestMaestro()
		if err := m.SetLaser(2, tt.on); err != nil {
			t.Fatalf("SetLaser(%v): %v", tt.on, err)
		}
		b := mp.buf.Bytes()
		target := uint16(b[2]) | (uint16(b[3]) << 7)
		gotUS := float64(target) / 4.0
		if gotUS != tt.wantUS {
			t.Errorf("on=%v → %.1f µs, want %.1f µs", tt.on, gotUS, tt.wantUS)
		}
	}
}

// TestCenter verifies Center sends 1500 µs.
func TestCenter(t *testing.T) {
	m, mp := newTestMaestro()
	if err := m.Center(0); err != nil {
		t.Fatalf("Center: %v", err)
	}
	b := mp.buf.Bytes()
	target := uint16(b[2]) | (uint16(b[3]) << 7)
	gotUS := float64(target) / 4.0
	if gotUS != 1500 {
		t.Errorf("Center → %.1f µs, want 1500 µs", gotUS)
	}
}

// TestNilPortSafety verifies all methods are no-ops on a zero-value Maestro.
func TestNilPortSafety(t *testing.T) {
	m := &Maestro{} // port is nil
	if err := m.SetTarget(0, 1500); err != nil {
		t.Errorf("SetTarget on nil port: %v", err)
	}
	if err := m.SetServo(0, 0, 1000, 2000); err != nil {
		t.Errorf("SetServo on nil port: %v", err)
	}
	if err := m.SetLaser(2, true); err != nil {
		t.Errorf("SetLaser on nil port: %v", err)
	}
	if err := m.Center(1); err != nil {
		t.Errorf("Center on nil port: %v", err)
	}
	if err := m.Close(); err != nil {
		t.Errorf("Close on nil port: %v", err)
	}
}

// TestCloseReleasesPort verifies Close is forwarded to the underlying port.
func TestCloseReleasesPort(t *testing.T) {
	m, mp := newTestMaestro()
	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !mp.closed {
		t.Error("expected mock port to be closed")
	}
}
