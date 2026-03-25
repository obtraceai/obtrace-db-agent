package main

import (
	"context"
	"log"
	"sync"
	"time"
)

type CollectorState struct {
	collector       Collector
	consecutiveFail int
	circuitOpenUntil time.Time
	lastSuccess     time.Time
	lastError       string
}

type ResilientOrchestrator struct {
	states          []*CollectorState
	perCollectorTimeout time.Duration
	maxRetries      int
	circuitThreshold int
	circuitCooldown  time.Duration
	mu              sync.RWMutex
}

func NewResilientOrchestrator(collectors []Collector) *ResilientOrchestrator {
	states := make([]*CollectorState, len(collectors))
	for i, c := range collectors {
		states[i] = &CollectorState{collector: c}
	}
	return &ResilientOrchestrator{
		states:              states,
		perCollectorTimeout: 8 * time.Second,
		maxRetries:          2,
		circuitThreshold:    5,
		circuitCooldown:     5 * time.Minute,
	}
}

func (r *ResilientOrchestrator) CollectAll(ctx context.Context) []Metric {
	var allMetrics []Metric
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, state := range r.states {
		if r.isCircuitOpen(state) {
			log.Printf("WARN: collector %s circuit open, skipping until %s", state.collector.Type(), state.circuitOpenUntil.Format(time.RFC3339))
			continue
		}

		wg.Add(1)
		go func(s *CollectorState) {
			defer wg.Done()

			metrics, err := r.collectWithRetry(ctx, s)
			if err != nil {
				r.recordFailure(s, err)
				return
			}

			r.recordSuccess(s)
			mu.Lock()
			allMetrics = append(allMetrics, metrics...)
			mu.Unlock()
		}(state)
	}

	wg.Wait()
	return allMetrics
}

func (r *ResilientOrchestrator) collectWithRetry(ctx context.Context, state *CollectorState) ([]Metric, error) {
	var lastErr error

	for attempt := 0; attempt <= r.maxRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(attempt*attempt) * 500 * time.Millisecond
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
		}

		collectorCtx, cancel := context.WithTimeout(ctx, r.perCollectorTimeout)
		metrics, err := state.collector.Collect(collectorCtx)
		cancel()

		if err == nil {
			return metrics, nil
		}

		lastErr = err
		if attempt < r.maxRetries {
			log.Printf("WARN: collector %s attempt %d failed: %v (retrying)", state.collector.Type(), attempt+1, err)
		}
	}

	return nil, lastErr
}

func (r *ResilientOrchestrator) isCircuitOpen(state *CollectorState) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return state.consecutiveFail >= r.circuitThreshold && time.Now().Before(state.circuitOpenUntil)
}

func (r *ResilientOrchestrator) recordFailure(state *CollectorState, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	state.consecutiveFail++
	state.lastError = err.Error()

	if state.consecutiveFail >= r.circuitThreshold {
		state.circuitOpenUntil = time.Now().Add(r.circuitCooldown)
		log.Printf("WARN: collector %s circuit opened after %d failures, cooling down for %s", state.collector.Type(), state.consecutiveFail, r.circuitCooldown)
	} else {
		log.Printf("WARN: collector %s failed (%d/%d): %v", state.collector.Type(), state.consecutiveFail, r.circuitThreshold, err)
	}
}

func (r *ResilientOrchestrator) recordSuccess(state *CollectorState) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if state.consecutiveFail > 0 {
		log.Printf("INFO: collector %s recovered after %d failures", state.collector.Type(), state.consecutiveFail)
	}
	state.consecutiveFail = 0
	state.lastSuccess = time.Now()
	state.lastError = ""
}

func (r *ResilientOrchestrator) Status() []CollectorStatus {
	r.mu.RLock()
	defer r.mu.RUnlock()

	statuses := make([]CollectorStatus, len(r.states))
	for i, s := range r.states {
		status := "ok"
		if s.consecutiveFail > 0 && s.consecutiveFail < r.circuitThreshold {
			status = "degraded"
		} else if s.consecutiveFail >= r.circuitThreshold {
			status = "open"
		}

		statuses[i] = CollectorStatus{
			Type:            s.collector.Type(),
			Status:          status,
			ConsecutiveFail: s.consecutiveFail,
			LastSuccess:     s.lastSuccess,
			LastError:       s.lastError,
		}
	}
	return statuses
}

func (r *ResilientOrchestrator) Close() {
	for _, s := range r.states {
		if err := s.collector.Close(); err != nil {
			log.Printf("WARN: error closing collector %s: %v", s.collector.Type(), err)
		}
	}
}

type CollectorStatus struct {
	Type            string    `json:"type"`
	Status          string    `json:"status"`
	ConsecutiveFail int       `json:"consecutive_failures"`
	LastSuccess     time.Time `json:"last_success,omitempty"`
	LastError       string    `json:"last_error,omitempty"`
}
