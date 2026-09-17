package main

import (
	"math"
	"sync"
	"time"
)

// MadgwickAHRS implements the exact IMU orientation filter used by PadTest.exe and BetterJoy.
// It estimates controller orientation as a unit quaternion [Q0, Q1, Q2, Q3]
// directly from calibrated gyroscope rates (RotX, RotY, RotZ in °/s)
// and accelerometer readings (AccX, AccY, AccZ in g).
type MadgwickAHRS struct {
	mu       sync.Mutex
	Q0       float32
	Q1       float32
	Q2       float32
	Q3       float32
	Beta     float32
	lastTime time.Time
}

// NewMadgwickAHRS creates a filter initialized to identity orientation [1, 0, 0, 0].
// Pass beta=0 for pure gyro integration without phantom accelerometer drift/spinning.
func NewMadgwickAHRS(beta float32) *MadgwickAHRS {
	if beta < 0 {
		beta = 0.0
	}
	return &MadgwickAHRS{
		Q0:   1.0,
		Q1:   0.0,
		Q2:   0.0,
		Q3:   0.0,
		Beta: beta,
	}
}

// Reset resets the filter to identity orientation.
func (m *MadgwickAHRS) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Q0 = 1.0
	m.Q1 = 0.0
	m.Q2 = 0.0
	m.Q3 = 0.0
	m.lastTime = time.Time{}
}

