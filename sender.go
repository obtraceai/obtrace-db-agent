package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// sendMetrics builds an OTLP JSON metrics payload and POSTs it to the ingest-edge.
func sendMetrics(ctx context.Context, ingestURL, apiKey string, resourceAttrs map[string]string, metrics []DBMetric) error {
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

	if resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("POST %s returned %d: %s", url, resp.StatusCode, string(respBody))
	}

	return nil
}

// buildOTLPMetricsPayload constructs the OTLP JSON structure for metrics.
func buildOTLPMetricsPayload(resourceAttrs map[string]string, metrics []DBMetric) map[string]interface{} {
	// Build resource attributes
	var resAttrs []map[string]interface{}
	for k, v := range resourceAttrs {
		resAttrs = append(resAttrs, map[string]interface{}{
			"key":   k,
			"value": map[string]interface{}{"stringValue": v},
		})
	}

	nowNanos := time.Now().UnixNano()

	// Group metrics by their attributes (so metrics from the same source share a scope)
	type scopeKey struct{}
	var otlpMetrics []map[string]interface{}

	for _, m := range metrics {
		// Build metric attributes
		var mAttrs []map[string]interface{}
		for k, v := range m.Attributes {
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
							"name":    "obtrace-db-agent",
							"version": "1.0.0",
						},
						"metrics": otlpMetrics,
					},
				},
			},
		},
	}
}
