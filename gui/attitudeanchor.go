package main

import (
	"log"
	"math"
)

// Attitude anchoring.
//
// The browser gives the gyro only at 60 Hz. Integrating 60 Hz samples of fast,
// wobbly hand motion (what DSU clients do) accumulates several degrees of error per
// minute; gravity later fixes tilt but nothing fixes yaw. The phone's own attitude
// (DeviceOrientation, fused by the OS at a much higher internal rate) does not have
// that error. We keep the live gyro for responsiveness and add a small, clamped
// correction rate so the angle a client integrates from our stream is slowly pulled
// onto the phone's attitude on all three axes.
//
// Which quaternion convention/handedness the browser really uses is not trusted: the
// frame-to-frame rotation of each candidate is compared against the gyro, and the
// anchor only engages once one candidate clearly matches.

type quat [4]float64 // w, x, y, z

const (
	anchorGain         = 2.0 // 1/s: fraction of the error removed per second
	anchorMaxCorrRad   = 20.0 * alignDegToRad
	anchorMinStepRad   = 0.5 * alignDegToRad // per-frame rotation needed to score a candidate
	anchorScoreFrames  = 120
	anchorMaxRelErr    = 0.25 // mean relative step mismatch of the winner
	anchorMarginRatio  = 4.0
	anchorCandidates   = 4 // {q, q*} × {+, -} rotation sense
	anchorMinQuatNorm  = 0.5
	anchorDefaultDtSec = 1.0 / 60.0
)

type attitudeAnchor struct {
	prev     quat
	havePrev bool

	score [anchorCandidates]float64
	n     int
	mode  int // -1 until a candidate is proven

	// Attitude a client reaches by integrating our output, per candidate convention,
	// tracked from the first frame so drift from before engagement is corrected too.
	est     [anchorCandidates]quat
	haveEst bool
}

func newAttitudeAnchor() *attitudeAnchor {
	return &attitudeAnchor{mode: -1}
}

// Reset forgets the client model (keeps the learned convention). Call it when the
// phone reconnects: its attitude reference restarts from an arbitrary heading.
func (a *attitudeAnchor) Reset() {
	a.havePrev, a.haveEst = false, false
}

// Correction returns the correction rate (device axes, rad/s) to add to the gyro for
// this frame. gyroDev is the bias-corrected gyro in device axes (rad/s), ref the
// phone's attitude quaternion, dt the frame period.
func (a *attitudeAnchor) Correction(gyroDev [3]float64, ref quat, dt float64) [3]float64 {
	var zero [3]float64
	if qnorm(ref) < anchorMinQuatNorm {
		a.havePrev, a.haveEst = false, false
		return zero
	}
	ref = qnormalize(ref)

	if a.havePrev {
		a.scoreStep(gyroDev, ref, dt)
	}
	a.prev, a.havePrev = ref, true

	if !a.haveEst {
		for c := range a.est {
			a.est[c], _ = view(c, ref)
		}
		a.haveEst = true
	}

	var corr [3]float64
	if a.mode >= 0 {
		att, sense := view(a.mode, ref)
		e := qlog(qmul(qconj(a.est[a.mode]), att))
		// Never resync on a large error: a network stall that lost rotation shows up
		// exactly like that, and accepting it would leave the game offset for good.
		// Chase it at the clamped rate instead; only Reset (reconnect) resyncs.
		for k := 0; k < 3; k++ {
			corr[k] = sense * anchorGain * e[k]
		}
		if n := norm3(corr); n > anchorMaxCorrRad {
			for k := 0; k < 3; k++ {
				corr[k] *= anchorMaxCorrRad / n
			}
		}
	}

	// Advance every client model with what we actually output this frame.
	for c := range a.est {
		_, sense := view(c, ref)
		var out [3]float64
		for k := 0; k < 3; k++ {
			out[k] = sense * (gyroDev[k] + corr[k]) * dt
		}
		a.est[c] = qnormalize(qmul(a.est[c], qexp(out)))
	}
	return corr
}

