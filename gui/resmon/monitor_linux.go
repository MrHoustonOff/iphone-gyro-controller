//go:build linux

package resmon

import (
	"bytes"
	"os"
	"strconv"
	"time"
)

const clockTicksHz = 100 // Standard USER_HZ on Linux (sysconf(_SC_CLK_TCK))

type linuxMonitor struct {
	pid       int
	lastTotal int64 // utime+stime in clock ticks
	lastWall  time.Time
	pageSize  int64
	statPath  string
	totalRAM  uint64
}

func readTotalRAMLinux() uint64 {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	lines := bytes.Split(data, []byte("\n"))
	for _, line := range lines {
		if bytes.HasPrefix(line, []byte("MemTotal:")) {
			fields := bytes.Fields(line)
			if len(fields) >= 2 {
				kb, _ := strconv.ParseUint(string(fields[1]), 10, 64)
				return kb * 1024
			}
		}
	}
	return 0
}

func newPlatformMonitor() (Monitor, error) {
	pid := os.Getpid()
	m := &linuxMonitor{
		pid:      pid,
		lastWall: time.Now(),
		pageSize: int64(os.Getpagesize()),
		statPath: "/proc/" + strconv.Itoa(pid) + "/stat",
		totalRAM: readTotalRAMLinux(),
	}
	total, err := m.readCPUTicks()
	if err != nil {
		return nil, err
	}
	m.lastTotal = total
	return m, nil
}

// readCPUTicks reads utime (field 14) + stime (field 15) from /proc/[pid]/stat.
func (m *linuxMonitor) readCPUTicks() (int64, error) {
	data, err := os.ReadFile(m.statPath)
	if err != nil {
		return 0, err
	}

	closeParen := bytes.LastIndexByte(data, ')')
	if closeParen == -1 {
		return 0, os.ErrInvalid
	}
	rest := data[closeParen+2:]
	fields := bytes.Fields(rest)

	const utimeIdx = 11
	const stimeIdx = 12
	if len(fields) <= stimeIdx {
		return 0, os.ErrInvalid
	}

	utime, err := strconv.ParseInt(string(fields[utimeIdx]), 10, 64)
	if err != nil {
		return 0, err
	}
	stime, err := strconv.ParseInt(string(fields[stimeIdx]), 10, 64)
	if err != nil {
		return 0, err
	}
	return utime + stime, nil
}

// readRSS reads VmRSS from /proc/[pid]/status in bytes.
func (m *linuxMonitor) readRSS() uint64 {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(m.pid) + "/status")
	if err != nil {
		return 0
	}
	lines := bytes.Split(data, []byte("\n"))
	for _, line := range lines {
		if bytes.HasPrefix(line, []byte("VmRSS:")) {
			fields := bytes.Fields(line)
			if len(fields) >= 2 {
				kb, _ := strconv.ParseUint(string(fields[1]), 10, 64)
				return kb * 1024
			}
		}
	}
	return 0
}

func (m *linuxMonitor) Sample() Stats {
	nowTotal, err := m.readCPUTicks()
	now := time.Now()

	var cpuPercent float64
	if err == nil {
		deltaTicks := nowTotal - m.lastTotal
		wallSeconds := now.Sub(m.lastWall).Seconds()
		if wallSeconds > 0 {
			cpuSeconds := float64(deltaTicks) / float64(clockTicksHz)
			cpuPercent = (cpuSeconds / wallSeconds) * 100
		}
		m.lastTotal = nowTotal
	}
	m.lastWall = now

	return Stats{
		CPUPercent:    cpuPercent,
		RAMBytes:      m.readRSS(),
		TotalRAMBytes: m.totalRAM,
	}
}
