package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
)

func sendMetricsWithRetry(ctx context.Context, ingestURL, apiKey string, resourceAttrs map[string]string, metrics []Metric, maxRetries int) error {
	var lastErr error

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(attempt) * time.Second
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
			log.Printf("INFO: retrying send (attempt %d/%d)", attempt+1, maxRetries+1)
		}

		err := sendMetrics(ctx, ingestURL, apiKey, resourceAttrs, metrics)
		if err == nil {
			return nil
		}
		lastErr = err
	}

	return lastErr
}

func sendMetrics(ctx context.Context, ingestURL, apiKey string, resourceAttrs map[string]string, metrics []Metric) error {
	payload := buildOTLPMetricsPayload(resourceAttrs, metrics)

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal OTLP payload: %w", err)
	}

	url := ingestURL + "/otlp/v1/metrics"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("POST %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 500 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("POST %s returned %d: %s (retryable)", url, resp.StatusCode, string(respBody))
	}

	if resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("POST %s returned %d: %s", url, resp.StatusCode, string(respBody))
	}

	return nil
}

func buildOTLPMetricsPayload(resourceAttrs map[string]string, metrics []Metric) map[string]interface{} {
	var resAttrs []map[string]interface{}
	for k, v := range resourceAttrs {
		resAttrs = append(resAttrs, map[string]interface{}{
			"key":   k,
			"value": map[string]interface{}{"stringValue": v},
		})
	}

	nowNanos := time.Now().UnixNano()
	var otlpMetrics []map[string]interface{}

	for _, m := range metrics {
		var mAttrs []map[string]interface{}
		for k, v := range m.Attributes {
			if len(v) > 256 {
				v = v[:256]
			}
			mAttrs = append(mAttrs, map[string]interface{}{
				"key":   k,
				"value": map[string]interface{}{"stringValue": v},
			})
		}

		dataPoint := map[string]interface{}{
			"timeUnixNano": fmt.Sprintf("%d", nowNanos),
			"asDouble":     m.Value,
			"attributes":   mAttrs,
		}

		otlpMetric := map[string]interface{}{
			"name": m.Name,
			"unit": m.Unit,
			"gauge": map[string]interface{}{
				"dataPoints": []map[string]interface{}{dataPoint},
			},
		}

		otlpMetrics = append(otlpMetrics, otlpMetric)
	}

	return map[string]interface{}{
		"resourceMetrics": []map[string]interface{}{
			{
				"resource": map[string]interface{}{
					"attributes": resAttrs,
				},
				"scopeMetrics": []map[string]interface{}{
					{
						"scope": map[string]interface{}{
							"name":    "obtrace-infra-agent",
							"version": "2.0.0",
						},
						"metrics": otlpMetrics,
					},
				},
			},
		},
	}
}
