package main

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/IBM/sarama"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "topic-controller")
	brokers := splitCSV(env("KAFKA_BROKERS", "localhost:9092"))
	topic := env("KAFKA_TOPIC", "trip-recorded")
	partitions := int32(envInt("KAFKA_PARTITIONS", 3))
	retentionMS := int64(envInt("KAFKA_RETENTION_MS", 172800000))
	segmentMS := int64(envInt("KAFKA_SEGMENT_MS", 10000))
	replicationFactor := int16(envInt("KAFKA_REPLICATION_FACTOR", 1))

	admin, err := newAdminWithRetry(brokers, 60*time.Second)
	if err != nil {
		logger.Error("cannot connect to Kafka", "error", err)
		os.Exit(1)
	}
	defer admin.Close()

	topics, err := admin.ListTopics()
	if err != nil {
		logger.Error("cannot list topics", "error", err)
		os.Exit(1)
	}

	detail, exists := topics[topic]
	retention := strconv.FormatInt(retentionMS, 10)
	segment := strconv.FormatInt(segmentMS, 10)
	if !exists {
		err = admin.CreateTopic(topic, &sarama.TopicDetail{
			NumPartitions:     partitions,
			ReplicationFactor: replicationFactor,
			ConfigEntries: map[string]*string{
				"retention.ms": &retention,
				"segment.ms":   &segment,
			},
		}, false)
		if err != nil {
			logger.Error("cannot create topic", "topic", topic, "error", err)
			os.Exit(1)
		}
		logger.Info("topic created", "topic", topic, "partitions", partitions, "retention_ms", retentionMS, "segment_ms", segmentMS)
		return
	}

	if partitions > detail.NumPartitions {
		if err := admin.CreatePartitions(topic, partitions, nil, false); err != nil {
			logger.Error("cannot increase partitions", "topic", topic, "from", detail.NumPartitions, "to", partitions, "error", err)
			os.Exit(1)
		}
		logger.Info("partition count increased", "topic", topic, "from", detail.NumPartitions, "to", partitions)
	} else if partitions < detail.NumPartitions {
		logger.Warn("Kafka cannot decrease partition count; existing value kept", "topic", topic, "configured", partitions, "actual", detail.NumPartitions)
	} else {
		logger.Info("partition count already matches configuration", "topic", topic, "partitions", partitions)
	}

	if err := admin.AlterConfig(sarama.TopicResource, topic, map[string]*string{"retention.ms": &retention, "segment.ms": &segment}, false); err != nil {
		logger.Error("cannot update retention", "topic", topic, "retention_ms", retentionMS, "error", err)
		os.Exit(1)
	}
	logger.Info("topic retention reconciled", "topic", topic, "retention_ms", retentionMS, "segment_ms", segmentMS)
}

func newAdminWithRetry(brokers []string, timeout time.Duration) (sarama.ClusterAdmin, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		config := sarama.NewConfig()
		config.ClientID = "chainwise-topic-controller"
		admin, err := sarama.NewClusterAdmin(brokers, config)
		if err == nil {
			return admin, nil
		}
		lastErr = err
		time.Sleep(2 * time.Second)
	}
	return nil, fmt.Errorf("Kafka unavailable after %s: %w", timeout, lastErr)
}

func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func envInt(key string, fallback int) int {
	value, err := strconv.Atoi(os.Getenv(key))
	if err != nil {
		return fallback
	}
	return value
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
