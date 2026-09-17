//go:build windows

package resmon

import (
	"os"
	"path/filepath"
	"strings"
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

const (
	processQueryInformation = 0x0400
	processQueryLimitedInfo = 0x1000
	processVMRead           = 0x0010
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
	rootPID      uint32
	rootHandle   syscall.Handle
	lastCPUTimes map[uint32]int64
	lastWall     time.Time
	totalRAM     uint64
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
	rootPID := uint32(os.Getpid())
	m := &windowsMonitor{
		rootPID:      rootPID,
		rootHandle:   h,
		lastCPUTimes: make(map[uint32]int64),
		lastWall:     time.Now(),
		totalRAM:     readTotalRAMWindows(),
	}
	m.lastCPUTimes, _ = m.queryTreeStats()
	return m, nil
}

// getProcessTreePIDs collects rootPID and all descendant child PIDs belonging to GyroBridge.
func getProcessTreePIDs(rootPID uint32) []uint32 {
	snap, err := syscall.CreateToolhelp32Snapshot(syscall.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return []uint32{rootPID}
	}
	defer syscall.CloseHandle(snap)

	var entry syscall.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))

	if err := syscall.Process32First(snap, &entry); err != nil {
		return []uint32{rootPID}
	}

	exePath, _ := os.Executable()
	ourExeName := strings.ToLower(filepath.Base(exePath))

	childrenOf := make(map[uint32][]uint32)
	exeNameOf := make(map[uint32]string)

	for {
		name := strings.ToLower(syscall.UTF16ToString(entry.ExeFile[:]))
		exeNameOf[entry.ProcessID] = name
		childrenOf[entry.ParentProcessID] = append(childrenOf[entry.ParentProcessID], entry.ProcessID)
		if err := syscall.Process32Next(snap, &entry); err != nil {
			break
		}
	}

	treePIDs := []uint32{rootPID}
	queue := []uint32{rootPID}
	visited := make(map[uint32]bool)
	visited[rootPID] = true

	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]

		for _, child := range childrenOf[curr] {
			if !visited[child] {
				visited[child] = true
				childName := exeNameOf[child]
				// Only track our own executable instances (e.g. main app and --livedebug child process)
				if ourExeName == "" || childName == ourExeName || strings.HasPrefix(childName, "gyrobridge") {
					treePIDs = append(treePIDs, child)
					queue = append(queue, child)
				}
			}
		}
	}

	return treePIDs
}

func (m *windowsMonitor) queryTreeStats() (map[uint32]int64, uint64) {
	pids := getProcessTreePIDs(m.rootPID)
	cpuTimes := make(map[uint32]int64, len(pids))
	var totalRSS uint64

	toNanos := func(ft syscall.Filetime) int64 {
		return (int64(ft.HighDateTime)<<32 | int64(ft.LowDateTime)) * 100
	}

	for _, pid := range pids {
		var h syscall.Handle
		var mustClose bool

		if pid == m.rootPID {
			h = m.rootHandle
			mustClose = false
		} else {
			var err error
			h, err = syscall.OpenProcess(processQueryInformation|processVMRead, false, pid)
			if err != nil {
				h, err = syscall.OpenProcess(processQueryLimitedInfo|processVMRead, false, pid)
			}
			if err != nil {
				continue
			}
			mustClose = true
		}

		// Read CPU times
		var creation, exit, k, u syscall.Filetime
		r, _, _ := procGetProcessTimes.Call(
			uintptr(h),
			uintptr(unsafe.Pointer(&creation)),
			uintptr(unsafe.Pointer(&exit)),
			uintptr(unsafe.Pointer(&k)),
			uintptr(unsafe.Pointer(&u)),
		)
		if r != 0 {
			cpuTimes[pid] = toNanos(k) + toNanos(u)
		}

		// Read RAM Working Set
		var pmc processMemoryCounters
		pmc.cb = uint32(unsafe.Sizeof(pmc))
		r, _, _ = procGetProcessMemoryInfo.Call(
			uintptr(h),
			uintptr(unsafe.Pointer(&pmc)),
			uintptr(pmc.cb),
		)
		if r != 0 {
			totalRSS += uint64(pmc.WorkingSetSize)
		}

		if mustClose {
			syscall.CloseHandle(h)
		}
	}

	return cpuTimes, totalRSS
}

func (m *windowsMonitor) Sample() Stats {
	nowCPUTimes, totalRSS := m.queryTreeStats()
	now := time.Now()

	var totalCPUDeltaNanos int64
	for pid, nowCPU := range nowCPUTimes {
		if prevCPU, ok := m.lastCPUTimes[pid]; ok {
			delta := nowCPU - prevCPU
			if delta > 0 {
				totalCPUDeltaNanos += delta
			}
		}
		// Newly discovered process: establish baseline without false cumulative delta spike
	}

	wallDeltaNanos := now.Sub(m.lastWall).Nanoseconds()
	var cpuPercent float64
	if wallDeltaNanos > 0 {
		cpuPercent = float64(totalCPUDeltaNanos) / float64(wallDeltaNanos) * 100
	}

	m.lastCPUTimes = nowCPUTimes
	m.lastWall = now

	return Stats{
		CPUPercent:    cpuPercent,
		RAMBytes:      totalRSS,
		TotalRAMBytes: m.totalRAM,
	}
}
