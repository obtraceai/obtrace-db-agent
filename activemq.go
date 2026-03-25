package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type ActiveMQCollector struct {
	baseURL  string
	name     string
	instance string
	client   *http.Client
	username string
	password string
	variant  string
}

func NewActiveMQCollector(dsn, name, instance string) (*ActiveMQCollector, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse ActiveMQ DSN: %w", err)
	}

	var username, password string
	if u.User != nil {
		username = u.User.Username()
		password, _ = u.User.Password()
	}
	if username == "" {
		username = "admin"
		password = "admin"
	}

	baseURL := fmt.Sprintf("%s://%s", u.Scheme, u.Host)
	client := &http.Client{Timeout: 5 * time.Second}

	variant := "classic"
	req, _ := http.NewRequest(http.MethodGet, baseURL+"/console/jolokia/version", nil)
	req.SetBasicAuth(username, password)
	resp, err := client.Do(req)
	if err == nil {
		resp.Body.Close()
		if resp.StatusCode == 200 {
			variant = "artemis"
		}
	}

	if variant == "classic" {
		req, _ = http.NewRequest(http.MethodGet, baseURL+"/api/jolokia/version", nil)
		req.SetBasicAuth(username, password)
		resp, err = client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("connect to ActiveMQ Jolokia API: %w", err)
		}
		resp.Body.Close()
		if resp.StatusCode == 401 || resp.StatusCode == 403 {
			return nil, fmt.Errorf("ActiveMQ auth failed (HTTP %d)", resp.StatusCode)
		}
	}

	return &ActiveMQCollector{
		baseURL:  baseURL,
		name:     name,
		instance: instance,
		client:   client,
		username: username,
		password: password,
		variant:  variant,
	}, nil
}

func (a *ActiveMQCollector) Type() string { return "activemq" }
func (a *ActiveMQCollector) Close() error { return nil }

func (a *ActiveMQCollector) baseAttrs() map[string]string {
	return map[string]string{
		"messaging.system":           "activemq",
		"messaging.activemq.variant": a.variant,
		"messaging.instance":         a.instance,
		"messaging.name":             a.name,
	}
}