// view maps the reference quaternion into candidate convention c.
func view(c int, q quat) (quat, float64) {
	if c/2 == 1 {
		q = qconj(q)
	}
	sense := 1.0
	if c%2 == 1 {
		sense = -1
	}
	return q, sense
}

func (a *attitudeAnchor) scoreStep(gyroDev [3]float64, ref quat, dt float64) {
	g := [3]float64{gyroDev[0] * dt, gyroDev[1] * dt, gyroDev[2] * dt}
	gn := norm3(g)
	if gn < anchorMinStepRad {
		return
	}
	for c := 0; c < anchorCandidates; c++ {
		p, q := a.prev, ref
		if c/2 == 1 {
			p, q = qconj(p), qconj(q)
		}
		d := qlog(qmul(qconj(p), q))
		if c%2 == 1 {
			d = [3]float64{-d[0], -d[1], -d[2]}
		}
		r := [3]float64{d[0] - g[0], d[1] - g[1], d[2] - g[2]}
		a.score[c] += math.Min(norm3(r)/gn, 2)
	}
	a.n++
	if a.n < anchorScoreFrames {
		return
	}
	best, second := 0, -1
	for c := 1; c < anchorCandidates; c++ {
		if a.score[c] < a.score[best] {
			second, best = best, c
		} else if second < 0 || a.score[c] < a.score[second] {
			second = c
		}
	}
	mean := a.score[best] / float64(a.n)
	if mean < anchorMaxRelErr && a.score[second] > anchorMarginRatio*a.score[best] {
		if a.mode != best {
			log.Printf("[anchor] attitude reference engaged: mode %d (step err %.2f)", best, mean)
			a.mode = best
		}
	} else if a.mode >= 0 && mean >= anchorMaxRelErr {
		log.Printf("[anchor] attitude reference disagrees with gyro (err %.2f): disengaged", mean)
		a.mode = -1
	}
	a.score = [anchorCandidates]float64{}
	a.n = 0
}

func qmul(a, b quat) quat {
	return quat{
		a[0]*b[0] - a[1]*b[1] - a[2]*b[2] - a[3]*b[3],
		a[0]*b[1] + a[1]*b[0] + a[2]*b[3] - a[3]*b[2],
		a[0]*b[2] - a[1]*b[3] + a[2]*b[0] + a[3]*b[1],
		a[0]*b[3] + a[1]*b[2] - a[2]*b[1] + a[3]*b[0],
	}
}

func qconj(q quat) quat { return quat{q[0], -q[1], -q[2], -q[3]} }

func qnorm(q quat) float64 { return math.Sqrt(q[0]*q[0] + q[1]*q[1] + q[2]*q[2] + q[3]*q[3]) }

func qnormalize(q quat) quat {
	n := qnorm(q)
	return quat{q[0] / n, q[1] / n, q[2] / n, q[3] / n}
}

// qlog returns the rotation vector (axis·angle, shortest path) of a unit quaternion.
func qlog(q quat) [3]float64 {
	if q[0] < 0 {
		q = quat{-q[0], -q[1], -q[2], -q[3]}
	}
	v := [3]float64{q[1], q[2], q[3]}
	s := norm3(v)
	if s < 1e-12 {
		return [3]float64{2 * v[0], 2 * v[1], 2 * v[2]}
	}
	k := 2 * math.Atan2(s, q[0]) / s
	return [3]float64{v[0] * k, v[1] * k, v[2] * k}
}

// qexp returns the unit quaternion of a rotation vector.
func qexp(r [3]float64) quat {
	th := norm3(r)
	if th < 1e-12 {
		return quat{1, r[0] / 2, r[1] / 2, r[2] / 2}
	}
	s := math.Sin(th/2) / th
	return quat{math.Cos(th / 2), r[0] * s, r[1] * s, r[2] * s}
}
