package filter

import (
	"math"
	"sync"
	"time"
)

// OneEuroFilter implements the adaptive low-pass filter by Géry Casiez, Nicolas Roussel, and Daniel Vogel (2012).
// When movement speed is low, the cutoff drops towards minCutoff to aggressively eliminate jitter and noise.
// When movement speed is high, cutoff increases dynamically with velocity to eliminate lag.
type OneEuroFilter struct {
	minCutoff float64 // Minimum cutoff frequency in Hz (> 0)
	beta      float64 // Speed coefficient (> 0)
	dCutoff   float64 // Cutoff frequency for derivative in Hz (> 0)
	xPrev     float64
	dxPrev    float64
	tPrev     time.Time
	hasPrev   bool
}

// NewOneEuroFilter constructs a 1-Euro filter with given parameters.
func NewOneEuroFilter(minCutoff, beta, dCutoff float64) *OneEuroFilter {
	if minCutoff <= 0 {
		minCutoff = 1.0
	}
	if dCutoff <= 0 {
		dCutoff = 1.0
	}
	return &OneEuroFilter{
		minCutoff: minCutoff,
		beta:      beta,
		dCutoff:   dCutoff,
	}
}

// SetParams updates filter tuning parameters dynamically.
func (f *OneEuroFilter) SetParams(minCutoff, beta, dCutoff float64) {
	if minCutoff > 0 {
		f.minCutoff = minCutoff
	}
	if beta >= 0 {
		f.beta = beta
	}
	if dCutoff > 0 {
		f.dCutoff = dCutoff
	}
}

// Reset clears filter history.
func (f *OneEuroFilter) Reset() {
	f.hasPrev = false
	f.xPrev = 0
	f.dxPrev = 0
	f.tPrev = time.Time{}
}

// Filter processes a single scalar sample at time t.
func (f *OneEuroFilter) Filter(x float64, t time.Time) float64 {
	if !f.hasPrev {
		f.xPrev = x
		f.dxPrev = 0
		f.tPrev = t
		f.hasPrev = true
		return x
	}

	dt := t.Sub(f.tPrev).Seconds()
	f.tPrev = t

	// Guard against clock jitter, duplicate timestamps or pause gaps
	if dt <= 1e-5 || dt > 1.0 {
		dt = 1.0 / 60.0
	}

	// 1. Calculate discrete derivative
	dx := (x - f.xPrev) / dt

	// 2. Filter derivative with dCutoff
	alphaD := smoothingFactor(dt, f.dCutoff)
	edx := alphaD*dx + (1.0-alphaD)*f.dxPrev
	f.dxPrev = edx

	// 3. Compute adaptive cutoff frequency
	cutoff := f.minCutoff + f.beta*math.Abs(edx)

	// 4. Filter signal with adaptive cutoff
	alpha := smoothingFactor(dt, cutoff)
	hatX := alpha*x + (1.0-alpha)*f.xPrev
	f.xPrev = hatX

	return hatX
}

func smoothingFactor(dt, cutoff float64) float64 {
	r := 2.0 * math.Pi * cutoff * dt
	return r / (r + 1.0)
}

// Vector3OneEuroFilter filters a 3D vector (Pitch, Yaw, Roll) synchronously.
type Vector3OneEuroFilter struct {
	mu sync.Mutex
	fx *OneEuroFilter
	fy *OneEuroFilter
	fz *OneEuroFilter
}

// NewVector3OneEuroFilter creates a 3-axis 1-Euro filter.
func NewVector3OneEuroFilter(minCutoff, beta, dCutoff float64) *Vector3OneEuroFilter {
	return &Vector3OneEuroFilter{
		fx: NewOneEuroFilter(minCutoff, beta, dCutoff),
		fy: NewOneEuroFilter(minCutoff, beta, dCutoff),
		fz: NewOneEuroFilter(minCutoff, beta, dCutoff),
	}
}

// SetParams updates parameters across all 3 axes.
func (vf *Vector3OneEuroFilter) SetParams(minCutoff, beta, dCutoff float64) {
	vf.mu.Lock()
	defer vf.mu.Unlock()
	vf.fx.SetParams(minCutoff, beta, dCutoff)
	vf.fy.SetParams(minCutoff, beta, dCutoff)
	vf.fz.SetParams(minCutoff, beta, dCutoff)
}

// Reset clears state across all 3 axes.
func (vf *Vector3OneEuroFilter) Reset() {
	vf.mu.Lock()
	defer vf.mu.Unlock()
	vf.fx.Reset()
	vf.fy.Reset()
	vf.fz.Reset()
}

// Filter filters a 3D vector at time t.
func (vf *Vector3OneEuroFilter) Filter(x, y, z float64, t time.Time) (float64, float64, float64) {
	vf.mu.Lock()
	defer vf.mu.Unlock()
	return vf.fx.Filter(x, t), vf.fy.Filter(y, t), vf.fz.Filter(z, t)
}

// ApplySmoothDeadband suppresses micro-tremor using a C1-continuous Hermite cubic smoothstep.
// Below deadband, output is exactly 0.
// Between deadband and 2*deadband, it smoothly transitions to 1.
// Above 2*deadband, the original signal passes with 100% fidelity and zero attenuation.
func ApplySmoothDeadband(val, totalSpeed, deadband float64) float64 {
	if deadband <= 1e-6 || totalSpeed <= 1e-6 {
		return val
	}
	if totalSpeed <= deadband {
		return 0.0
	}
	upper := 2.0 * deadband
	if totalSpeed >= upper {
		return val
	}
	// Cubic Hermite smoothstep in [0, 1]
	s := (totalSpeed - deadband) / (upper - deadband)
	factor := s * s * (3.0 - 2.0*s)
	return val * factor
}
