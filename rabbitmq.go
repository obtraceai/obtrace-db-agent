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

type RabbitMQCollector struct {
	baseURL  string
	name     string
	instance string
	client   *http.Client
	username string
	password string
}

func NewRabbitMQCollector(dsn, name, instance string) (*RabbitMQCollector, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse rabbitmq DSN: %w", err)
	}

	var username, password string
	if u.User != nil {
		username = u.User.Username()
		password, _ = u.User.Password()
	}
	if username == "" {
		username = "guest"
		password = "guest"
	}

	baseURL := fmt.Sprintf("%s://%s/api", u.Scheme, u.Host)

	client := &http.Client{Timeout: 5 * time.Second}
	req, _ := http.NewRequest(http.MethodGet, baseURL+"/overview", nil)
	req.SetBasicAuth(username, password)
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("connect to rabbitmq management API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return nil, fmt.Errorf("rabbitmq auth failed (HTTP %d) — check credentials", resp.StatusCode)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("rabbitmq management API returned HTTP %d", resp.StatusCode)
	}

	return &RabbitMQCollector{
		baseURL:  baseURL,
		name:     name,
		instance: instance,
		client:   client,
		username: username,
		password: password,
	}, nil
}

func (r *RabbitMQCollector) Type() string { return "rabbitmq" }
func (r *RabbitMQCollector) Close() error { return nil }

func (r *RabbitMQCollector) baseAttrs() map[string]string {
	return map[string]string{
		"messaging.system":   "rabbitmq",
		"messaging.instance": r.instance,
		"messaging.name":     r.name,
	}
}