func (a *ActiveMQCollector) jolokiaGet(ctx context.Context, path string) (map[string]interface{}, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(a.username, a.password)
	req.Header.Set("Accept", "application/json")

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("GET %s: %d %s", path, resp.StatusCode, string(body))
	}

	var result struct {
		Value interface{} `json:"value"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	if m, ok := result.Value.(map[string]interface{}); ok {
		return m, nil
	}
	return nil, fmt.Errorf("unexpected jolokia response type")
}

func (a *ActiveMQCollector) Collect(ctx context.Context) ([]Metric, error) {
	if a.variant == "artemis" {
		return a.collectArtemis(ctx)
	}
	return a.collectClassic(ctx)
}

func (a *ActiveMQCollector) collectClassic(ctx context.Context) ([]Metric, error) {
	brokerPath := "/api/jolokia/read/org.apache.activemq:type=Broker,brokerName=*"
	broker, err := a.jolokiaGet(ctx, brokerPath)
	if err != nil {
		return nil, err
	}

	attrs := a.baseAttrs()
	var metrics []Metric

	if v, ok := toFloat(broker["TotalConnectionsCount"]); ok {
		metrics = append(metrics, Metric{Name: "messaging.activemq.connections", Value: v, Unit: "{connections}", Attributes: attrs})
	}
	if v, ok := toFloat(broker["TotalConsumerCount"]); ok {
		metrics = append(metrics, Metric{Name: "messaging.activemq.consumers", Value: v, Unit: "{consumers}", Attributes: attrs})
	}
	if v, ok := toFloat(broker["TotalProducerCount"]); ok {
		metrics = append(metrics, Metric{Name: "messaging.activemq.producers", Value: v, Unit: "{producers}", Attributes: attrs})
	}
	if v, ok := toFloat(broker["TotalEnqueueCount"]); ok {
		metrics = append(metrics, Metric{Name: "messaging.activemq.enqueue.total", Value: v, Unit: "{messages}", Attributes: attrs})
	}
	if v, ok := toFloat(broker["TotalDequeueCount"]); ok {
		metrics = append(metrics, Metric{Name: "messaging.activemq.dequeue.total", Value: v, Unit: "{messages}", Attributes: attrs})
	}
	if v, ok := toFloat(broker["StorePercentUsage"]); ok {
		metrics = append(metrics, Metric{Name: "messaging.activemq.store_percent", Value: v, Unit: "%", Attributes: attrs})
	}
	if v, ok := toFloat(broker["MemoryPercentUsage"]); ok {
		metrics = append(metrics, Metric{Name: "messaging.activemq.memory_percent", Value: v, Unit: "%", Attributes: attrs})
	}
	if v, ok := toFloat(broker["TempPercentUsage"]); ok {
		metrics = append(metrics, Metric{Name: "messaging.activemq.temp_percent", Value: v, Unit: "%", Attributes: attrs})
	}

	qPath := "/api/jolokia/read/org.apache.activemq:type=Broker,brokerName=*,destinationType=Queue,destinationName=*"
	queues, _ := a.jolokiaGet(ctx, qPath)
	for objectName, qData := range queues {
		qMap, ok := qData.(map[string]interface{})
		if !ok {
			continue
		}
		qName := extractJMXParam(objectName, "destinationName")
		if qName == "" {
			continue
		}

		qAttrs := a.baseAttrs()
		qAttrs["messaging.destination.name"] = qName

		if v, ok := toFloat(qMap["QueueSize"]); ok {
			metrics = append(metrics, Metric{Name: "messaging.activemq.queue.size", Value: v, Unit: "{messages}", Attributes: qAttrs})
		}
		if v, ok := toFloat(qMap["ConsumerCount"]); ok {
			metrics = append(metrics, Metric{Name: "messaging.activemq.queue.consumers", Value: v, Unit: "{consumers}", Attributes: qAttrs})
		}
		if v, ok := toFloat(qMap["EnqueueCount"]); ok {
			metrics = append(metrics, Metric{Name: "messaging.activemq.queue.enqueue", Value: v, Unit: "{messages}", Attributes: qAttrs})
		}
		if v, ok := toFloat(qMap["DequeueCount"]); ok {
			metrics = append(metrics, Metric{Name: "messaging.activemq.queue.dequeue", Value: v, Unit: "{messages}", Attributes: qAttrs})
		}
		if v, ok := toFloat(qMap["ExpiredCount"]); ok {
			metrics = append(metrics, Metric{Name: "messaging.activemq.queue.expired", Value: v, Unit: "{messages}", Attributes: qAttrs})
		}
	}

	return metrics, nil
}

func (a *ActiveMQCollector) collectArtemis(ctx context.Context) ([]Metric, error) {
	brokerPath := "/console/jolokia/read/org.apache.activemq.artemis:broker=%22*%22"
	broker, err := a.jolokiaGet(ctx, brokerPath)
	if err != nil {
		return nil, err
	}

	attrs := a.baseAttrs()
	var metrics []Metric

	if v, ok := toFloat(broker["ConnectionCount"]); ok {
		metrics = append(metrics, Metric{Name: "messaging.activemq.connections", Value: v, Unit: "{connections}", Attributes: attrs})
	}
	if v, ok := toFloat(broker["TotalConnectionCount"]); ok {
		metrics = append(metrics, Metric{Name: "messaging.activemq.connections.total", Value: v, Unit: "{connections}", Attributes: attrs})
	}
	if v, ok := toFloat(broker["TotalConsumerCount"]); ok {
		metrics = append(metrics, Metric{Name: "messaging.activemq.consumers", Value: v, Unit: "{consumers}", Attributes: attrs})
	}
	if v, ok := toFloat(broker["TotalMessageCount"]); ok {
		metrics = append(metrics, Metric{Name: "messaging.activemq.messages.total", Value: v, Unit: "{messages}", Attributes: attrs})
	}
	if v, ok := toFloat(broker["AddressMemoryUsage"]); ok {
		metrics = append(metrics, Metric{Name: "messaging.activemq.address_memory", Value: v, Unit: "By", Attributes: attrs})
	}
	if v, ok := toFloat(broker["GlobalMaxSize"]); ok {
		metrics = append(metrics, Metric{Name: "messaging.activemq.global_max_size", Value: v, Unit: "By", Attributes: attrs})
	}

	qPath := "/console/jolokia/read/org.apache.activemq.artemis:broker=%22*%22,component=addresses,address=%22*%22,subcomponent=queues,routing-type=%22anycast%22,queue=%22*%22"
	queues, _ := a.jolokiaGet(ctx, qPath)
	for objectName, qData := range queues {
		qMap, ok := qData.(map[string]interface{})
		if !ok {
			continue
		}
		qName := extractJMXParam(objectName, "queue")
		if qName == "" {
			continue
		}
		qName = strings.Trim(qName, "\"")

		qAttrs := a.baseAttrs()
		qAttrs["messaging.destination.name"] = qName

		if v, ok := toFloat(qMap["MessageCount"]); ok {
			metrics = append(metrics, Metric{Name: "messaging.activemq.queue.size", Value: v, Unit: "{messages}", Attributes: qAttrs})
		}
		if v, ok := toFloat(qMap["ConsumerCount"]); ok {
			metrics = append(metrics, Metric{Name: "messaging.activemq.queue.consumers", Value: v, Unit: "{consumers}", Attributes: qAttrs})
		}
		if v, ok := toFloat(qMap["MessagesAdded"]); ok {
			metrics = append(metrics, Metric{Name: "messaging.activemq.queue.enqueue", Value: v, Unit: "{messages}", Attributes: qAttrs})
		}
		if v, ok := toFloat(qMap["MessagesAcknowledged"]); ok {
			metrics = append(metrics, Metric{Name: "messaging.activemq.queue.dequeue", Value: v, Unit: "{messages}", Attributes: qAttrs})
		}
		if v, ok := toFloat(qMap["MessagesExpired"]); ok {
			metrics = append(metrics, Metric{Name: "messaging.activemq.queue.expired", Value: v, Unit: "{messages}", Attributes: qAttrs})
		}
		if v, ok := toFloat(qMap["DeliveringCount"]); ok {
			metrics = append(metrics, Metric{Name: "messaging.activemq.queue.delivering", Value: v, Unit: "{messages}", Attributes: qAttrs})
		}
	}

	return metrics, nil
}

func extractJMXParam(objectName, param string) string {
	for _, part := range strings.Split(objectName, ",") {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) == 2 && strings.TrimSpace(kv[0]) == param {
			return kv[1]
		}
	}
	return ""
}

func toFloat(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}
