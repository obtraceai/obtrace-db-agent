package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/segmentio/kafka-go"
)

type KafkaCollector struct {
	brokers  []string
	name     string
	instance string
}

func NewKafkaCollector(dsn, name, instance string) (*KafkaCollector, error) {
	brokers := strings.Split(dsn, ",")
	for i := range brokers {
		brokers[i] = strings.TrimSpace(brokers[i])
	}

	if err := dialAnyBroker(brokers); err != nil {
		return nil, fmt.Errorf("no reachable kafka broker: %w", err)
	}

	return &KafkaCollector{
		brokers:  brokers,
		name:     name,
		instance: instance,
	}, nil
}

func dialAnyBroker(brokers []string) error {
	var lastErr error
	for _, b := range brokers {
		conn, err := kafka.Dial("tcp", b)
		if err != nil {
			lastErr = err
			continue
		}
		conn.Close()
		return nil
	}
	return lastErr
}

func (k *KafkaCollector) dialWithFailover() (*kafka.Conn, error) {
	var lastErr error
	for _, b := range k.brokers {
		conn, err := kafka.DialContext(context.Background(), "tcp", b)
		if err != nil {
			lastErr = err
			continue
		}
		conn.SetDeadline(time.Now().Add(8 * time.Second))
		return conn, nil
	}
	return nil, fmt.Errorf("all brokers unreachable: %w", lastErr)
}

func (k *KafkaCollector) Type() string { return "kafka" }
func (k *KafkaCollector) Close() error { return nil }

func (k *KafkaCollector) baseAttrs() map[string]string {
	return map[string]string{
		"messaging.system":   "kafka",
		"messaging.instance": k.instance,
		"messaging.name":     k.name,
	}
}

func (k *KafkaCollector) Collect(ctx context.Context) ([]Metric, error) {
	conn, err := k.dialWithFailover()
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	var metrics []Metric

	m, err := k.collectBrokersAndController(conn)
	if err == nil {
		metrics = append(metrics, m...)
	}

	m, err = k.collectTopics(conn)
	if err == nil {
		metrics = append(metrics, m...)
	}

	m, err = k.collectConsumerGroups(ctx)
	if err == nil {
		metrics = append(metrics, m...)
	}

	if len(metrics) == 0 {
		return nil, fmt.Errorf("no kafka metrics collected")
	}

	return metrics, nil
}

func (k *KafkaCollector) collectBrokersAndController(conn *kafka.Conn) ([]Metric, error) {
	brokers, err := conn.Brokers()
	if err != nil {
		return nil, fmt.Errorf("list brokers: %w", err)
	}

	controller, err := conn.Controller()
	if err != nil {
		return nil, fmt.Errorf("get controller: %w", err)
	}

	attrs := k.baseAttrs()
	attrs["messaging.kafka.controller"] = net.JoinHostPort(controller.Host, strconv.Itoa(controller.Port))

	return []Metric{
		{Name: "messaging.kafka.brokers.count", Value: float64(len(brokers)), Unit: "{brokers}", Attributes: attrs},
	}, nil
}

func (k *KafkaCollector) collectTopics(conn *kafka.Conn) ([]Metric, error) {
	partitions, err := conn.ReadPartitions()
	if err != nil {
		return nil, fmt.Errorf("read partitions: %w", err)
	}

	type topicInfo struct {
		partitions int
		replicas   int
		isr        int
	}
	topics := map[string]*topicInfo{}

	for _, p := range partitions {
		t, ok := topics[p.Topic]
		if !ok {
			t = &topicInfo{}
			topics[p.Topic] = t
		}
		t.partitions++
		if len(p.Replicas) > t.replicas {
			t.replicas = len(p.Replicas)
		}
		t.isr += len(p.Isr)
	}

	attrs := k.baseAttrs()
	var metrics []Metric

	totalPartitions := 0
	userTopics := 0
	totalUnderReplicated := 0

	for name, info := range topics {
		totalPartitions += info.partitions

		if strings.HasPrefix(name, "_") {
			continue
		}
		userTopics++

		expectedISR := info.partitions * info.replicas
		underReplicated := 0
		if expectedISR > info.isr {
			underReplicated = expectedISR - info.isr
		}
		totalUnderReplicated += underReplicated

		topicAttrs := k.baseAttrs()
		topicAttrs["messaging.destination.name"] = name
		metrics = append(metrics,
			Metric{Name: "messaging.kafka.topic.partitions", Value: float64(info.partitions), Unit: "{partitions}", Attributes: topicAttrs},
			Metric{Name: "messaging.kafka.topic.replication_factor", Value: float64(info.replicas), Unit: "1", Attributes: topicAttrs},
			Metric{Name: "messaging.kafka.topic.under_replicated", Value: float64(underReplicated), Unit: "{partitions}", Attributes: topicAttrs},
		)
	}

	metrics = append(metrics,
		Metric{Name: "messaging.kafka.topics.count", Value: float64(userTopics), Unit: "{topics}", Attributes: attrs},
		Metric{Name: "messaging.kafka.partitions.total", Value: float64(totalPartitions), Unit: "{partitions}", Attributes: attrs},
		Metric{Name: "messaging.kafka.under_replicated.total", Value: float64(totalUnderReplicated), Unit: "{partitions}", Attributes: attrs},
	)

	return metrics, nil
}

