package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

type IBMMQCollector struct {
	baseURL  string
	name     string
	instance string
	client   *http.Client
	username string
	password string
}

func NewIBMMQCollector(dsn, name, instance string) (*IBMMQCollector, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse IBM MQ DSN: %w", err)
	}

	var username, password string
	if u.User != nil {
		username = u.User.Username()
		password, _ = u.User.Password()
	}

	baseURL := fmt.Sprintf("%s://%s/ibmmq/rest/v2", u.Scheme, u.Host)

	client := &http.Client{Timeout: 5 * time.Second}
	req, _ := http.NewRequest(http.MethodGet, baseURL+"/admin/installation", nil)
	if username != "" {
		req.SetBasicAuth(username, password)
	}
	req.Header.Set("ibm-mq-rest-csrf-token", "blank")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("connect to IBM MQ REST API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return nil, fmt.Errorf("IBM MQ auth failed (HTTP %d)", resp.StatusCode)
	}

	return &IBMMQCollector{
		baseURL:  baseURL,
		name:     name,
		instance: instance,
		client:   client,
		username: username,
		password: password,
	}, nil
}

func (m *IBMMQCollector) Type() string { return "ibmmq" }
func (m *IBMMQCollector) Close() error { return nil }

func (m *IBMMQCollector) baseAttrs() map[string]string {
	return map[string]string{
		"messaging.system":   "ibmmq",
		"messaging.instance": m.instance,
		"messaging.name":     m.name,
	}
}

