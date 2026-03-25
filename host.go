//go:build linux

package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
)

type HostCollector struct {
	name     string
	prevCPU  cpuTimes
	hasFirst bool
}

type cpuTimes struct {
	user, nice, system, idle, iowait, irq, softirq, steal float64
}

func NewHostCollector(name string) (*HostCollector, error) {
	return &HostCollector{name: name}, nil
}

func (h *HostCollector) Type() string { return "host" }
func (h *HostCollector) Close() error { return nil }

func (h *HostCollector) baseAttrs() map[string]string {
	hostname, _ := os.Hostname()
	return map[string]string{
		"host.name": hostname,
		"host.arch": runtime.GOARCH,
		"os.type":   runtime.GOOS,
	}
}

func (h *HostCollector) Collect(ctx context.Context) ([]Metric, error) {
	var metrics []Metric

	m, _ := h.collectCPU()
	metrics = append(metrics, m...)

	m, _ = h.collectMemory()
	metrics = append(metrics, m...)

	m, _ = h.collectDisk()
	metrics = append(metrics, m...)

	m, _ = h.collectNetwork()
	metrics = append(metrics, m...)

	m, _ = h.collectLoadAvg()
	metrics = append(metrics, m...)

	if len(metrics) == 0 {
		return nil, fmt.Errorf("no host metrics collected")
	}

	return metrics, nil
}

func (h *HostCollector) collectCPU() ([]Metric, error) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "cpu ") {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 8 {
			break
		}

		cur := cpuTimes{
			user:    parseF(fields[1]),
			nice:    parseF(fields[2]),
			system:  parseF(fields[3]),
			idle:    parseF(fields[4]),
			iowait:  parseF(fields[5]),
			irq:     parseF(fields[6]),
			softirq: parseF(fields[7]),
		}
		if len(fields) > 8 {
			cur.steal = parseF(fields[8])
		}

		attrs := h.baseAttrs()

		if !h.hasFirst {
			h.prevCPU = cur
			h.hasFirst = true
			return []Metric{
				{Name: "host.cpu.cores", Value: float64(runtime.NumCPU()), Unit: "{cores}", Attributes: attrs},
			}, nil
		}

		prev := h.prevCPU
		h.prevCPU = cur

		totalDelta := (cur.user - prev.user) + (cur.nice - prev.nice) + (cur.system - prev.system) +
			(cur.idle - prev.idle) + (cur.iowait - prev.iowait) + (cur.irq - prev.irq) +
			(cur.softirq - prev.softirq) + (cur.steal - prev.steal)

		if totalDelta == 0 {
			break
		}

		return []Metric{
			{Name: "host.cpu.cores", Value: float64(runtime.NumCPU()), Unit: "{cores}", Attributes: attrs},
			{Name: "host.cpu.utilization", Value: 100 * (1 - (cur.idle-prev.idle)/totalDelta), Unit: "%", Attributes: attrs},
			{Name: "host.cpu.user", Value: 100 * (cur.user - prev.user) / totalDelta, Unit: "%", Attributes: attrs},
			{Name: "host.cpu.system", Value: 100 * (cur.system - prev.system) / totalDelta, Unit: "%", Attributes: attrs},
			{Name: "host.cpu.iowait", Value: 100 * (cur.iowait - prev.iowait) / totalDelta, Unit: "%", Attributes: attrs},
			{Name: "host.cpu.steal", Value: 100 * (cur.steal - prev.steal) / totalDelta, Unit: "%", Attributes: attrs},
		}, nil
	}

	return nil, fmt.Errorf("no cpu line in /proc/stat")
}

func (h *HostCollector) collectMemory() ([]Metric, error) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return nil, err
	}
	defer f.Close()

	vals := map[string]float64{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		valStr := strings.TrimSpace(parts[1])
		valStr = strings.TrimSuffix(valStr, " kB")
		v, err := strconv.ParseFloat(strings.TrimSpace(valStr), 64)
		if err != nil {
			continue
		}
		vals[key] = v * 1024
	}

	attrs := h.baseAttrs()
	total := vals["MemTotal"]
	available := vals["MemAvailable"]
	buffers := vals["Buffers"]
	cached := vals["Cached"]
	swapTotal := vals["SwapTotal"]
	swapFree := vals["SwapFree"]

	var metrics []Metric
	metrics = append(metrics,
		Metric{Name: "host.memory.total", Value: total, Unit: "By", Attributes: attrs},
		Metric{Name: "host.memory.available", Value: available, Unit: "By", Attributes: attrs},
		Metric{Name: "host.memory.used", Value: total - available, Unit: "By", Attributes: attrs},
		Metric{Name: "host.memory.buffers", Value: buffers, Unit: "By", Attributes: attrs},
		Metric{Name: "host.memory.cached", Value: cached, Unit: "By", Attributes: attrs},
	)

	if total > 0 {
		metrics = append(metrics, Metric{
			Name: "host.memory.utilization", Value: 100 * (total - available) / total, Unit: "%", Attributes: attrs,
		})
	}

	if swapTotal > 0 {
		metrics = append(metrics,
			Metric{Name: "host.memory.swap_total", Value: swapTotal, Unit: "By", Attributes: attrs},
			Metric{Name: "host.memory.swap_used", Value: swapTotal - swapFree, Unit: "By", Attributes: attrs},
		)
	}

	return metrics, nil
}

