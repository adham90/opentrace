package vmagent

import (
	"context"
	"fmt"
	"runtime"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/net"
	"github.com/shirou/gopsutil/v4/process"

	"github.com/adham90/opentrace/pkg/store"
)

// HostInfo contains static information about the host, sent during registration.
type HostInfo struct {
	Hostname string
	OS       string
	Arch     string
}

// GetHostInfo collects static host information for registration.
func GetHostInfo() (HostInfo, error) {
	info, err := host.Info()
	if err != nil {
		return HostInfo{}, fmt.Errorf("getting host info: %w", err)
	}
	return HostInfo{
		Hostname: info.Hostname,
		OS:       info.OS,
		Arch:     runtime.GOARCH,
	}, nil
}

// Collector collects system metrics from the host.
type Collector struct {
	lastNetCounters map[string]net.IOCountersStat
	lastCollectTime time.Time
}

// NewCollector creates a new Collector.
func NewCollector() *Collector {
	return &Collector{}
}

// Collect gathers all system metrics and returns them as MetricSamples.
func (c *Collector) Collect(ctx context.Context) ([]store.MetricSample, error) {
	var samples []store.MetricSample

	// CPU
	if cpuSamples, err := c.collectCPU(ctx); err == nil {
		samples = append(samples, cpuSamples...)
	}

	// Memory
	if memSamples, err := c.collectMemory(); err == nil {
		samples = append(samples, memSamples...)
	}

	// Swap
	if swapSamples, err := c.collectSwap(); err == nil {
		samples = append(samples, swapSamples...)
	}

	// Disk
	if diskSamples, err := c.collectDisk(); err == nil {
		samples = append(samples, diskSamples...)
	}

	// Network
	if netSamples, err := c.collectNetwork(); err == nil {
		samples = append(samples, netSamples...)
	}

	// Load averages
	if loadSamples, err := c.collectLoad(); err == nil {
		samples = append(samples, loadSamples...)
	}

	// Process count
	if procSamples, err := c.collectProcessCount(); err == nil {
		samples = append(samples, procSamples...)
	}

	// Uptime
	if uptimeSamples, err := c.collectUptime(); err == nil {
		samples = append(samples, uptimeSamples...)
	}

	return samples, nil
}

func (c *Collector) collectCPU(ctx context.Context) ([]store.MetricSample, error) {
	percentages, err := cpu.PercentWithContext(ctx, time.Second, false)
	if err != nil || len(percentages) == 0 {
		return nil, err
	}

	samples := []store.MetricSample{
		{Name: "cpu.usage_percent", Value: percentages[0], Unit: "percent"},
	}

	times, err := cpu.TimesWithContext(ctx, false)
	if err == nil && len(times) > 0 {
		total := times[0].User + times[0].System + times[0].Idle + times[0].Nice +
			times[0].Iowait + times[0].Irq + times[0].Softirq + times[0].Steal
		if total > 0 {
			samples = append(samples,
				store.MetricSample{Name: "cpu.user_percent", Value: times[0].User / total * 100, Unit: "percent"},
				store.MetricSample{Name: "cpu.system_percent", Value: times[0].System / total * 100, Unit: "percent"},
				store.MetricSample{Name: "cpu.idle_percent", Value: times[0].Idle / total * 100, Unit: "percent"},
			)
		}
	}

	return samples, nil
}

func (c *Collector) collectMemory() ([]store.MetricSample, error) {
	v, err := mem.VirtualMemory()
	if err != nil {
		return nil, err
	}
	return []store.MetricSample{
		{Name: "memory.total_bytes", Value: float64(v.Total), Unit: "bytes"},
		{Name: "memory.used_bytes", Value: float64(v.Used), Unit: "bytes"},
		{Name: "memory.available_bytes", Value: float64(v.Available), Unit: "bytes"},
		{Name: "memory.usage_percent", Value: v.UsedPercent, Unit: "percent"},
	}, nil
}

func (c *Collector) collectSwap() ([]store.MetricSample, error) {
	s, err := mem.SwapMemory()
	if err != nil {
		return nil, err
	}
	return []store.MetricSample{
		{Name: "swap.total_bytes", Value: float64(s.Total), Unit: "bytes"},
		{Name: "swap.used_bytes", Value: float64(s.Used), Unit: "bytes"},
		{Name: "swap.usage_percent", Value: s.UsedPercent, Unit: "percent"},
	}, nil
}

func (c *Collector) collectDisk() ([]store.MetricSample, error) {
	partitions, err := disk.Partitions(false)
	if err != nil {
		return nil, err
	}

	var samples []store.MetricSample
	seen := make(map[string]bool)
	for _, p := range partitions {
		if seen[p.Mountpoint] {
			continue
		}
		seen[p.Mountpoint] = true

		usage, err := disk.Usage(p.Mountpoint)
		if err != nil || usage.Total == 0 {
			continue
		}

		labels := map[string]string{"mount": p.Mountpoint}
		samples = append(samples,
			store.MetricSample{Name: "disk.total_bytes", Value: float64(usage.Total), Unit: "bytes", Labels: labels},
			store.MetricSample{Name: "disk.used_bytes", Value: float64(usage.Used), Unit: "bytes", Labels: labels},
			store.MetricSample{Name: "disk.usage_percent", Value: usage.UsedPercent, Unit: "percent", Labels: labels},
		)
	}
	return samples, nil
}

func (c *Collector) collectNetwork() ([]store.MetricSample, error) {
	counters, err := net.IOCounters(true)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	var samples []store.MetricSample

	if c.lastNetCounters != nil && !c.lastCollectTime.IsZero() {
		elapsed := now.Sub(c.lastCollectTime).Seconds()
		if elapsed > 0 {
			for _, counter := range counters {
				if prev, ok := c.lastNetCounters[counter.Name]; ok {
					labels := map[string]string{"interface": counter.Name}
					bytesSentPerSec := float64(counter.BytesSent-prev.BytesSent) / elapsed
					bytesRecvPerSec := float64(counter.BytesRecv-prev.BytesRecv) / elapsed
					samples = append(samples,
						store.MetricSample{Name: "network.bytes_sent_per_sec", Value: bytesSentPerSec, Unit: "bytes/s", Labels: labels},
						store.MetricSample{Name: "network.bytes_recv_per_sec", Value: bytesRecvPerSec, Unit: "bytes/s", Labels: labels},
					)
				}
			}
		}
	}

	// Store current counters for next delta calculation
	c.lastNetCounters = make(map[string]net.IOCountersStat)
	for _, counter := range counters {
		c.lastNetCounters[counter.Name] = counter
	}
	c.lastCollectTime = now

	return samples, nil
}

func (c *Collector) collectLoad() ([]store.MetricSample, error) {
	avg, err := load.Avg()
	if err != nil {
		return nil, err
	}
	return []store.MetricSample{
		{Name: "load.1m", Value: avg.Load1},
		{Name: "load.5m", Value: avg.Load5},
		{Name: "load.15m", Value: avg.Load15},
	}, nil
}

func (c *Collector) collectProcessCount() ([]store.MetricSample, error) {
	pids, err := process.Pids()
	if err != nil {
		return nil, err
	}
	return []store.MetricSample{
		{Name: "process.count", Value: float64(len(pids))},
	}, nil
}

func (c *Collector) collectUptime() ([]store.MetricSample, error) {
	uptime, err := host.Uptime()
	if err != nil {
		return nil, err
	}
	return []store.MetricSample{
		{Name: "uptime.seconds", Value: float64(uptime), Unit: "seconds"},
	}, nil
}
