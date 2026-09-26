package main

import (
	"encoding/json"
	"log"
	"math"
	"os"
	"path/filepath"
	"sync"
)

// Sensor frame alignment.
//
// The phone sends gyro and accelerometer in whatever axes the browser exposes, and
// browsers disagree: Chrome/Android follows the W3C spec, while iOS Safari reports
// rotationRate with permuted axes (and gravity with the opposite sign). The
// calibration matrix is derived from gyro gestures only, so it is valid for the gyro
// but NOT for the accelerometer. Feeding the accelerometer through it (or through an
// independently guessed gravity matrix) gives PadTest/Cemu a gyro and a gravity
// vector that disagree, and their Madgwick filter fights itself.
//
// Instead of hardcoding per-platform quirks we learn the relation from physics.
// Gravity is fixed in the world, so seen from the rotating phone it obeys
//
//	d(a)/dt = h · (ω × a)        (h = -1 for right-handed rate reporting)
//
// where a is the accelerometer rotated into the gyro packet axes by a proper signed
// permutation Q. Between two still moments we integrate the gyro under each of the
// 24 Q × 2 h hypotheses and check which one lands gravity where the accelerometer
// reports it; the true mapping wins by a wide margin after a handful of tilts.

// sensorFrame maps raw accelerometer axes into raw gyro packet axes.
type sensorFrame struct {
	Q [3][3]float64 `json:"q"` // a_pk = Q · acc_raw (det = +1)
	H float64       `json:"h"` // rate handedness: -1 right-handed, +1 left-handed
}

const (
	alignMinPairs    = 6    // informative still→still pairs before deciding
	alignMaxPairs    = 40   // give up on an undecided batch and start fresh
	alignMinTiltDeg  = 20.0 // gravity must really move between the two still moments
	alignMaxSpanSec  = 3.0  // longer spans accumulate gyro drift
	alignStillRate   = 0.5  // rad/s (~30°/s)
	alignStillAccTol = 0.07 // | |acc| - 1g |
	alignStillJerk   = 0.03 // g change per frame: accelerometer settled
	alignMaxErrDeg   = 10.0 // winner's mean prediction error
	alignMarginRatio = 2.5  // runner-up must be this much worse
	alignErrClampDeg = 45.0
	alignDegToRad    = math.Pi / 180.0
	alignPersistFile = "sensor_frame.json"
)

type sensorAligner struct {
	mu     sync.Mutex
	path   string
	cands  []sensorFrame // 24 proper Q; h is scored separately
	errSum []float64     // [2*i + (h==+1)] accumulated prediction error, degrees
	pairs  int

	havePrev bool
	prevAcc  [3]float64
	prevTsUs uint64

	// Anchor: the last moment the phone was still. rotM/rotP accumulate the body
	// rotation since then (packet axes) under h = -1 / h = +1.
	anchored  bool
	anchorAcc [3]float64
	anchorAge float64
	rotM      [3][3]float64
	rotP      [3][3]float64

	cur   sensorFrame
	known bool

	// onLearn receives every newly locked frame (stored in the active profile).
	onLearn func(sensorFrame)
}

// newSensorAligner creates an aligner; dir only migrates a legacy global sensor_frame.json
// (the mapping now lives in each profile).
func newSensorAligner(dir string) *sensorAligner {
	s := &sensorAligner{
		cands: properSignedPermutations(),
		cur:   sensorFrame{Q: identity3(), H: -1}, // W3C spec until proven otherwise
	}
	s.errSum = make([]float64, 2*len(s.cands))
	if dir != "" {
		s.path = filepath.Join(dir, alignPersistFile)
		if data, err := os.ReadFile(s.path); err == nil {
			var f sensorFrame
			if json.Unmarshal(data, &f) == nil && math.Abs(f.H) == 1 && math.Abs(det3x3(f.Q)-1) < 1e-6 {
				s.cur = f
				s.known = true
			}
		}
	}
	return s
}

