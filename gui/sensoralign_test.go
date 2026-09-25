package main

import (
	"math"
	"math/rand"
	"testing"
	"time"
)

// iosQ maps device axes into the gyro packet axes produced by web/index.html on iOS
// Safari, whose rotationRate reports alpha/beta/gamma about device X/Y/Z:
// packet = (beta, gamma, alpha) = (devY, devZ, devX).
var iosQ = [3][3]float64{{0, 1, 0}, {0, 0, 1}, {1, 0, 0}}

// userProfile is the real "vertical iPhone" matrix from the calibration wizard.
var userProfile = [3][3]float64{{0, 0, 1}, {0, 1, 0}, {1, 0, 0}}

func rodrigues(v, axis [3]float64, angle float64) [3]float64 {
	n := norm3(axis)
	if n < 1e-12 {
		return v
	}
	k := [3]float64{axis[0] / n, axis[1] / n, axis[2] / n}
	c, s := math.Cos(angle), math.Sin(angle)
	kxv := cross3(k, v)
	kv := k[0]*v[0] + k[1]*v[1] + k[2]*v[2]
	var out [3]float64
	for i := 0; i < 3; i++ {
		out[i] = v[i]*c + kxv[i]*s + k[i]*kv*(1-c)
	}
	return out
}

type simFrame struct {
	rotPk [3]float64 // deg/s, packet axes
	acc   [3]float64 // g, device axes
	tsUs  uint64
}

// simulateIOSBursts mimics real handling: random-axis bursts (60..400 deg/s) with
// hand acceleration up to ~0.5g, separated by still pauses, plus sensor noise.
func simulateIOSBursts(seconds float64, seed int64) []simFrame {
	const dt = 1.0 / 60.0
	rng := rand.New(rand.NewSource(seed))
	g := [3]float64{0, 0, -1}
	var w [3]float64
	var out []simFrame
	for i := 0; float64(i)*dt < seconds; i++ {
		t := float64(i) * dt
		phase := math.Mod(t, 1.2)
		moving := phase < 0.5
		if i%72 == 0 {
			ax := [3]float64{rng.NormFloat64(), rng.NormFloat64(), rng.NormFloat64()}
			sp := (60 + 340*rng.Float64()) * alignDegToRad / norm3(ax)
			w = [3]float64{ax[0] * sp, ax[1] * sp, ax[2] * sp}
		}
		cur := w
		if !moving {
			cur = [3]float64{}
		}
		acc := g
		for k := 0; k < 3; k++ {
			acc[k] += 0.004 * rng.NormFloat64()
			if moving {
				acc[k] += 0.5 * math.Sin(9*t+2*float64(k))
			}
		}
		pk := mulVec3(iosQ, cur)
		for k := 0; k < 3; k++ {
			pk[k] = pk[k]/alignDegToRad + 0.3*rng.NormFloat64()
		}
		out = append(out, simFrame{rotPk: pk, acc: acc, tsUs: uint64(i) * 16667})
		g = rodrigues(g, cur, -norm3(cur)*dt)
	}
	return out
}

// simulateIOS rotates a phone (right-handed body rates) starting flat, screen up,
// and returns what the iOS web client would send.
func simulateIOS(seconds float64) []simFrame {
	const dt = 1.0 / 60.0
	g := [3]float64{0, 0, -1} // iOS: gravity along -Z when flat, screen up
	var out []simFrame
	for i := 0; float64(i)*dt < seconds; i++ {
		t := float64(i) * dt
		w := [3]float64{2.0 * math.Sin(1.3*t), 1.5 * math.Sin(0.7*t+1), 2.5 * math.Sin(0.9*t+2)}
		pk := mulVec3(iosQ, w)
		out = append(out, simFrame{
			rotPk: [3]float64{pk[0] / alignDegToRad, pk[1] / alignDegToRad, pk[2] / alignDegToRad},
			acc:   g,
			tsUs:  uint64(i) * 16667,
		})
		g = rodrigues(g, w, -norm3(w)*dt) // world-fixed vector seen from the body
	}
	return out
}

func TestSensorAlignerLearnsIOSAxes(t *testing.T) {
	for seed := int64(1); seed <= 20; seed++ {
		s := newSensorAligner("")
		for _, f := range simulateIOSBursts(20, seed) {
			s.Feed(f.rotPk, f.acc, f.tsUs)
		}
		got, known := s.Frame()
		if !known || got.Q != iosQ || got.H != -1 {
			t.Fatalf("seed %d: learned %+v known=%v, want Q=%v h=-1", seed, got, known, iosQ)
		}
	}
}

func TestOutputMappingFlatRestReadsMinusY(t *testing.T) {
	accMat, ys := buildOutputMapping(userProfile, sensorFrame{Q: iosQ, H: -1}, [3]float64{0, 0, -1})
	a := mulVec3(accMat, [3]float64{0, 0, -1})
	if math.Abs(a[0]) > 1e-9 || math.Abs(a[1]+1) > 1e-9 || math.Abs(a[2]) > 1e-9 {
		t.Fatalf("flat rest -> Acc %v, want [0 -1 0]", a)
	}
	if ys != 1 {
		t.Fatalf("yawSign = %v, want +1 for right-handed rates", ys)
	}
}

