package resmon

import (
	"testing"
	"time"
)

func TestResmon_Sample(t *testing.T) {
	m, err := New()
	if err != nil {
		t.Fatalf("Failed to create monitor: %v", err)
	}

	// Sleep slightly to allow CPU time delta
	time.Sleep(50 * time.Millisecond)

	s := m.Sample()
	if s.RAMBytes == 0 {
		t.Errorf("Expected RAMBytes > 0, got 0")
	}
	if s.TotalRAMBytes == 0 {
		t.Errorf("Expected TotalRAMBytes > 0, got 0")
	}
	if s.CPUPercent < 0 {
		t.Errorf("Expected CPUPercent >= 0, got %f", s.CPUPercent)
	}
}

func TestResmon_RunLoop(t *testing.T) {
	samples := 0
	stop := RunLoop(20*time.Millisecond, func(s Stats) {
		samples++
	})
	time.Sleep(70 * time.Millisecond)
	stop()

	if samples == 0 {
		t.Errorf("Expected at least 1 sample from RunLoop, got 0")
	}
}

func TestResmon_ChildProcessTree(t *testing.T) {
	m, err := New()
	if err != nil {
		t.Fatalf("Failed to create monitor: %v", err)
	}

	initialSample := m.Sample()
	if initialSample.RAMBytes == 0 {
		t.Errorf("Expected initial RAMBytes > 0, got 0")
	}

	time.Sleep(50 * time.Millisecond)
	s := m.Sample()
	if s.RAMBytes == 0 {
		t.Errorf("Expected RAMBytes > 0, got 0")
	}
}
