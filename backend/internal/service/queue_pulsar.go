package service

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/apache/pulsar-client-go/pulsar"
	"github.com/rs/zerolog/log"
)

// PulsarQueue implements MessageQueue backed by Apache Pulsar.
type PulsarQueue struct {
	client    pulsar.Client
	tenant    string
	namespace string
	producers sync.Map // queueName -> pulsar.Producer
	consumers sync.Map // "queueName:group:consumer" -> pulsar.Consumer
}

// NewPulsarQueue creates a new PulsarQueue.
func NewPulsarQueue(serviceURL, tenant, namespace string) (*PulsarQueue, error) {
	client, err := pulsar.NewClient(pulsar.ClientOptions{
		URL:               serviceURL,
		OperationTimeout:  30 * time.Second,
		ConnectionTimeout: 10 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("pulsar client: %w", err)
	}
	return &PulsarQueue{
		client:    client,
		tenant:    tenant,
		namespace: namespace,
	}, nil
}

// topicName maps a queue name (e.g. "bridge:bot_inbox:abc") to a Pulsar topic.
// "bridge:bot_inbox:abc" -> "persistent://tenant/namespace/bot-inbox-abc"
func (q *PulsarQueue) topicName(queueName string) string {
	// Replace colons with dashes for Pulsar topic naming
	name := strings.ReplaceAll(queueName, ":", "-")
	// Remove the leading "bridge-" prefix since namespace already provides context
	name = strings.TrimPrefix(name, "bridge-")
	return fmt.Sprintf("persistent://%s/%s/%s", q.tenant, q.namespace, name)
}

// getOrCreateProducer lazily creates and caches a producer for the given queue.
func (q *PulsarQueue) getOrCreateProducer(queueName string) (pulsar.Producer, error) {
	topic := q.topicName(queueName)

	if p, ok := q.producers.Load(queueName); ok {
		return p.(pulsar.Producer), nil
	}

	producer, err := q.client.CreateProducer(pulsar.ProducerOptions{
		Topic:           topic,
		DisableBatching: true, // Low latency for chat messages
	})
	if err != nil {
		return nil, fmt.Errorf("create producer for %s: %w", topic, err)
	}

	// Use LoadOrStore to handle concurrent creation
	actual, loaded := q.producers.LoadOrStore(queueName, producer)
	if loaded {
		// Another goroutine already created a producer, close the duplicate
		producer.Close()
		return actual.(pulsar.Producer), nil
	}
	return producer, nil
}

// getOrCreateConsumer lazily creates and caches a consumer for the given queue/group/consumer.
func (q *PulsarQueue) getOrCreateConsumer(queueName, group, consumer string) (pulsar.Consumer, error) {
	consumerKey := queueName + ":" + group + ":" + consumer
	topic := q.topicName(queueName)

	if c, ok := q.consumers.Load(consumerKey); ok {
		return c.(pulsar.Consumer), nil
	}

	cons, err := q.client.Subscribe(pulsar.ConsumerOptions{
		Topic:                       topic,
		SubscriptionName:            group,
		Type:                        pulsar.Shared,
		SubscriptionInitialPosition: pulsar.SubscriptionPositionLatest,
		Name:                        consumer,
	})
	if err != nil {
		return nil, fmt.Errorf("subscribe %s/%s: %w", topic, group, err)
	}

	actual, loaded := q.consumers.LoadOrStore(consumerKey, cons)
	if loaded {
		cons.Close()
		return actual.(pulsar.Consumer), nil
	}
	return cons, nil
}

func (q *PulsarQueue) Publish(ctx context.Context, queueName, message string) (string, error) {
	producer, err := q.getOrCreateProducer(queueName)
	if err != nil {
		return "", err
	}

	msgID, err := producer.Send(ctx, &pulsar.ProducerMessage{
		Payload: []byte(message),
	})
	if err != nil {
		return "", fmt.Errorf("pulsar send: %w", err)
	}

	id := base64.StdEncoding.EncodeToString(msgID.Serialize())
	log.Debug().Str("topic", q.topicName(queueName)).Str("msg_id", id).Msg("Pulsar publish")
	return id, nil
}

func (q *PulsarQueue) Consume(ctx context.Context, queueName, group, consumer string, blockMs int) (*QueueMessage, error) {
	cons, err := q.getOrCreateConsumer(queueName, group, consumer)
	if err != nil {
		return nil, err
	}

	timeout := time.Duration(blockMs) * time.Millisecond
	receiveCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	msg, err := cons.Receive(receiveCtx)
	if err != nil {
		// Timeout or context cancelled — not an error
		if receiveCtx.Err() != nil {
			return nil, nil
		}
		return nil, fmt.Errorf("pulsar receive: %w", err)
	}

	id := base64.StdEncoding.EncodeToString(msg.ID().Serialize())
	data := string(msg.Payload())
	log.Debug().Str("topic", q.topicName(queueName)).Str("group", group).Str("msg_id", id).Msg("Pulsar consume")
	return &QueueMessage{ID: id, Data: data}, nil
}

func (q *PulsarQueue) Ack(ctx context.Context, queueName, group, messageID string) error {
	consumerKey := queueName + ":" + group
	// Find any consumer for this queue+group (consumer name suffix doesn't matter for ack)
	var cons pulsar.Consumer
	q.consumers.Range(func(key, value interface{}) bool {
		if strings.HasPrefix(key.(string), consumerKey) {
			cons = value.(pulsar.Consumer)
			return false
		}
		return true
	})
	if cons == nil {
		return fmt.Errorf("no consumer found for %s/%s", queueName, group)
	}

	data, err := base64.StdEncoding.DecodeString(messageID)
	if err != nil {
		return fmt.Errorf("decode message id: %w", err)
	}

	msgID, err := pulsar.DeserializeMessageID(data)
	if err != nil {
		return fmt.Errorf("deserialize message id: %w", err)
	}

	if err := cons.AckID(msgID); err != nil {
		return fmt.Errorf("pulsar ack: %w", err)
	}
	log.Debug().Str("topic", q.topicName(queueName)).Str("msg_id", messageID).Msg("Pulsar ack")
	return nil
}

func (q *PulsarQueue) DeleteMessage(_ context.Context, _ string, _ string) error {
	// Pulsar handles message lifecycle through ack/nack and retention policies.
	// Individual message deletion is not a standard Pulsar operation.
	return nil
}

func (q *PulsarQueue) DeleteQueue(_ context.Context, queueName string) error {
	// Close and remove producer
	if p, loaded := q.producers.LoadAndDelete(queueName); loaded {
		p.(pulsar.Producer).Close()
	}
	// Close and remove all consumers for this queue
	q.consumers.Range(func(key, value interface{}) bool {
		if strings.HasPrefix(key.(string), queueName+":") {
			value.(pulsar.Consumer).Close()
			q.consumers.Delete(key)
		}
		return true
	})
	log.Debug().Str("topic", q.topicName(queueName)).Msg("Pulsar delete_queue (closed producer/consumers)")
	return nil
}

func (q *PulsarQueue) Purge(_ context.Context, queueName string) error {
	// Close existing consumers so they reconnect with latest position
	q.consumers.Range(func(key, value interface{}) bool {
		if strings.HasPrefix(key.(string), queueName+":") {
			value.(pulsar.Consumer).Close()
			q.consumers.Delete(key)
		}
		return true
	})
	log.Debug().Str("topic", q.topicName(queueName)).Msg("Pulsar purge (closed consumers)")
	return nil
}

func (q *PulsarQueue) EnsureGroup(_ context.Context, _ string, _ string) error {
	// Pulsar automatically creates subscriptions when subscribing.
	return nil
}

func (q *PulsarQueue) Close() error {
	q.producers.Range(func(key, value interface{}) bool {
		value.(pulsar.Producer).Close()
		return true
	})
	q.consumers.Range(func(key, value interface{}) bool {
		value.(pulsar.Consumer).Close()
		return true
	})
	q.client.Close()
	log.Info().Msg("Pulsar queue closed")
	return nil
}
