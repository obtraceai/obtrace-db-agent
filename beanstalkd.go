package main

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

type BeanstalkdCollector struct {
	addr     string
	name     string
	instance string
}

func NewBeanstalkdCollector(dsn, name, instance string) (*BeanstalkdCollector, error) {
	conn, err := net.DialTimeout("tcp", dsn, 3*time.Second)
	if err != nil {
		return nil, fmt.Errorf("connect to beanstalkd %s: %w", dsn, err)
	}
	conn.Close()

	return &BeanstalkdCollector{
		addr:     dsn,
		name:     name,
		instance: instance,
	}, nil
}

func (b *BeanstalkdCollector) Type() string { return "beanstalkd" }
func (b *BeanstalkdCollector) Close() error { return nil }

func (b *BeanstalkdCollector) baseAttrs() map[string]string {
	return map[string]string{
		"messaging.system":   "beanstalkd",
		"messaging.instance": b.instance,
		"messaging.name":     b.name,
	}
}

func (b *BeanstalkdCollector) Collect(ctx context.Context) ([]Metric, error) {
	conn, err := net.DialTimeout("tcp", b.addr, 3*time.Second)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))

	var metrics []Metric

	stats, err := b.command(conn, "stats")
	if err != nil {
		return nil, fmt.Errorf("stats command: %w", err)
	}

	attrs := b.baseAttrs()
	addGauge := func(metric, key, unit string) {
		if v, ok := stats[key]; ok {
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				metrics = append(metrics, Metric{Name: metric, Value: f, Unit: unit, Attributes: attrs})
			}
		}
	}

	addGauge("messaging.beanstalkd.jobs.ready", "current-jobs-ready", "{jobs}")
	addGauge("messaging.beanstalkd.jobs.reserved", "current-jobs-reserved", "{jobs}")
	addGauge("messaging.beanstalkd.jobs.delayed", "current-jobs-delayed", "{jobs}")
	addGauge("messaging.beanstalkd.jobs.buried", "current-jobs-buried", "{jobs}")
	addGauge("messaging.beanstalkd.jobs.urgent", "current-jobs-urgent", "{jobs}")
	addGauge("messaging.beanstalkd.jobs.total", "total-jobs", "{jobs}")
	addGauge("messaging.beanstalkd.connections.current", "current-connections", "{connections}")
	addGauge("messaging.beanstalkd.connections.producers", "current-producers", "{connections}")
	addGauge("messaging.beanstalkd.connections.workers", "current-workers", "{connections}")
	addGauge("messaging.beanstalkd.connections.waiting", "current-waiting", "{connections}")
	addGauge("messaging.beanstalkd.tubes.count", "current-tubes", "{tubes}")
	addGauge("messaging.beanstalkd.cmd.put", "cmd-put", "{commands}")
	addGauge("messaging.beanstalkd.cmd.reserve", "cmd-reserve", "{commands}")
	addGauge("messaging.beanstalkd.cmd.delete", "cmd-delete", "{commands}")
	addGauge("messaging.beanstalkd.cmd.bury", "cmd-bury", "{commands}")
	addGauge("messaging.beanstalkd.cmd.kick", "cmd-kick", "{commands}")
	addGauge("messaging.beanstalkd.timeouts", "job-timeouts", "{timeouts}")

	tubes, err := b.listTubes(conn)
	if err == nil {
		for i, tube := range tubes {
			if i >= 50 {
				break
			}
			tubeStats, err := b.command(conn, "stats-tube "+tube)
			if err != nil {
				continue
			}

			tAttrs := b.baseAttrs()
			tAttrs["messaging.destination.name"] = tube

			tubeGauge := func(metric, key, unit string) {
				if v, ok := tubeStats[key]; ok {
					if f, err := strconv.ParseFloat(v, 64); err == nil {
						metrics = append(metrics, Metric{Name: metric, Value: f, Unit: unit, Attributes: tAttrs})
					}
				}
			}

			tubeGauge("messaging.beanstalkd.tube.ready", "current-jobs-ready", "{jobs}")
			tubeGauge("messaging.beanstalkd.tube.reserved", "current-jobs-reserved", "{jobs}")
			tubeGauge("messaging.beanstalkd.tube.delayed", "current-jobs-delayed", "{jobs}")
			tubeGauge("messaging.beanstalkd.tube.buried", "current-jobs-buried", "{jobs}")
			tubeGauge("messaging.beanstalkd.tube.waiting", "current-waiting", "{consumers}")
			tubeGauge("messaging.beanstalkd.tube.total_jobs", "total-jobs", "{jobs}")
		}
	}

	return metrics, nil
}

func (b *BeanstalkdCollector) command(conn net.Conn, cmd string) (map[string]string, error) {
	fmt.Fprintf(conn, "%s\r\n", cmd)

	reader := bufio.NewReader(conn)
	line, err := reader.ReadString('\n')
	if err != nil {
		return nil, err
	}
	line = strings.TrimSpace(line)

	if !strings.HasPrefix(line, "OK") {
		return nil, fmt.Errorf("unexpected response: %s", line)
	}

	result := map[string]string{}
	for {
		yamlLine, err := reader.ReadString('\n')
		if err != nil {
			break
		}
		yamlLine = strings.TrimSpace(yamlLine)
		if yamlLine == "" {
			break
		}
		if strings.HasPrefix(yamlLine, "---") {
			continue
		}
		parts := strings.SplitN(yamlLine, ": ", 2)
		if len(parts) == 2 {
			result[parts[0]] = parts[1]
		}
	}

	return result, nil
}

func (b *BeanstalkdCollector) listTubes(conn net.Conn) ([]string, error) {
	fmt.Fprintf(conn, "list-tubes\r\n")

	reader := bufio.NewReader(conn)
	line, err := reader.ReadString('\n')
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(strings.TrimSpace(line), "OK") {
		return nil, fmt.Errorf("unexpected response: %s", line)
	}

	var tubes []string
	for {
		yamlLine, err := reader.ReadString('\n')
		if err != nil {
			break
		}
		yamlLine = strings.TrimSpace(yamlLine)
		if yamlLine == "" {
			break
		}
		if strings.HasPrefix(yamlLine, "---") {
			continue
		}
		if strings.HasPrefix(yamlLine, "- ") {
			tubes = append(tubes, strings.TrimPrefix(yamlLine, "- "))
		}
	}

	return tubes, nil
}