// Feed consumes one bias-corrected raw frame (gyro packet axes in °/s, acc in g,
// phone timestamp in µs). Safe to call for every frame.
//
// During fast motion the accelerometer mostly measures the hand (a 400°/s flick
// reads 1.5g+), so gravity is only compared at still moments: integrate the gyro
// from one still moment to the next and check where each hypothesis says gravity
// should have ended up.
func (s *sensorAligner) Feed(rot, acc [3]float64, tsUs uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	dt := 1.0 / 60.0
	jerk := math.Inf(1)
	if s.havePrev {
		if tsUs > s.prevTsUs {
			if d := float64(tsUs-s.prevTsUs) / 1e6; d >= 0.004 && d <= 0.1 {
				dt = d
			}
		}
		jerk = norm3([3]float64{acc[0] - s.prevAcc[0], acc[1] - s.prevAcc[1], acc[2] - s.prevAcc[2]})
	}
	s.prevAcc, s.prevTsUs, s.havePrev = acc, tsUs, true

	w := [3]float64{rot[0] * alignDegToRad, rot[1] * alignDegToRad, rot[2] * alignDegToRad}
	wn := norm3(w)
	if s.anchored {
		s.rotM = matMul(axisAngle(w, -wn*dt), s.rotM)
		s.rotP = matMul(axisAngle(w, wn*dt), s.rotP)
		s.anchorAge += dt
	}

	still := wn < alignStillRate && math.Abs(norm3(acc)-1) < alignStillAccTol && jerk < alignStillJerk
	if !still {
		return
	}
	if s.anchored && s.anchorAge <= alignMaxSpanSec {
		if angleDeg(s.anchorAcc, acc) < alignMinTiltDeg {
			return // not informative yet; keep integrating from the same anchor
		}
		s.scorePairLocked(acc)
	}
	s.anchored, s.anchorAcc, s.anchorAge = true, acc, 0
	s.rotM, s.rotP = identity3(), identity3()
}

func (s *sensorAligner) scorePairLocked(acc [3]float64) {
	for i, c := range s.cands {
		from := mulVec3(c.Q, s.anchorAcc)
		to := mulVec3(c.Q, acc)
		s.errSum[2*i] += math.Min(angleDeg(mulVec3(s.rotM, from), to), alignErrClampDeg)
		s.errSum[2*i+1] += math.Min(angleDeg(mulVec3(s.rotP, from), to), alignErrClampDeg)
	}
	s.pairs++
	if s.pairs >= alignMinPairs {
		s.decideLocked()
	}
}

func (s *sensorAligner) decideLocked() {
	best, second := -1, -1
	for i := range s.errSum {
		if best < 0 || s.errSum[i] < s.errSum[best] {
			second, best = best, i
		} else if second < 0 || s.errSum[i] < s.errSum[second] {
			second = i
		}
	}
	bestMean := s.errSum[best] / float64(s.pairs)
	secondMean := s.errSum[second] / float64(s.pairs)

	if bestMean < alignMaxErrDeg && secondMean > alignMarginRatio*bestMean {
		f := s.cands[best/2]
		f.H = [2]float64{-1, 1}[best%2]
		if !s.known || f != s.cur {
			log.Printf("[align] sensor frame locked: Q=%v h=%+.0f (err %.1f°, runner-up %.1f°, %d pairs)",
				f.Q, f.H, bestMean, secondMean, s.pairs)
			s.cur = f
			s.known = true
			s.saveLocked()
		}
	} else if s.pairs < alignMaxPairs {
		return // keep collecting evidence
	}
	for i := range s.errSum {
		s.errSum[i] = 0
	}
	s.pairs = 0
}

func (s *sensorAligner) saveLocked() {
	if s.onLearn != nil {
		s.onLearn(s.cur)
	}
}

// iosSensorFrame is the axis relation iOS Safari has been observed to use for every
// device tested so far (see docs/motion-pipeline.md §3): rotationRate reports
// (beta, gamma, alpha) = (devY, devZ, devX), so a_pk = Q · acc_raw with
// Q = [[0,1,0],[0,0,1],[1,0,0]], h = -1.
func iosSensorFrame() sensorFrame {
	return sensorFrame{Q: [3][3]float64{{0, 1, 0}, {0, 0, 1}, {1, 0, 0}}, H: -1}
}

