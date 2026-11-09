//go:build integration

package creation_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/images"
	kafkago "github.com/segmentio/kafka-go"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func imageStorage(t *testing.T, ctx context.Context) *images.S3 {
	t.Helper()
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	t.Setenv("AWS_SESSION_TOKEN", "")
	c, err := testcontainers.Run(
		ctx,
		"floci/floci:2.3.0",
		testcontainers.WithExposedPorts("4566/tcp"),
		testcontainers.WithEnv(map[string]string{"FLOCI_SERVICES_S3_ENFORCE_AUTH": "true"}),
		testcontainers.WithWaitStrategy(wait.ForListeningPort("4566/tcp")),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Terminate(context.Background()) })
	endpoint, err := c.PortEndpoint(ctx, "4566/tcp", "http")
	if err != nil {
		t.Fatal(err)
	}
	storage, err := images.NewS3(ctx, images.S3Config{Bucket: "order-test", Endpoint: endpoint, Region: "us-east-1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.InitializeLocalBucket(ctx); err != nil {
		t.Fatal(err)
	}
	return storage
}

func kafkaBroker(t *testing.T, ctx context.Context) string {
	t.Helper()
	c, err := testcontainers.Run(
		ctx,
		"apache/kafka:4.2.2",
		testcontainers.WithExposedPorts("19092/tcp"),
		testcontainers.WithEntrypoint("/bin/sh"),
		testcontainers.WithCmd(
			"-c",
			"while [ ! -f /tmp/start-order-kafka ]; do sleep 0.1; done; exec sh /tmp/start-order-kafka",
		),
		testcontainers.WithEnv(map[string]string{
			"KAFKA_NODE_ID":                          "1",
			"KAFKA_PROCESS_ROLES":                    "broker,controller",
			"KAFKA_CONTROLLER_QUORUM_VOTERS":         "1@localhost:9093",
			"KAFKA_CONTROLLER_LISTENER_NAMES":        "CONTROLLER",
			"KAFKA_LISTENERS":                        "BROKER://:9092,EXTERNAL://:19092,CONTROLLER://:9093",
			"KAFKA_LISTENER_SECURITY_PROTOCOL_MAP":   "BROKER:PLAINTEXT,EXTERNAL:PLAINTEXT,CONTROLLER:PLAINTEXT",
			"KAFKA_INTER_BROKER_LISTENER_NAME":       "BROKER",
			"KAFKA_OFFSETS_TOPIC_REPLICATION_FACTOR": "1",
			"KAFKA_AUTO_CREATE_TOPICS_ENABLE":        "false",
		}),
		testcontainers.WithWaitStrategy(wait.ForMappedPort("19092/tcp")),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Terminate(context.Background()) })
	address, err := c.PortEndpoint(ctx, "19092/tcp", "")
	if err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf(
		"export KAFKA_ADVERTISED_LISTENERS='BROKER://localhost:9092,EXTERNAL://%s'\nexec /etc/kafka/docker/run\n",
		address,
	)
	if err := c.CopyToContainer(ctx, []byte(script), "/tmp/start-order-kafka", 0755); err != nil {
		t.Fatal(err)
	}
	if err := wait.ForListeningPort("19092/tcp").WithStartupTimeout(time.Minute).WaitUntilReady(ctx, c); err != nil {
		t.Fatal(err)
	}
	// A listening socket is not evidence that the broker serves Kafka
	// requests: during startup it briefly accepts and then drops connections,
	// which surfaces as EOF or a connection reset. Retry the handshake until
	// the broker answers; CreateTopics is idempotent, so retries are harmless.
	var topics []kafkago.TopicConfig
	for _, name := range []string{"order.status_changed", "order.outcome", "order.courier_warning_requested"} {
		topics = append(topics, kafkago.TopicConfig{Topic: name, NumPartitions: 1, ReplicationFactor: 1})
	}
	handshake, cancelHandshake := context.WithTimeout(ctx, time.Minute)
	defer cancelHandshake()
	for {
		var err error
		connection, dialErr := kafkago.DialContext(handshake, "tcp", address)
		if dialErr == nil {
			// A broker that accepts but never answers must not pin the loop:
			// bound each attempt so the handshake deadline stays observable.
			_ = connection.SetDeadline(time.Now().Add(10 * time.Second))
			err = connection.CreateTopics(topics...)
			_ = connection.Close()
			if err == nil {
				break
			}
		} else {
			err = dialErr
		}
		select {
		case <-handshake.Done():
			t.Fatalf("Kafka broker did not serve a request: %v", err)
		case <-time.After(200 * time.Millisecond):
		}
	}
	// Topic creation is asynchronous; a listening broker is not evidence that
	// each partition has an elected, serving leader yet.
	for _, topic := range topics {
		_ = topicOffset(t, address, topic.Topic)
	}
	return address
}