func (m *IBMMQCollector) fetchJSON(ctx context.Context, path string, out interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.baseURL+path, nil)
	if err != nil {
		return err
	}
	if m.username != "" {
		req.SetBasicAuth(m.username, m.password)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("ibm-mq-rest-csrf-token", "blank")

	resp, err := m.client.Do(req)
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

func (m *IBMMQCollector) Collect(ctx context.Context) ([]Metric, error) {
	var metrics []Metric

	qm, err := m.collectQueueManager(ctx)
	if err == nil {
		metrics = append(metrics, qm...)
	}

	qMetrics, err := m.collectQueues(ctx)
	if err == nil {
		metrics = append(metrics, qMetrics...)
	}

	ch, err := m.collectChannels(ctx)
	if err == nil {
		metrics = append(metrics, ch...)
	}

	if len(metrics) == 0 {
		return nil, fmt.Errorf("no IBM MQ metrics collected")
	}

	return metrics, nil
}

func (m *IBMMQCollector) collectQueueManager(ctx context.Context) ([]Metric, error) {
	var resp struct {
		QueueManager []struct {
			Name  string `json:"name"`
			State string `json:"state"`
			Status struct {
				ConnectionCount int64 `json:"connectionCount"`
			} `json:"status"`
		} `json:"queueManager"`
	}

	if err := m.fetchJSON(ctx, "/admin/qmgr?status=status.connectionCount", &resp); err != nil {
		return nil, err
	}

	var metrics []Metric
	for _, qm := range resp.QueueManager {
		attrs := m.baseAttrs()
		attrs["messaging.ibmmq.qmgr"] = qm.Name
		attrs["messaging.ibmmq.qmgr.state"] = qm.State

		running := 0.0
		if qm.State == "running" {
			running = 1
		}

		metrics = append(metrics,
			Metric{Name: "messaging.ibmmq.qmgr.running", Value: running, Unit: "1", Attributes: attrs},
			Metric{Name: "messaging.ibmmq.qmgr.connections", Value: float64(qm.Status.ConnectionCount), Unit: "{connections}", Attributes: attrs},
		)
	}

	return metrics, nil
}

func (m *IBMMQCollector) collectQueues(ctx context.Context) ([]Metric, error) {
	var resp struct {
		Queue []struct {
			Name   string `json:"name"`
			Type   string `json:"type"`
			Status struct {
				CurrentDepth       int64 `json:"currentDepth"`
				OpenInputCount     int64 `json:"openInputCount"`
				OpenOutputCount    int64 `json:"openOutputCount"`
				UncommittedMessages int64 `json:"uncommittedMessages"`
				OldestMessageAge   int64 `json:"oldestMessageAge"`
				LastGetTime        string `json:"lastGetTime"`
				LastPutTime        string `json:"lastPutTime"`
			} `json:"status"`
		} `json:"queue"`
	}

	if err := m.fetchJSON(ctx, "/admin/queue?status=*", &resp); err != nil {
		return nil, err
	}

	var metrics []Metric
	totalDepth := int64(0)

	for i, q := range resp.Queue {
		if i >= 100 {
			break
		}

		totalDepth += q.Status.CurrentDepth

		attrs := m.baseAttrs()
		attrs["messaging.destination.name"] = q.Name
		attrs["messaging.ibmmq.queue.type"] = q.Type

		metrics = append(metrics,
			Metric{Name: "messaging.ibmmq.queue.depth", Value: float64(q.Status.CurrentDepth), Unit: "{messages}", Attributes: attrs},
			Metric{Name: "messaging.ibmmq.queue.open_input", Value: float64(q.Status.OpenInputCount), Unit: "{handles}", Attributes: attrs},
			Metric{Name: "messaging.ibmmq.queue.open_output", Value: float64(q.Status.OpenOutputCount), Unit: "{handles}", Attributes: attrs},
			Metric{Name: "messaging.ibmmq.queue.uncommitted", Value: float64(q.Status.UncommittedMessages), Unit: "{messages}", Attributes: attrs},
			Metric{Name: "messaging.ibmmq.queue.oldest_msg_age", Value: float64(q.Status.OldestMessageAge), Unit: "s", Attributes: attrs},
		)
	}

	attrs := m.baseAttrs()
	metrics = append(metrics,
		Metric{Name: "messaging.ibmmq.queues.count", Value: float64(len(resp.Queue)), Unit: "{queues}", Attributes: attrs},
		Metric{Name: "messaging.ibmmq.queues.total_depth", Value: float64(totalDepth), Unit: "{messages}", Attributes: attrs},
	)

	return metrics, nil
}

func (m *IBMMQCollector) collectChannels(ctx context.Context) ([]Metric, error) {
	var resp struct {
		Channel []struct {
			Name   string `json:"name"`
			Type   string `json:"type"`
			Status struct {
				General struct {
					ConnectionName string `json:"connectionName"`
				} `json:"general"`
				State     string `json:"state"`
				Messages  int64  `json:"msgs"`
				BytesSent int64  `json:"bytesSent"`
				BytesRcvd int64  `json:"bytesReceived"`
			} `json:"status"`
		} `json:"channel"`
	}

	if err := m.fetchJSON(ctx, "/admin/channel?status=*", &resp); err != nil {
		return nil, err
	}

	var metrics []Metric
	stateCount := map[string]int{}

	for _, ch := range resp.Channel {
		stateCount[ch.Status.State]++

		attrs := m.baseAttrs()
		attrs["messaging.ibmmq.channel"] = ch.Name
		attrs["messaging.ibmmq.channel.type"] = ch.Type
		attrs["messaging.ibmmq.channel.state"] = ch.Status.State

		metrics = append(metrics,
			Metric{Name: "messaging.ibmmq.channel.messages", Value: float64(ch.Status.Messages), Unit: "{messages}", Attributes: attrs},
			Metric{Name: "messaging.ibmmq.channel.bytes_sent", Value: float64(ch.Status.BytesSent), Unit: "By", Attributes: attrs},
			Metric{Name: "messaging.ibmmq.channel.bytes_received", Value: float64(ch.Status.BytesRcvd), Unit: "By", Attributes: attrs},
		)
	}

	attrs := m.baseAttrs()
	metrics = append(metrics, Metric{
		Name: "messaging.ibmmq.channels.count", Value: float64(len(resp.Channel)), Unit: "{channels}", Attributes: attrs,
	})

	for state, count := range stateCount {
		stateAttrs := m.baseAttrs()
		stateAttrs["messaging.ibmmq.channel.state"] = state
		metrics = append(metrics, Metric{
			Name: "messaging.ibmmq.channels.by_state", Value: float64(count), Unit: "{channels}", Attributes: stateAttrs,
		})
	}

	return metrics, nil
}