func (k *KafkaCollector) collectConsumerGroups(ctx context.Context) ([]Metric, error) {
	client := &kafka.Client{
		Addr:    kafka.TCP(k.brokers...),
		Timeout: 5 * time.Second,
	}

	groups, err := client.ListGroups(ctx, &kafka.ListGroupsRequest{
		Addr: kafka.TCP(k.brokers[0]),
	})
	if err != nil {
		return nil, fmt.Errorf("list consumer groups: %w", err)
	}

	attrs := k.baseAttrs()
	var metrics []Metric

	metrics = append(metrics, Metric{
		Name: "messaging.kafka.consumer_groups.count", Value: float64(len(groups.Groups)), Unit: "{groups}", Attributes: attrs,
	})

	groupIDs := make([]string, 0, len(groups.Groups))
	for _, g := range groups.Groups {
		groupIDs = append(groupIDs, g.GroupID)
	}

	if len(groupIDs) > 0 {
		described, err := client.DescribeGroups(ctx, &kafka.DescribeGroupsRequest{
			Addr:     kafka.TCP(k.brokers[0]),
			GroupIDs: groupIDs,
		})
		if err == nil {
			stateCount := map[string]int{}
			for _, g := range described.Groups {
				stateCount[g.GroupState]++
			}
			for state, count := range stateCount {
				stateAttrs := k.baseAttrs()
				stateAttrs["messaging.kafka.consumer_group.state"] = state
				metrics = append(metrics, Metric{
					Name: "messaging.kafka.consumer_groups.by_state", Value: float64(count), Unit: "{groups}", Attributes: stateAttrs,
				})
			}
		}
	}

	limit := len(groupIDs)
	if limit > 50 {
		log.Printf("WARN: kafka consumer groups truncated to 50 (total: %d)", len(groupIDs))
		limit = 50
	}

	for _, groupID := range groupIDs[:limit] {
		offsets, err := client.OffsetFetch(ctx, &kafka.OffsetFetchRequest{
			Addr:    kafka.TCP(k.brokers[0]),
			GroupID: groupID,
		})
		if err != nil {
			continue
		}

		var totalLag int64
		for topic, partitionOffsets := range offsets.Topics {
			for _, po := range partitionOffsets {
				if po.CommittedOffset < 0 {
					continue
				}

				endResp, err := client.ListOffsets(ctx, &kafka.ListOffsetsRequest{
					Addr: kafka.TCP(k.brokers[0]),
					Topics: map[string][]kafka.OffsetRequest{
						topic: {{Partition: po.Partition, Timestamp: -1}},
					},
				})
				if err != nil {
					continue
				}
				if topicOffsets, ok := endResp.Topics[topic]; ok {
					for _, to := range topicOffsets {
						if to.Partition == po.Partition && to.LastOffset > po.CommittedOffset {
							totalLag += to.LastOffset - po.CommittedOffset
						}
					}
				}
			}
		}

		lagAttrs := k.baseAttrs()
		lagAttrs["messaging.kafka.consumer_group"] = groupID
		metrics = append(metrics, Metric{
			Name: "messaging.kafka.consumer_group.lag", Value: float64(totalLag), Unit: "{messages}", Attributes: lagAttrs,
		})
	}

	return metrics, nil
}