// SeedGuess sets the working hypothesis used for the accelerometer mapping before any
// physics evidence has been collected, so a brand-new profile doesn't ship a wrong
// gravity axis (identity) while it waits for the 6+ still-tilt-still pairs the real
// alignment learning needs. It never overwrites a mapping already learned or loaded
// from a profile (known == true) and never marks the guess as "known": the physics
// scoring in scorePairLocked/decideLocked keeps running and will correct a wrong
// guess exactly like it would correct the identity default.
func (s *sensorAligner) SeedGuess(f sensorFrame) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.known {
		s.cur = f
	}
}

// Reset clears accumulated still-tilt-still evidence so a fresh confidence readout can
// be taken (used by the explicit "determine axes" wizard step). When forgetKnown is
// true, the current mapping is also marked unconfirmed so the wizard step must
// re-earn it from scratch; cur is left untouched either way so accelerometer output
// never regresses to identity mid-reset.
func (s *sensorAligner) Reset(forgetKnown bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.errSum {
		s.errSum[i] = 0
	}
	s.pairs = 0
	s.anchored = false
	if forgetKnown {
		s.known = false
	}
}

// Progress reports how many informative still-tilt-still pairs have been scored since
// the last Reset/lock, and how many are required to decide. Used by the wizard to show
// a live "N of M tilts" readout during the explicit axis-alignment step.
func (s *sensorAligner) Progress() (pairs, minPairs int, known bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pairs, alignMinPairs, s.known
}

// SetFrame switches to another device's mapping (profile change) and drops any
// half-collected evidence. known=false falls back to the W3C default and relearns.
func (s *sensorAligner) SetFrame(f sensorFrame, known bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !known {
		f = sensorFrame{Q: identity3(), H: -1}
	}
	s.cur, s.known = f, known
	for i := range s.errSum {
		s.errSum[i] = 0
	}
	s.pairs, s.anchored = 0, false
}

// AxisMapping describes the current accelerometer→gyro axis relation as three signed
// axis labels (packet X, Y, Z <- raw device axis), e.g. ["+Y", "+Z", "+X"] for the iOS
// default. Used by the wizard to show the user what was actually determined.
func (s *sensorAligner) AxisMapping() [3]string {
	s.mu.Lock()
	q := s.cur.Q
	s.mu.Unlock()
	axes := [3]string{"X", "Y", "Z"}
	var out [3]string
	for r := 0; r < 3; r++ {
		out[r] = "?"
		for c := 0; c < 3; c++ {
			if q[r][c] > 0.5 {
				out[r] = "+" + axes[c]
			} else if q[r][c] < -0.5 {
				out[r] = "-" + axes[c]
			}
		}
	}
	return out
}

// Frame returns the current mapping and whether it was learned (or loaded) rather than assumed.
func (s *sensorAligner) Frame() (sensorFrame, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cur, s.known
}

// buildOutputMapping derives the DSU output transform from the gyro calibration matrix.
//
// PadTest/Cemu run Madgwick on g = -Rot, a = D·Acc with D = diag(1,-1,-1) (see ahrs.go).
// For gyro and gravity to agree there, the DSU fields must satisfy
//
//	d(Acc)/dt = (D·Rot) × Acc
//
// With Rot = S·mat·ω_pk (S flips yaw by ys) and Acc = N·a_pk this holds iff
// N = ±D·S·mat and det(D·S·mat) = h, i.e. ys = -h (det(mat) = -1 by construction).
// The remaining ± is gravity's sign, which no kinematics can observe (browsers
// disagree on it); we pick it so the calibration rest pose reads AccY = -1g,
// the flat-on-table convention of Cemuhook DSU.
func buildOutputMapping(mat [3][3]float64, f sensorFrame, calGravity [3]float64) (accMat [3][3]float64, yawSign float64) {
	// det(S)·det(mat) = h  →  ys = h·det(mat)
	yawSign = 1
	if f.H*det3x3(mat) < 0 {
		yawSign = -1
	}
	dsm := mat
	for j := 0; j < 3; j++ {
		dsm[1][j] *= -yawSign // D flips Y, S flips Y by yawSign
		dsm[2][j] *= -1       // D flips Z
	}
	accMat = matMul(dsm, f.Q)

	if norm3(calGravity) > 0.3 {
		if y := mulVec3(accMat, calGravity)[1]; y > 0 {
			for i := 0; i < 3; i++ {
				for j := 0; j < 3; j++ {
					accMat[i][j] = -accMat[i][j]
				}
			}
		}
	}
	return accMat, yawSign
}

