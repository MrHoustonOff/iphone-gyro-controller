//go:build windows

package resmon

import (
	"syscall"
	"time"
	"unsafe"
)

var (
	kernel32                 = syscall.NewLazyDLL("kernel32.dll")
	psapi                    = syscall.NewLazyDLL("psapi.dll")
	procGetProcessMemoryInfo = psapi.NewProc("GetProcessMemoryInfo")
	procGetProcessTimes      = kernel32.NewProc("GetProcessTimes")
	procGlobalMemoryStatusEx = kernel32.NewProc("GlobalMemoryStatusEx")
)

// processMemoryCounters matches Windows PROCESS_MEMORY_COUNTERS layout.
type processMemoryCounters struct {
	cb                         uint32
	PageFaultCount             uint32
	PeakWorkingSetSize         uintptr
	WorkingSetSize             uintptr // RSS / Working Set
	QuotaPeakPagedPoolUsage    uintptr
	QuotaPagedPoolUsage        uintptr
	QuotaPeakNonPagedPoolUsage uintptr
	QuotaNonPagedPoolUsage     uintptr
	PagefileUsage              uintptr
	PeakPagefileUsage          uintptr
}

// memoryStatusEx matches Windows MEMORYSTATUSEX layout.
type memoryStatusEx struct {
	cbSize                  uint32
	dwMemoryLoad            uint32
	ullTotalPhys            uint64
	ullAvailPhys            uint64
	ullTotalPageFile        uint64
	ullAvailPageFile        uint64
	ullTotalVirtual         uint64
	ullAvailVirtual         uint64
	ullAvailExtendedVirtual uint64
}

type windowsMonitor struct {
	handle     syscall.Handle
	lastKernel int64 // 100-ns ticks
	lastUser   int64
	lastWall   time.Time
	totalRAM   uint64
}

func readTotalRAMWindows() uint64 {
	var mse memoryStatusEx
	mse.cbSize = uint32(unsafe.Sizeof(mse))
	r, _, _ := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&mse)))
	if r != 0 {
		return mse.ullTotalPhys
	}
	return 0
}

func newPlatformMonitor() (Monitor, error) {
	h, err := syscall.GetCurrentProcess()
	if err != nil {
		return nil, err
	}
	m := &windowsMonitor{
		handle:   h,
		lastWall: time.Now(),
		totalRAM: readTotalRAMWindows(),
	}
	k, u := m.readCPUTimes()
	m.lastKernel, m.lastUser = k, u
	return m, nil
}

func (m *windowsMonitor) readCPUTimes() (kernel, user int64) {
	var creation, exit, k, u syscall.Filetime
	procGetProcessTimes.Call(
		uintptr(m.handle),
		uintptr(unsafe.Pointer(&creation)),
		uintptr(unsafe.Pointer(&exit)),
		uintptr(unsafe.Pointer(&k)),
		uintptr(unsafe.Pointer(&u)),
	)
	toNanos := func(ft syscall.Filetime) int64 {
		return (int64(ft.HighDateTime)<<32 | int64(ft.LowDateTime)) * 100
	}
	return toNanos(k), toNanos(u)
}

func (m *windowsMonitor) readRSS() uint64 {
	var pmc processMemoryCounters
	pmc.cb = uint32(unsafe.Sizeof(pmc))
	procGetProcessMemoryInfo.Call(
		uintptr(m.handle),
		uintptr(unsafe.Pointer(&pmc)),
		uintptr(pmc.cb),
	)
	return uint64(pmc.WorkingSetSize)
}

func (m *windowsMonitor) Sample() Stats {
	nowKernel, nowUser := m.readCPUTimes()
	now := time.Now()

	cpuDeltaNanos := (nowKernel - m.lastKernel) + (nowUser - m.lastUser)
	wallDeltaNanos := now.Sub(m.lastWall).Nanoseconds()

	var cpuPercent float64
	if wallDeltaNanos > 0 {
		cpuPercent = float64(cpuDeltaNanos) / float64(wallDeltaNanos) * 100
	}

	m.lastKernel, m.lastUser, m.lastWall = nowKernel, nowUser, now

	return Stats{
		CPUPercent:    cpuPercent,
		RAMBytes:      m.readRSS(),
		TotalRAMBytes: m.totalRAM,
	}
}
