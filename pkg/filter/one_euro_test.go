package filter

import (
	"math"
	"testing"
	"time"
)

func TestOneEuroFilter_SuppressesNoiseWhenStationary(t *testing.T) {
	// minCutoff = 1.0 Hz, beta = 0.01, dCutoff = 1.0 Hz
	f := NewOneEuroFilter(1.0, 0.01, 1.0)
	now := time.Now()

	// Feed stationary signal with 60 Hz high-frequency electrical/hand jitter (amplitude +/- 0.5)
	var filteredVals []float64
	for i := 0; i < 60; i++ {
		noise := 0.5 * math.Sin(float64(i)*1.2)
		val := f.Filter(noise, now.Add(time.Duration(i)*16*time.Millisecond))
		if i > 20 {
			filteredVals = append(filteredVals, val)
		}
	}

	// Filtered signal amplitude should be dramatically reduced compared to raw 0.5 amplitude
	maxFiltered := 0.0
	for _, v := range filteredVals {
		if math.Abs(v) > maxFiltered {
			maxFiltered = math.Abs(v)
		}
	}

	if maxFiltered > 0.25 {
		t.Fatalf("expected filtered noise amplitude <= 0.25, got %f", maxFiltered)
	}
}

func TestOneEuroFilter_FollowsFastFlickWithZeroLag(t *testing.T) {
	// Fast flick motion (velocity 150 deg/s)
	f := NewOneEuroFilter(1.0, 0.02, 1.0)
	now := time.Now()

	// Initial rest
	for i := 0; i < 10; i++ {
		f.Filter(0.0, now.Add(time.Duration(i)*16*time.Millisecond))
	}

	// Rapid flick at step 10 to 100.0 deg/s
	tFlick := now.Add(11 * 16 * time.Millisecond)
	outFlick := f.Filter(100.0, tFlick)

	// With high beta, filter opens wide immediately.
	// OutFlick should achieve high response in first frame (> 60.0).
	if outFlick < 60.0 {
		t.Fatalf("expected fast response on rapid flick (> 60.0), got %f", outFlick)
	}
}

func TestApplySmoothDeadband(t *testing.T) {
	deadband := 0.10

	// 1. Below deadband -> exactly 0
	val1 := ApplySmoothDeadband(0.05, 0.05, deadband)
	if val1 != 0.0 {
		t.Fatalf("expected 0.0 below deadband, got %f", val1)
	}

	// 2. Above 2*deadband -> 100% untouched
	val2 := ApplySmoothDeadband(25.0, 25.0, deadband)
	if math.Abs(val2-25.0) > 1e-6 {
		t.Fatalf("expected 25.0 above 2*deadband, got %f", val2)
	}

	// 3. In transition zone (e.g. at 1.5 * deadband) -> smooth Hermite interpolation
	valMid := ApplySmoothDeadband(0.15, 0.15, deadband)
	// s = (0.15 - 0.10)/0.10 = 0.5. Hermite factor: 0.5^2 * (3 - 2*0.5) = 0.25 * 2 = 0.5
	expected := 0.15 * 0.5
	if math.Abs(valMid-expected) > 1e-4 {
		t.Fatalf("expected %f at midpoint, got %f", expected, valMid)
	}
}
