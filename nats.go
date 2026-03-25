package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type NATSCollector struct {
	baseURL  string
	name     string
	instance string
	client   *http.Client
}

func NewNATSCollector(dsn, name, instance string) (*NATSCollector, error) {
	client := &http.Client{Timeout: 5 * time.Second}

	resp, err := client.Get(dsn + "/varz")
	if err != nil {
		return nil, fmt.Errorf("connect to NATS monitoring: %w", err)
	}
	resp.Body.Close()

	return &NATSCollector{
		baseURL:  dsn,
		name:     name,
		instance: instance,
		client:   client,
	}, nil
}

func (n *NATSCollector) Type() string { return "nats" }
func (n *NATSCollector) Close() error { return nil }

func (n *NATSCollector) baseAttrs() map[string]string {
	return map[string]string{
		"messaging.system":   "nats",
		"messaging.instance": n.instance,
		"messaging.name":     n.name,
	}
}

func (n *NATSCollector) fetchJSON(ctx context.Context, path string, out interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, n.baseURL+path, nil)
	if err != nil {
		return err
	}

	resp, err := n.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("GET %s: %d %s", path, resp.StatusCode, string(body))
	}

	return json.NewDecoder(resp.Body).Decode(out)
}

func (n *NATSCollector) Collect(ctx context.Context) ([]Metric, error) {
	var metrics []Metric

	m, err := n.collectVarz(ctx)
	if err == nil {
		metrics = append(metrics, m...)
	}

	m, err = n.collectJsz(ctx)
	if err == nil {
		metrics = append(metrics, m...)
	}

	if len(metrics) == 0 {
		return nil, fmt.Errorf("no NATS metrics collected")
	}

	return metrics, nil
}

func (n *NATSCollector) collectVarz(ctx context.Context) ([]Metric, error) {
	var resp struct {
		ServerID    string `json:"server_id"`
		Version     string `json:"version"`
		Connections int64  `json:"connections"`
		TotalConnections int64 `json:"total_connections"`
		Routes      int64  `json:"routes"`
		Remotes     int64  `json:"remotes"`
		InMsgs      int64  `json:"in_msgs"`
		OutMsgs     int64  `json:"out_msgs"`
		InBytes     int64  `json:"in_bytes"`
		OutBytes    int64  `json:"out_bytes"`
		SlowConsumers int64 `json:"slow_consumers"`
		Subscriptions uint32 `json:"subscriptions"`
		Mem         int64  `json:"mem"`
		Cores       int    `json:"cores"`
		CPU         float64 `json:"cpu"`
		MaxPayload  int64  `json:"max_payload"`
		Uptime      string `json:"uptime"`
	}

	if err := n.fetchJSON(ctx, "/varz", &resp); err != nil {
		return nil, err
	}

	attrs := n.baseAttrs()
	attrs["messaging.nats.version"] = resp.Version
	attrs["messaging.nats.server_id"] = resp.ServerID

	return []Metric{
		{Name: "messaging.nats.connections.current", Value: float64(resp.Connections), Unit: "{connections}", Attributes: attrs},
		{Name: "messaging.nats.connections.total", Value: float64(resp.TotalConnections), Unit: "{connections}", Attributes: attrs},
		{Name: "messaging.nats.routes", Value: float64(resp.Routes), Unit: "{routes}", Attributes: attrs},
		{Name: "messaging.nats.remotes", Value: float64(resp.Remotes), Unit: "{remotes}", Attributes: attrs},
		{Name: "messaging.nats.messages.in", Value: float64(resp.InMsgs), Unit: "{messages}", Attributes: attrs},
		{Name: "messaging.nats.messages.out", Value: float64(resp.OutMsgs), Unit: "{messages}", Attributes: attrs},
		{Name: "messaging.nats.bytes.in", Value: float64(resp.InBytes), Unit: "By", Attributes: attrs},
		{Name: "messaging.nats.bytes.out", Value: float64(resp.OutBytes), Unit: "By", Attributes: attrs},
		{Name: "messaging.nats.slow_consumers", Value: float64(resp.SlowConsumers), Unit: "{consumers}", Attributes: attrs},
		{Name: "messaging.nats.subscriptions", Value: float64(resp.Subscriptions), Unit: "{subscriptions}", Attributes: attrs},
		{Name: "messaging.nats.memory", Value: float64(resp.Mem), Unit: "By", Attributes: attrs},
		{Name: "messaging.nats.cpu", Value: resp.CPU, Unit: "%", Attributes: attrs},
	}, nil
}

func (n *NATSCollector) collectJsz(ctx context.Context) ([]Metric, error) {
	var resp struct {
		Memory  int64 `json:"memory"`
		Storage int64 `json:"storage"`
		Streams int   `json:"streams"`
		Consumers int `json:"consumers"`
		Messages  int64 `json:"messages"`
		Bytes     int64 `json:"bytes"`
		AccountDetails []struct {
			Name      string `json:"name"`
			Streams   []struct {
				Name      string `json:"name"`
				Messages  uint64 `json:"messages"`
				Bytes     uint64 `json:"bytes"`
				Consumers int    `json:"consumer_count"`
				State     struct {
					FirstSeq uint64 `json:"first_seq"`
					LastSeq  uint64 `json:"last_seq"`
				} `json:"state"`
			} `json:"stream_detail"`
		} `json:"account_details"`
	}

	if err := n.fetchJSON(ctx, "/jsz?streams=true&consumers=true", &resp); err != nil {
		return nil, err
	}

	attrs := n.baseAttrs()
	var metrics []Metric

	metrics = append(metrics,
		Metric{Name: "messaging.nats.jetstream.memory", Value: float64(resp.Memory), Unit: "By", Attributes: attrs},
		Metric{Name: "messaging.nats.jetstream.storage", Value: float64(resp.Storage), Unit: "By", Attributes: attrs},
		Metric{Name: "messaging.nats.jetstream.streams", Value: float64(resp.Streams), Unit: "{streams}", Attributes: attrs},
		Metric{Name: "messaging.nats.jetstream.consumers", Value: float64(resp.Consumers), Unit: "{consumers}", Attributes: attrs},
		Metric{Name: "messaging.nats.jetstream.messages", Value: float64(resp.Messages), Unit: "{messages}", Attributes: attrs},
		Metric{Name: "messaging.nats.jetstream.bytes", Value: float64(resp.Bytes), Unit: "By", Attributes: attrs},
	)

	for _, acct := range resp.AccountDetails {
		for _, s := range acct.Streams {
			streamAttrs := n.baseAttrs()
			streamAttrs["messaging.nats.jetstream.stream"] = s.Name
			streamAttrs["messaging.nats.jetstream.account"] = acct.Name

			metrics = append(metrics,
				Metric{Name: "messaging.nats.jetstream.stream.messages", Value: float64(s.Messages), Unit: "{messages}", Attributes: streamAttrs},
				Metric{Name: "messaging.nats.jetstream.stream.bytes", Value: float64(s.Bytes), Unit: "By", Attributes: streamAttrs},
				Metric{Name: "messaging.nats.jetstream.stream.consumers", Value: float64(s.Consumers), Unit: "{consumers}", Attributes: streamAttrs},
				Metric{Name: "messaging.nats.jetstream.stream.last_seq", Value: float64(s.State.LastSeq), Unit: "{sequence}", Attributes: streamAttrs},
			)
		}
	}

	return metrics, nil
}