// Real DSU clients (PadTest, Cemu, yuzu-family) read the pad in a frame where
// d(Acc)/dt = -(D·Rot) × Acc, the opposite handedness of our ahrs.go clone
// (+(D·Rot) × Acc). Flipping yaw alone would break gyro↔gravity agreement; the
// consistent fix that keeps flat = AccY -1g is Rot → S·Rot, Acc → -S·Acc with
// S = diag(1,-1,1). Verified in PadTest: yaw mirrored and held tilts slid back
// until all three were applied.
const dsuYawSign float32 = -1

var dsuAccSign = [3]float32{-1, 1, -1}

func properSignedPermutations() []sensorFrame {
	perms := [6][3]int{{0, 1, 2}, {0, 2, 1}, {1, 0, 2}, {1, 2, 0}, {2, 0, 1}, {2, 1, 0}}
	var out []sensorFrame
	for _, p := range perms {
		for signs := 0; signs < 8; signs++ {
			var m [3][3]float64
			for r := 0; r < 3; r++ {
				sg := 1.0
				if signs&(1<<r) != 0 {
					sg = -1
				}
				m[r][p[r]] = sg
			}
			if det3x3(m) > 0 {
				out = append(out, sensorFrame{Q: m, H: -1})
			}
		}
	}
	return out
}

func identity3() [3][3]float64 {
	return [3][3]float64{{1, 0, 0}, {0, 1, 0}, {0, 0, 1}}
}

func mulVec3(m [3][3]float64, v [3]float64) [3]float64 {
	x, y, z := applyMatrix(m, v[0], v[1], v[2])
	return [3]float64{x, y, z}
}

func cross3(a, b [3]float64) [3]float64 {
	return [3]float64{
		a[1]*b[2] - a[2]*b[1],
		a[2]*b[0] - a[0]*b[2],
		a[0]*b[1] - a[1]*b[0],
	}
}

func norm3(v [3]float64) float64 {
	return math.Sqrt(v[0]*v[0] + v[1]*v[1] + v[2]*v[2])
}

// axisAngle returns the rotation matrix for angle (rad) about axis (any length).
func axisAngle(axis [3]float64, angle float64) [3][3]float64 {
	n := norm3(axis)
	if n < 1e-12 || angle == 0 {
		return identity3()
	}
	x, y, z := axis[0]/n, axis[1]/n, axis[2]/n
	c, s := math.Cos(angle), math.Sin(angle)
	t := 1 - c
	return [3][3]float64{
		{t*x*x + c, t*x*y - s*z, t*x*z + s*y},
		{t*x*y + s*z, t*y*y + c, t*y*z - s*x},
		{t*x*z - s*y, t*y*z + s*x, t*z*z + c},
	}
}

func angleDeg(a, b [3]float64) float64 {
	d := (a[0]*b[0] + a[1]*b[1] + a[2]*b[2]) / (norm3(a)*norm3(b) + 1e-12)
	return math.Acos(math.Max(-1, math.Min(1, d))) * 180 / math.Pi
}

func transpose3(m [3][3]float64) [3][3]float64 {
	return [3][3]float64{
		{m[0][0], m[1][0], m[2][0]},
		{m[0][1], m[1][1], m[2][1]},
		{m[0][2], m[1][2], m[2][2]},
	}
}