// madgwickGravityError settles PadTest's Madgwick on the first frame's gravity, then
// integrates the gyro alone and returns the max angle (deg) between the gravity it
// predicts and the gravity the accelerometer actually reports. When the DSU gyro and
// accelerometer agree this stays ~0 and Madgwick has nothing to fight.
func madgwickGravityError(frames []simFrame, gyroMat, accMat [3][3]float64, ys float64) float64 {
	toDSU := func(f simFrame) (r, a [3]float64) {
		r = mulVec3(gyroMat, f.rotPk)
		r[1] *= ys
		return r, mulVec3(accMat, f.acc)
	}

	m := NewMadgwickAHRS(2.0)
	_, a0 := toDSU(frames[0])
	for i := 0; i < 2000; i++ {
		m.Update(0, 0, 0, float32(a0[0]), float32(a0[1]), float32(a0[2]), time.Time{})
	}
	m.Beta = 0

	var worst float64
	for i, f := range frames {
		_, a := toDSU(f)
		if i > 0 { // frames[i].acc is the attitude after frames[i-1]'s rate
			pr, _ := toDSU(frames[i-1])
			m.Update(float32(pr[0]), float32(pr[1]), float32(pr[2]), 0, 0, 0, time.Time{})
		}
		q0, q1, q2, q3 := float64(m.Q0), float64(m.Q1), float64(m.Q2), float64(m.Q3)
		// Madgwick's expected body gravity, mapped back through PadTest's a = (AccX, -AccY, -AccZ)
		ex := 2 * (q1*q3 - q0*q2)
		ey := -2 * (q0*q1 + q2*q3)
		ez := -(1 - 2*(q1*q1+q2*q2))
		dot := (ex*a[0] + ey*a[1] + ez*a[2]) / norm3(a)
		worst = math.Max(worst, math.Acos(math.Max(-1, math.Min(1, dot)))*180/math.Pi)
	}
	return worst
}

func TestPadTestMadgwickAgreesAfterAlignment(t *testing.T) {
	frames := simulateIOS(30)
	accMat, ys := buildOutputMapping(userProfile, sensorFrame{Q: iosQ, H: -1}, [3]float64{0, 0, -1})

	if d := madgwickGravityError(frames, userProfile, accMat, ys); d > 2 {
		t.Fatalf("aligned pipeline: gyro-predicted gravity off by %.1f deg, want < 2", d)
	}
	// Sanity: the previous approach (gesture matrix applied to the accelerometer) must disagree.
	if d := madgwickGravityError(frames, userProfile, userProfile, 1); d < 20 {
		t.Fatalf("naive pipeline only %.1f deg off; test is not discriminating", d)
	}
}

// dsuKinematicResidual returns the mean relative residual of
// d(Acc)/dt = sign·(D·Rot) × Acc over the DSU output of a smooth iOS simulation.
func dsuKinematicResidual(sign float64) float64 {
	frames := simulateIOS(10)
	accMat, ys := buildOutputMapping(userProfile, sensorFrame{Q: iosQ, H: -1}, [3]float64{0, 0, -1})
	out := func(f simFrame) (r, a [3]float64) {
		r = mulVec3(userProfile, f.rotPk)
		r[1] *= ys * float64(dsuYawSign)
		a = mulVec3(accMat, f.acc)
		for k := 0; k < 3; k++ {
			r[k] *= alignDegToRad
			a[k] *= float64(dsuAccSign[k])
		}
		return r, a
	}
	var sum float64
	for i := 1; i < len(frames); i++ {
		r, a1 := out(frames[i-1])
		_, a2 := out(frames[i])
		dr := [3]float64{r[0], -r[1], -r[2]}
		pred := cross3(dr, a1)
		var e float64
		for k := 0; k < 3; k++ {
			d := (a2[k]-a1[k])*60 - sign*pred[k]
			e += d * d
		}
		sum += math.Sqrt(e) / (norm3(r) + 1e-9)
	}
	return sum / float64(len(frames)-1)
}

func TestDSUOutputMatchesClientConvention(t *testing.T) {
	flat, _ := buildOutputMapping(userProfile, sensorFrame{Q: iosQ, H: -1}, [3]float64{0, 0, -1})
	if y := mulVec3(flat, [3]float64{0, 0, -1})[1] * float64(dsuAccSign[1]); math.Abs(y+1) > 1e-9 {
		t.Fatalf("flat DSU AccY = %v, want -1", y)
	}
	if e := dsuKinematicResidual(-1); e > 0.05 {
		t.Fatalf("DSU output violates client kinematics: residual %.3f", e)
	}
	if e := dsuKinematicResidual(+1); e < 0.5 {
		t.Fatalf("residual for the wrong handedness only %.3f; test not discriminating", e)
	}
}