func (r *RabbitMQCollector) fetchJSON(ctx context.Context, path string, out interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.baseURL+path, nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth(r.username, r.password)
	req.Header.Set("Accept", "application/json")

	resp, err := r.client.Do(req)
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

func (r *RabbitMQCollector) Collect(ctx context.Context) ([]Metric, error) {
	var metrics []Metric

	m, err := r.collectOverview(ctx)
	if err == nil {
		metrics = append(metrics, m...)
	}

	m, err = r.collectQueues(ctx)
	if err == nil {
		metrics = append(metrics, m...)
	}

	m, err = r.collectNodes(ctx)
	if err == nil {
		metrics = append(metrics, m...)
	}

	if len(metrics) == 0 {
		return nil, fmt.Errorf("no rabbitmq metrics collected")
	}

	return metrics, nil
}

func (r *RabbitMQCollector) collectOverview(ctx context.Context) ([]Metric, error) {
	var resp struct {
		MessageStats struct {
			PublishDetails struct {
				Rate float64 `json:"rate"`
			} `json:"publish_details"`
			DeliverGetDetails struct {
				Rate float64 `json:"rate"`
			} `json:"deliver_get_details"`
			AckDetails struct {
				Rate float64 `json:"rate"`
			} `json:"ack_details"`
			Publish    int64 `json:"publish"`
			DeliverGet int64 `json:"deliver_get"`
			Ack        int64 `json:"ack"`
			Redeliver  int64 `json:"redeliver"`
		} `json:"message_stats"`
		QueueTotals struct {
			Messages        int64 `json:"messages"`
			MessagesReady   int64 `json:"messages_ready"`
			MessagesUnacked int64 `json:"messages_unacknowledged"`
		} `json:"queue_totals"`
		ObjectTotals struct {
			Connections int64 `json:"connections"`
			Channels    int64 `json:"channels"`
			Queues      int64 `json:"queues"`
			Consumers   int64 `json:"consumers"`
			Exchanges   int64 `json:"exchanges"`
		} `json:"object_totals"`
		ClusterName     string `json:"cluster_name"`
		RabbitmqVersion string `json:"rabbitmq_version"`
	}

	if err := r.fetchJSON(ctx, "/overview", &resp); err != nil {
		return nil, err
	}

	attrs := r.baseAttrs()
	attrs["messaging.rabbitmq.version"] = resp.RabbitmqVersion

	return []Metric{
		{Name: "messaging.rabbitmq.connections", Value: float64(resp.ObjectTotals.Connections), Unit: "{connections}", Attributes: attrs},
		{Name: "messaging.rabbitmq.channels", Value: float64(resp.ObjectTotals.Channels), Unit: "{channels}", Attributes: attrs},
		{Name: "messaging.rabbitmq.queues.count", Value: float64(resp.ObjectTotals.Queues), Unit: "{queues}", Attributes: attrs},
		{Name: "messaging.rabbitmq.consumers", Value: float64(resp.ObjectTotals.Consumers), Unit: "{consumers}", Attributes: attrs},
		{Name: "messaging.rabbitmq.exchanges", Value: float64(resp.ObjectTotals.Exchanges), Unit: "{exchanges}", Attributes: attrs},
		{Name: "messaging.rabbitmq.messages.total", Value: float64(resp.QueueTotals.Messages), Unit: "{messages}", Attributes: attrs},
		{Name: "messaging.rabbitmq.messages.ready", Value: float64(resp.QueueTotals.MessagesReady), Unit: "{messages}", Attributes: attrs},
		{Name: "messaging.rabbitmq.messages.unacked", Value: float64(resp.QueueTotals.MessagesUnacked), Unit: "{messages}", Attributes: attrs},
		{Name: "messaging.rabbitmq.publish.rate", Value: resp.MessageStats.PublishDetails.Rate, Unit: "{messages}/s", Attributes: attrs},
		{Name: "messaging.rabbitmq.deliver.rate", Value: resp.MessageStats.DeliverGetDetails.Rate, Unit: "{messages}/s", Attributes: attrs},
		{Name: "messaging.rabbitmq.ack.rate", Value: resp.MessageStats.AckDetails.Rate, Unit: "{messages}/s", Attributes: attrs},
		{Name: "messaging.rabbitmq.publish.total", Value: float64(resp.MessageStats.Publish), Unit: "{messages}", Attributes: attrs},
		{Name: "messaging.rabbitmq.redeliver.total", Value: float64(resp.MessageStats.Redeliver), Unit: "{messages}", Attributes: attrs},
	}, nil
}

func (r *RabbitMQCollector) collectQueues(ctx context.Context) ([]Metric, error) {
	var queues []struct {
		Name     string `json:"name"`
		VHost    string `json:"vhost"`
		Messages int64  `json:"messages"`
		Ready    int64  `json:"messages_ready"`
		Unacked  int64  `json:"messages_unacknowledged"`
		Consumers int64 `json:"consumers"`
		Memory   int64  `json:"memory"`
		MessageStats struct {
			PublishDetails struct {
				Rate float64 `json:"rate"`
			} `json:"publish_details"`
			DeliverGetDetails struct {
				Rate float64 `json:"rate"`
			} `json:"deliver_get_details"`
		} `json:"message_stats"`
		State string `json:"state"`
	}

	if err := r.fetchJSON(ctx, "/queues?columns=name,vhost,messages,messages_ready,messages_unacknowledged,consumers,memory,message_stats,state", &queues); err != nil {
		return nil, err
	}

	var metrics []Metric
	for i, q := range queues {
		if i >= 100 {
			break
		}
		attrs := r.baseAttrs()
		attrs["messaging.destination.name"] = q.Name
		attrs["messaging.rabbitmq.vhost"] = q.VHost
		attrs["messaging.rabbitmq.queue.state"] = q.State

		metrics = append(metrics,
			Metric{Name: "messaging.rabbitmq.queue.messages", Value: float64(q.Messages), Unit: "{messages}", Attributes: attrs},
			Metric{Name: "messaging.rabbitmq.queue.ready", Value: float64(q.Ready), Unit: "{messages}", Attributes: attrs},
			Metric{Name: "messaging.rabbitmq.queue.unacked", Value: float64(q.Unacked), Unit: "{messages}", Attributes: attrs},
			Metric{Name: "messaging.rabbitmq.queue.consumers", Value: float64(q.Consumers), Unit: "{consumers}", Attributes: attrs},
			Metric{Name: "messaging.rabbitmq.queue.memory", Value: float64(q.Memory), Unit: "By", Attributes: attrs},
			Metric{Name: "messaging.rabbitmq.queue.publish_rate", Value: q.MessageStats.PublishDetails.Rate, Unit: "{messages}/s", Attributes: attrs},
			Metric{Name: "messaging.rabbitmq.queue.deliver_rate", Value: q.MessageStats.DeliverGetDetails.Rate, Unit: "{messages}/s", Attributes: attrs},
		)
	}

	return metrics, nil
}

func (r *RabbitMQCollector) collectNodes(ctx context.Context) ([]Metric, error) {
	var nodes []struct {
		Name         string `json:"name"`
		Running      bool   `json:"running"`
		MemUsed      int64  `json:"mem_used"`
		MemLimit     int64  `json:"mem_limit"`
		MemAlarm     bool   `json:"mem_alarm"`
		DiskFree     int64  `json:"disk_free"`
		DiskLimit    int64  `json:"disk_free_limit"`
		DiskAlarm    bool   `json:"disk_free_alarm"`
		FdUsed       int64  `json:"fd_used"`
		FdTotal      int64  `json:"fd_total"`
		SocketsUsed  int64  `json:"sockets_used"`
		SocketsTotal int64  `json:"sockets_total"`
		Uptime       int64  `json:"uptime"`
		Processors   int64  `json:"processors"`
	}

	if err := r.fetchJSON(ctx, "/nodes", &nodes); err != nil {
		return nil, err
	}

	var metrics []Metric
	for _, n := range nodes {
		attrs := r.baseAttrs()
		attrs["messaging.rabbitmq.node"] = n.Name

		running := 0.0
		if n.Running {
			running = 1
		}
		memAlarm := 0.0
		if n.MemAlarm {
			memAlarm = 1
		}
		diskAlarm := 0.0
		if n.DiskAlarm {
			diskAlarm = 1
		}

		metrics = append(metrics,
			Metric{Name: "messaging.rabbitmq.node.running", Value: running, Unit: "1", Attributes: attrs},
			Metric{Name: "messaging.rabbitmq.node.memory_used", Value: float64(n.MemUsed), Unit: "By", Attributes: attrs},
			Metric{Name: "messaging.rabbitmq.node.memory_limit", Value: float64(n.MemLimit), Unit: "By", Attributes: attrs},
			Metric{Name: "messaging.rabbitmq.node.memory_alarm", Value: memAlarm, Unit: "1", Attributes: attrs},
			Metric{Name: "messaging.rabbitmq.node.disk_free", Value: float64(n.DiskFree), Unit: "By", Attributes: attrs},
			Metric{Name: "messaging.rabbitmq.node.disk_alarm", Value: diskAlarm, Unit: "1", Attributes: attrs},
			Metric{Name: "messaging.rabbitmq.node.fd_used", Value: float64(n.FdUsed), Unit: "{descriptors}", Attributes: attrs},
			Metric{Name: "messaging.rabbitmq.node.fd_total", Value: float64(n.FdTotal), Unit: "{descriptors}", Attributes: attrs},
			Metric{Name: "messaging.rabbitmq.node.sockets_used", Value: float64(n.SocketsUsed), Unit: "{sockets}", Attributes: attrs},
			Metric{Name: "messaging.rabbitmq.node.sockets_total", Value: float64(n.SocketsTotal), Unit: "{sockets}", Attributes: attrs},
			Metric{Name: "messaging.rabbitmq.node.uptime_ms", Value: float64(n.Uptime), Unit: "ms", Attributes: attrs},
		)
	}

	return metrics, nil
}
