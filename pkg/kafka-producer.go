// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"context"
	"sync"
	"time"

	"github.com/bborbe/errors"
	libkafka "github.com/bborbe/kafka"
	"github.com/golang/glog"
)

const defaultChannelCapacity = 1000

//counterfeiter:generate -o ../mocks/producer.go --fake-name Producer . Producer
type Producer interface {
	// Publish enqueues an alert record for asynchronous publication. It never
	// blocks and never returns an error; when the bounded channel is full the
	// record is dropped and counted (result="dropped"). Deliberately does NOT
	// take a context: the enqueue must not depend on the request's lifecycle.
	Publish(body []byte, project string, receivedAt time.Time, outcome string)
	// Run drains the channel and publishes records until ctx is cancelled,
	// then flushes all remaining queued records within a 5s deadline and
	// returns nil.
	Run(ctx context.Context) error
}

// NewProducer creates a Producer that asynchronously publishes alert records
// to the given Kafka topic. createSender is called lazily by the worker and
// cached on success; a failed creation is retried on the next record, so a
// broker that returns after startup is picked up without a restart.
// channelCapacity <= 0 defaults to 1000.
func NewProducer(
	createSender func(ctx context.Context) (libkafka.JSONSender, error),
	topic libkafka.Topic,
	metrics Metrics,
	channelCapacity int,
) Producer {
	if channelCapacity <= 0 {
		channelCapacity = defaultChannelCapacity
	}
	return &producer{
		createSender: createSender,
		topic:        topic,
		metrics:      metrics,
		channel:      make(chan KafkaAlertRecord, channelCapacity),
	}
}

type producer struct {
	createSender func(ctx context.Context) (libkafka.JSONSender, error)
	topic        libkafka.Topic
	metrics      Metrics
	channel      chan KafkaAlertRecord
	mux          sync.Mutex
	sender       libkafka.JSONSender
}

func (p *producer) Publish(body []byte, project string, receivedAt time.Time, outcome string) {
	record := KafkaAlertRecord{
		Body:       string(body),
		Project:    project,
		ReceivedAt: receivedAt.UTC().Format(time.RFC3339),
		Outcome:    outcome,
	}
	select {
	case p.channel <- record:
	default:
		p.metrics.KafkaPublishDroppedInc()
		glog.V(2).Infof("kafka publish dropped: channel full")
	}
}

func (p *producer) Run(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return p.flush()
		case record := <-p.channel:
			p.send(ctx, record)
		}
	}
}

func (p *producer) flush() error {
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case record := <-p.channel:
			p.send(context.Background(), record)
		case <-timer.C:
			return nil
		default:
			return nil
		}
	}
}

func (p *producer) send(ctx context.Context, record KafkaAlertRecord) {
	sender, err := p.getSender(ctx)
	if err != nil {
		p.metrics.KafkaPublishFailureInc()
		glog.V(2).Infof("kafka publish failed: %v", err)
		return
	}
	if err := sender.SendUpdate(ctx, p.topic, libkafka.NewKey(record.Project), record); err != nil {
		p.metrics.KafkaPublishFailureInc()
		glog.V(2).Infof("kafka publish failed: %v", err)
		return
	}
	p.metrics.KafkaPublishSuccessInc()
}

func (p *producer) getSender(ctx context.Context) (libkafka.JSONSender, error) {
	p.mux.Lock()
	defer p.mux.Unlock()
	if p.sender != nil {
		return p.sender, nil
	}
	sender, err := p.createSender(ctx)
	if err != nil {
		return nil, errors.Wrap(ctx, err, "create kafka sender failed")
	}
	p.sender = sender
	return sender, nil
}
