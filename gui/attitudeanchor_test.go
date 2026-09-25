package main

import (
	"math"
	"math/rand"
	"testing"
)

// simulateHandMotion returns 60 Hz samples of fast wobbly rotation: the gyro sample
// (instantaneous rate with per-axis scale error and noise, device axes, rad/s) and
// the true attitude (device -> world) integrated finely in between.
func simulateHandMotion(seconds float64, seed int64) (gyro [][3]float64, truth []quat) {
	rng := rand.New(rand.NewSource(seed))
	const sub = 20
	const dt = 1.0 / 60.0
	scale := [3]float64{1.03, 0.98, 1.02}
	q := quat{1, 0, 0, 0}
	f := [3]float64{1.1 + rng.Float64(), 1.7 + rng.Float64(), 2.3 + rng.Float64()}
	rate := func(t float64) [3]float64 {
		if t > seconds-1.5 {
			return [3]float64{}
		}
		return [3]float64{4 * math.Sin(f[0]*t), 5 * math.Sin(f[1]*t+1), 6 * math.Sin(f[2]*t+2)}
	}
	for i := 0; float64(i)*dt < seconds; i++ {
		t := float64(i) * dt
		truth = append(truth, q)
		for s := 0; s < sub; s++ {
			ws := rate(t + (float64(s)+0.5)*dt/sub)
			q = qnormalize(qmul(q, qexp([3]float64{ws[0] * dt / sub, ws[1] * dt / sub, ws[2] * dt / sub})))
		}
		w := rate(t + dt/2)
		var g [3]float64
		for k := 0; k < 3; k++ {
			g[k] = w[k]*scale[k] + 0.005*rng.NormFloat64()
		}
		gyro = append(gyro, g)
	}
	return
}

// clientDriftDeg integrates the output stream like a DSU client and returns the final
// angle (deg) between the client's attitude and the true one.
func clientDriftDeg(useAnchor, lag bool) float64 {
	gyro, truth := simulateHandMotion(60, 3)
	an := newAttitudeAnchor()
	client := truth[0]
	for i := 1; i < len(truth); i++ {
		w := gyro[i-1]                    // rate covering truth[i-1] -> truth[i]
		if lag && i >= 1200 && i < 1230 { // network stall: this rotation never reaches the client
			w = [3]float64{}
		}
		if useAnchor {
			c := an.Correction(w, truth[i-1], 1.0/60)
			for k := 0; k < 3; k++ {
				w[k] += c[k]
			}
		}
		client = qnormalize(qmul(client, qexp([3]float64{w[0] / 60, w[1] / 60, w[2] / 60})))
	}
	return norm3(qlog(qmul(qconj(client), truth[len(truth)-1]))) / alignDegToRad
}

func TestAttitudeAnchorRemovesIntegrationDrift(t *testing.T) {
	raw := clientDriftDeg(false, false)
	anchored := clientDriftDeg(true, false)
	lagged := clientDriftDeg(true, true)
	t.Logf("drift after 60 s: raw gyro %.1f deg, anchored %.1f deg", raw, anchored)
	if raw < 10 {
		t.Fatalf("simulation not demanding enough: raw drift %.1f", raw)
	}
	t.Logf("after a 0.5 s stall that lost rotation: anchored %.1f deg", lagged)
	if lagged > 3 {
		t.Fatalf("stall left a %.1f deg offset, want < 3", lagged)
	}
	if anchored > 3 {
		t.Fatalf("anchored drift %.1f deg, want < 3", anchored)
	}
}
