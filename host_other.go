//go:build !linux

package main

import (
	"context"
	"fmt"
)

type HostCollector struct {
	name string
}

func NewHostCollector(name string) (*HostCollector, error) {
	return nil, fmt.Errorf("host metrics only supported on linux")
}

func (h *HostCollector) Type() string                                { return "host" }
func (h *HostCollector) Collect(_ context.Context) ([]Metric, error) { return nil, nil }
func (h *HostCollector) Close() error                                { return nil }