// Update runs one integration step matching PadTest.exe (0x140832a67 - 0x140833a00) exactly.
// Input arguments are the canonical Cemuhook DSU fields:
//   rotX (Pitch in °/s, nose up = +, nose down = -)
//   rotY (Yaw in °/s, nose left = +, nose right = -)
//   rotZ (Roll in °/s, bank right = +, bank left = -)
//   accX (Lateral acceleration in g, left = +, right = -)
//   accY (Vertical acceleration in g, up = +, down = -)
//   accZ (Longitudinal acceleration in g, back = +, forward = -)
func (m *MadgwickAHRS) Update(rotX, rotY, rotZ, accX, accY, accZ float32, now time.Time) (float32, float32, float32, float32) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Fixed sensor sample period: iOS devicemotion fires at exactly 60 Hz.
	// Never use network arrival time delta (now.Sub(lastTime)) because WiFi jitter
	// causes bursts with 100ms+ deltas that multiply rotation rates by up to 10x,
	// causing violent spasms/instability during fast movements.
	const dt float32 = 1.0 / 60.0
	m.lastTime = now

	const deg2rad = float32(math.Pi / 180.0)

	// PadTest.exe mapping at 0x140832a67 - 0x140832a95:
	// gx = -RotX, gy = -RotY, gz = -RotZ
	// ax = +AccX, ay = -AccY, az = -AccZ
	gx := -rotX * deg2rad
	gy := -rotY * deg2rad
	gz := -rotZ * deg2rad
	ax := accX
	ay := -accY
	az := -accZ

	q1, q2, q3, q4 := m.Q0, m.Q1, m.Q2, m.Q3

	beta := m.Beta
	if beta > 0 {
		norm := float32(math.Sqrt(float64(ax*ax + ay*ay + az*az)))
		if norm > 1e-4 {
			// Adaptive gain (§6): trust accelerometer only when close to 1g
			// During fast motion, centrifugal acceleration distorts gravity -> confidence = 0 (zero kick)
			deviation := float32(math.Abs(float64(norm - 1.0)))
			confidence := 1.0 - deviation/0.3
			if confidence < 0 {
				confidence = 0
			} else if confidence > 1 {
				confidence = 1
			}
			effectiveBeta := beta * confidence

			if effectiveBeta > 1e-5 {
				recip := 1.0 / norm
				ax *= recip
				ay *= recip
				az *= recip

				_2q1 := 2.0 * q1
				_2q2 := 2.0 * q2
				_2q3 := 2.0 * q3
				_2q4 := 2.0 * q4
				_4q1 := 4.0 * q1
				_4q2 := 4.0 * q2
				_4q3 := 4.0 * q3
				_8q2 := 8.0 * q2
				_8q3 := 8.0 * q3
				q1q1 := q1 * q1
				q2q2 := q2 * q2
				q3q3 := q3 * q3
				q4q4 := q4 * q4

				s1 := _4q1*q3q3 + _2q3*ax + _4q1*q2q2 - _2q2*ay
				s2 := _4q2*q4q4 - _2q4*ax + 4.0*q1q1*q2 - _2q1*ay - _4q2 + _8q2*q2q2 + _8q2*q3q3 + _4q2*az
				s3 := 4.0*q1q1*q3 + _2q1*ax + _4q3*q4q4 - _2q4*ay - _4q3 + _8q3*q2q2 + _8q3*q3q3 + _4q3*az
				s4 := 4.0*q2q2*q4 - _2q2*ax + 4.0*q3q3*q4 - _2q3*ay

				snorm := float32(math.Sqrt(float64(s1*s1 + s2*s2 + s3*s3 + s4*s4)))
				if snorm > 1e-4 {
					srecip := 1.0 / snorm
					gx -= 2.0 * effectiveBeta * s1 * srecip
					gy -= 2.0 * effectiveBeta * s2 * srecip
					gz -= 2.0 * effectiveBeta * s3 * srecip
				}
			}
		}
	}

	// Exact closed-form Lie algebra quaternion integration:
	// dq = [cos(|w|*dt/2), (w/|w|) * sin(|w|*dt/2)]
	// Unlike first-order Euler integration (q + 0.5*q*w*dt), this does not diverge
	// or overshoot at high angular velocities.
	omegaMag := float32(math.Sqrt(float64(gx*gx + gy*gy + gz*gz)))
	if omegaMag > 1e-6 {
		halfAngle := omegaMag * dt * 0.5
		sinHalf := float32(math.Sin(float64(halfAngle))) / omegaMag
		cosHalf := float32(math.Cos(float64(halfAngle)))

		dq0 := cosHalf
		dq1 := gx * sinHalf
		dq2 := gy * sinHalf
		dq3 := gz * sinHalf

		// Hamilton product: q_next = q * dq
		n0 := q1*dq0 - q2*dq1 - q3*dq2 - q4*dq3
		n1 := q1*dq1 + q2*dq0 + q3*dq3 - q4*dq2
		n2 := q1*dq2 - q2*dq3 + q3*dq0 + q4*dq1
		n3 := q1*dq3 + q2*dq2 - q3*dq1 + q4*dq0

		qnorm := float32(math.Sqrt(float64(n0*n0 + n1*n1 + n2*n2 + n3*n3)))
		if qnorm > 1e-4 {
			qrecip := 1.0 / qnorm
			m.Q0 = n0 * qrecip
			m.Q1 = n1 * qrecip
			m.Q2 = n2 * qrecip
			m.Q3 = n3 * qrecip
		}
	}
	// When stationary (omegaMag <= 1e-6), keep the exact current quaternion without decay.
	// This ensures 1:1 attitude hold and zero offset when returning to neutral.

	return m.Q0, m.Q1, m.Q2, m.Q3
}

// GetEulerAngles returns pitch, roll, yaw in degrees matching PadTest.
func (m *MadgwickAHRS) GetEulerAngles() (pitch, roll, yaw float64) {
	m.mu.Lock()
	q0, q1, q2, q3 := float64(m.Q0), float64(m.Q1), float64(m.Q2), float64(m.Q3)
	m.mu.Unlock()

	sq1 := q1 * q1
	sq2 := q2 * q2
	sq3 := q3 * q3

	const rad2deg = 180.0 / math.Pi
	pitch = math.Asin(math.Max(-1.0, math.Min(1.0, 2.0*(q0*q2-q3*q1)))) * rad2deg
	yaw = math.Atan2(2.0*(q0*q3+q1*q2), 1.0-2.0*(sq2+sq3)) * rad2deg
	roll = math.Atan2(2.0*(q0*q1+q2*q3), 1.0-2.0*(sq1+sq2)) * rad2deg
	return pitch, roll, yaw
}
