package main

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

type SQSCollector struct {
	client   *sqs.Client
	region   string
	name     string
	instance string
}

func NewSQSCollector(dsn, name, instance string) (*SQSCollector, error) {
	ctx := context.Background()

	optFns := []func(*config.LoadOptions) error{}

	if strings.HasPrefix(dsn, "http") {
		optFns = append(optFns, config.WithRegion("us-east-1"))
	} else {
		optFns = append(optFns, config.WithRegion(dsn))
	}

	cfg, err := config.LoadDefaultConfig(ctx, optFns...)
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}

	sqsOpts := []func(*sqs.Options){}
	if strings.HasPrefix(dsn, "http") {
		sqsOpts = append(sqsOpts, func(o *sqs.Options) {
			o.BaseEndpoint = aws.String(dsn)
		})
	}

	client := sqs.NewFromConfig(cfg, sqsOpts...)

	_, err = client.ListQueues(ctx, &sqs.ListQueuesInput{MaxResults: aws.Int32(1)})
	if err != nil {
		return nil, fmt.Errorf("SQS connectivity check: %w", err)
	}

	region := dsn
	if strings.HasPrefix(dsn, "http") {
		region = "local"
	}

	return &SQSCollector{
		client:   client,
		region:   region,
		name:     name,
		instance: instance,
	}, nil
}

func (s *SQSCollector) Type() string { return "sqs" }
func (s *SQSCollector) Close() error { return nil }

func (s *SQSCollector) baseAttrs() map[string]string {
	return map[string]string{
		"messaging.system":   "aws_sqs",
		"cloud.provider":     "aws",
		"cloud.region":       s.region,
		"messaging.instance": s.instance,
		"messaging.name":     s.name,
	}
}

func (s *SQSCollector) Collect(ctx context.Context) ([]Metric, error) {
	var queueURLs []string
	paginator := sqs.NewListQueuesPaginator(s.client, &sqs.ListQueuesInput{
		MaxResults: aws.Int32(100),
	})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("SQS ListQueues: %w", err)
		}
		queueURLs = append(queueURLs, page.QueueUrls...)
		if len(queueURLs) >= 200 {
			log.Printf("WARN: SQS queue list truncated at 200 (total available: >%d)", len(queueURLs))
			break
		}
	}

	attrs := s.baseAttrs()
	var metrics []Metric

	metrics = append(metrics, Metric{
		Name: "messaging.sqs.queues.count", Value: float64(len(queueURLs)), Unit: "{queues}", Attributes: attrs,
	})

	for _, queueURL := range queueURLs {
		parts := strings.Split(queueURL, "/")
		queueName := parts[len(parts)-1]

		resp, err := s.client.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
			QueueUrl: aws.String(queueURL),
			AttributeNames: []types.QueueAttributeName{
				types.QueueAttributeNameApproximateNumberOfMessages,
				types.QueueAttributeNameApproximateNumberOfMessagesNotVisible,
				types.QueueAttributeNameApproximateNumberOfMessagesDelayed,
			},
		})
		if err != nil {
			continue
		}

		qAttrs := s.baseAttrs()
		qAttrs["messaging.destination.name"] = queueName

		if v, ok := resp.Attributes["ApproximateNumberOfMessages"]; ok {
			metrics = append(metrics, Metric{Name: "messaging.sqs.queue.visible", Value: parseF(v), Unit: "{messages}", Attributes: qAttrs})
		}
		if v, ok := resp.Attributes["ApproximateNumberOfMessagesNotVisible"]; ok {
			metrics = append(metrics, Metric{Name: "messaging.sqs.queue.in_flight", Value: parseF(v), Unit: "{messages}", Attributes: qAttrs})
		}
		if v, ok := resp.Attributes["ApproximateNumberOfMessagesDelayed"]; ok {
			metrics = append(metrics, Metric{Name: "messaging.sqs.queue.delayed", Value: parseF(v), Unit: "{messages}", Attributes: qAttrs})
		}
	}

	return metrics, nil
}
