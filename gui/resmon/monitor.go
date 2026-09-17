package resmon

import "time"

// Stats represents a snapshot of process resource consumption metrics.
type Stats struct {
	CPUPercent    float64 // Single core percentage (e.g., 2.5 means 2.5% of one core)
	RAMBytes      uint64  // RSS / Working Set memory in bytes
	TotalRAMBytes uint64  // Total physical RAM in bytes
}

// Monitor collects resource statistics for the current process.
// A single instance should be reused across the application lifecycle.
type Monitor interface {
	// Sample returns the current snapshot. Call no more often than once per 1-2 seconds.
	Sample() Stats
}

// New creates a platform-specific monitor implementation.
func New() (Monitor, error) {
	return newPlatformMonitor()
}

// RunLoop starts a sampling loop with the specified interval and callback.
// Returns a stop function to gracefully terminate the goroutine.
func RunLoop(interval time.Duration, onSample func(Stats)) (stop func()) {
	m, err := New()
	if err != nil {
		return func() {}
	}

	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				onSample(m.Sample())
			}
		}
	}()

	return func() {
		close(done)
	}
}
