package main

import (
	"math"
	"testing"
	"time"
)

func TestMadgwickAHRS_ConvergeToGravity_Flat(t *testing.T) {
	ahrs := NewMadgwickAHRS(0.0)

	// Phone flat on table: AccX = 0, AccY = 1.0 (pointing down in screen frame), AccZ = 0
	// (PadTest maps ax = +AccX = 0, ay = -AccY = -1.0, az = -AccZ = 0)
	ahrs.ConvergeToGravity(0.0, 1.0, 0.0)

	pitch, roll, yaw := ahrs.GetEulerAngles()
	if math.Abs(pitch) > 0.5 {
		t.Errorf("expected pitch near 0, got %.2f", pitch)
	}
	if math.Abs(roll) > 0.5 {
		t.Errorf("expected roll near 0, got %.2f", roll)
	}
	if math.Abs(yaw) > 0.5 {
		t.Errorf("expected yaw near 0, got %.2f", yaw)
	}
}

func TestMadgwickAHRS_ConvergeToGravity_TiltedPitch(t *testing.T) {
	ahrs := NewMadgwickAHRS(0.0)

	// Tilt phone 20 degrees pitch forward (nodding forward / nose down)
	targetPitchDeg := 20.0
	pitchRad := targetPitchDeg * math.Pi / 180.0

	// In PadTest coordinate frame:
	// ax = 0, ay = -cos(pitch), az = -sin(pitch)
	// Since PadTest maps ax = AccX, ay = -AccY, az = -AccZ:
	// AccX = 0, AccY = cos(pitch), AccZ = sin(pitch)
	accX := float32(0.0)
	accY := float32(math.Cos(pitchRad))
	accZ := float32(math.Sin(pitchRad))

	ahrs.ConvergeToGravity(accX, accY, accZ)

	pitch, roll, _ := ahrs.GetEulerAngles()
	if math.Abs(pitch-targetPitchDeg) > 0.5 {
		t.Errorf("expected pitch near %.2f, got %.2f", targetPitchDeg, pitch)
	}
	if math.Abs(roll) > 0.5 {
		t.Errorf("expected roll near 0, got %.2f", roll)
	}
}

func TestMadgwickAHRS_ConvergeToGravity_TiltedRoll(t *testing.T) {
	ahrs := NewMadgwickAHRS(0.0)

	// Tilt phone 15 degrees roll right (banking right)
	targetRollDeg := 15.0
	rollRad := targetRollDeg * math.Pi / 180.0

	// PadTest: ax = +AccX, ay = -AccY, az = -AccZ
	// For roll right: ax = -sin(roll), ay = -cos(roll)
	// => AccX = -sin(roll), AccY = cos(roll), AccZ = 0
	accX := float32(-math.Sin(rollRad))
	accY := float32(math.Cos(rollRad))
	accZ := float32(0.0)

	ahrs.ConvergeToGravity(accX, accY, accZ)

	pitch, roll, _ := ahrs.GetEulerAngles()
	if math.Abs(roll-targetRollDeg) > 0.5 {
		t.Errorf("expected roll near %.2f, got %.2f", targetRollDeg, roll)
	}
	if math.Abs(pitch) > 0.5 {
		t.Errorf("expected pitch near 0, got %.2f", pitch)
	}
}

func TestMadgwickAHRS_PreservesYaw(t *testing.T) {
	ahrs := NewMadgwickAHRS(0.0)

	// Integrate yaw rotation: 90 deg/s for 0.5s = 45 degrees
	now := time.Now()
	for i := 0; i < 30; i++ {
		now = now.Add(time.Millisecond * 16)
		ahrs.Update(0, 90, 0, 0, 1.0, 0, now)
	}

	_, _, yawBefore := ahrs.GetEulerAngles()
	if math.Abs(math.Abs(yawBefore)-45.0) > 5.0 {
		t.Fatalf("expected yaw magnitude around 45 deg, got %.2f", yawBefore)
	}

	// Now call ConvergeToGravity with flat gravity
	ahrs.ConvergeToGravity(0.0, 1.0, 0.0)

	_, _, yawAfter := ahrs.GetEulerAngles()
	if math.Abs(yawAfter-yawBefore) > 0.5 {
		t.Errorf("ConvergeToGravity wiped yaw! before=%.2f, after=%.2f", yawBefore, yawAfter)
	}
}