func (h *HostCollector) collectDisk() ([]Metric, error) {
	f, err := os.Open("/proc/mounts")
	if err != nil {
		return nil, err
	}
	defer f.Close()

	attrs := h.baseAttrs()
	var metrics []Metric

	seen := map[string]bool{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 3 {
			continue
		}

		device := fields[0]
		mountpoint := fields[1]
		fstype := fields[2]

		if !strings.HasPrefix(device, "/dev/") {
			continue
		}
		if seen[device] {
			continue
		}
		if fstype == "squashfs" || fstype == "tmpfs" || fstype == "devtmpfs" {
			continue
		}

		seen[device] = true

		var stat syscallStatfs
		if err := statfs(mountpoint, &stat); err != nil {
			continue
		}

		total := stat.Blocks * uint64(stat.Bsize)
		free := stat.Bavail * uint64(stat.Bsize)
		used := total - (stat.Bfree * uint64(stat.Bsize))

		diskAttrs := copyAttrs(attrs)
		diskAttrs["host.disk.device"] = device
		diskAttrs["host.disk.mountpoint"] = mountpoint

		metrics = append(metrics,
			Metric{Name: "host.disk.total", Value: float64(total), Unit: "By", Attributes: diskAttrs},
			Metric{Name: "host.disk.used", Value: float64(used), Unit: "By", Attributes: diskAttrs},
			Metric{Name: "host.disk.free", Value: float64(free), Unit: "By", Attributes: diskAttrs},
		)
		if total > 0 {
			metrics = append(metrics, Metric{
				Name: "host.disk.utilization", Value: 100 * float64(used) / float64(total), Unit: "%", Attributes: diskAttrs,
			})
		}
	}

	return metrics, nil
}

func (h *HostCollector) collectNetwork() ([]Metric, error) {
	f, err := os.Open("/proc/net/dev")
	if err != nil {
		return nil, err
	}
	defer f.Close()

	attrs := h.baseAttrs()
	var metrics []Metric

	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		if lineNum <= 2 {
			continue
		}

		line := scanner.Text()
		colonIdx := strings.Index(line, ":")
		if colonIdx < 0 {
			continue
		}

		iface := strings.TrimSpace(line[:colonIdx])
		if iface == "lo" {
			continue
		}

		fields := strings.Fields(line[colonIdx+1:])
		if len(fields) < 12 {
			continue
		}

		netAttrs := copyAttrs(attrs)
		netAttrs["host.network.interface"] = iface

		metrics = append(metrics,
			Metric{Name: "host.network.receive_bytes", Value: parseF(fields[0]), Unit: "By", Attributes: netAttrs},
			Metric{Name: "host.network.receive_packets", Value: parseF(fields[1]), Unit: "{packets}", Attributes: netAttrs},
			Metric{Name: "host.network.receive_errors", Value: parseF(fields[2]), Unit: "{errors}", Attributes: netAttrs},
			Metric{Name: "host.network.receive_drops", Value: parseF(fields[3]), Unit: "{drops}", Attributes: netAttrs},
			Metric{Name: "host.network.transmit_bytes", Value: parseF(fields[8]), Unit: "By", Attributes: netAttrs},
			Metric{Name: "host.network.transmit_packets", Value: parseF(fields[9]), Unit: "{packets}", Attributes: netAttrs},
			Metric{Name: "host.network.transmit_errors", Value: parseF(fields[10]), Unit: "{errors}", Attributes: netAttrs},
			Metric{Name: "host.network.transmit_drops", Value: parseF(fields[11]), Unit: "{drops}", Attributes: netAttrs},
		)
	}

	return metrics, nil
}

func (h *HostCollector) collectLoadAvg() ([]Metric, error) {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return nil, err
	}

	fields := strings.Fields(string(data))
	if len(fields) < 3 {
		return nil, fmt.Errorf("unexpected /proc/loadavg format")
	}

	attrs := h.baseAttrs()
	return []Metric{
		{Name: "host.load.1m", Value: parseF(fields[0]), Unit: "1", Attributes: attrs},
		{Name: "host.load.5m", Value: parseF(fields[1]), Unit: "1", Attributes: attrs},
		{Name: "host.load.15m", Value: parseF(fields[2]), Unit: "1", Attributes: attrs},
	}, nil
}

