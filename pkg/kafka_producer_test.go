// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"context"
	"encoding/json"
	"time"

	"github.com/bborbe/errors"
	libkafka "github.com/bborbe/kafka"
	kafkamocks "github.com/bborbe/kafka/mocks"
	"github.com/bborbe/validation"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/bborbe/sentry-proxy/mocks"
	"github.com/bborbe/sentry-proxy/pkg"
)

var _ = Describe("Kafka Producer", func() {
	var ctx context.Context
	var cancel context.CancelFunc

	BeforeEach(func() {
		ctx, cancel = context.WithCancel(context.Background())
	})

	AfterEach(func() {
		cancel()
	})

	It("flushes all queued records before Run returns on shutdown", func() {
		fake := &kafkamocks.KafkaJSONSender{}
		producer := pkg.NewProducer(
			func(ctx context.Context) (libkafka.JSONSender, error) { return fake, nil },
			libkafka.Topic("develop-raw-sentry-alert-input"),
			&mocks.Metrics{},
			100,
		)
		for i := 0; i < 10; i++ {
			producer.Publish(
				[]byte("raw-envelope"),
				"project-a",
				time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC),
				"forwarded",
			)
		}

		done := make(chan error, 1)
		go func() {
			done <- producer.Run(ctx)
		}()
		cancel()

		Eventually(done).Should(Receive(BeNil()))
		Expect(fake.SendUpdateCallCount()).To(Equal(10))
	})

	It("counts publish failures in the registered metric", func() {
		registry := prometheus.NewRegistry()
		metrics := pkg.NewMetrics(registry)
		producer := pkg.NewProducer(
			func(ctx context.Context) (libkafka.JSONSender, error) {
				return nil, errors.New(ctx, "no broker reachable")
			},
			libkafka.Topic("develop-raw-sentry-alert-input"),
			metrics,
			10,
		)
		producer.Publish(
			[]byte("raw-envelope"),
			"project-a",
			time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC),
			"forwarded",
		)
		cancel()

		Expect(producer.Run(ctx)).To(BeNil())
		Expect(
			metricValue(registry, "sentry_proxy_kafka_publish_counter", "failure"),
		).To(Equal(1.0))
	})

	It("counts publish successes in the registered metric", func() {
		registry := prometheus.NewRegistry()
		metrics := pkg.NewMetrics(registry)
		fake := &kafkamocks.KafkaJSONSender{}
		producer := pkg.NewProducer(
			func(ctx context.Context) (libkafka.JSONSender, error) { return fake, nil },
			libkafka.Topic("develop-raw-sentry-alert-input"),
			metrics,
			10,
		)
		producer.Publish(
			[]byte("raw-envelope"),
			"project-a",
			time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC),
			"forwarded",
		)
		cancel()

		Expect(producer.Run(ctx)).To(BeNil())
		Expect(
			metricValue(registry, "sentry_proxy_kafka_publish_counter", "success"),
		).To(Equal(1.0))
	})

	It("counts drops in the registered metric", func() {
		registry := prometheus.NewRegistry()
		metrics := pkg.NewMetrics(registry)
		producer := pkg.NewProducer(
			func(ctx context.Context) (libkafka.JSONSender, error) { return &kafkamocks.KafkaJSONSender{}, nil },
			libkafka.Topic("develop-raw-sentry-alert-input"),
			metrics,
			1,
		)
		producer.Publish(
			[]byte("raw-envelope"),
			"project-a",
			time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC),
			"forwarded",
		)
		producer.Publish(
			[]byte("raw-envelope"),
			"project-a",
			time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC),
			"forwarded",
		)

		Expect(
			metricValue(registry, "sentry_proxy_kafka_publish_counter", "dropped"),
		).To(Equal(1.0))
	})

	It("drops and counts when the bounded channel is full", func() {
		metrics := &mocks.Metrics{}
		producer := pkg.NewProducer(
			func(ctx context.Context) (libkafka.JSONSender, error) { return &kafkamocks.KafkaJSONSender{}, nil },
			libkafka.Topic("develop-raw-sentry-alert-input"),
			metrics,
			1,
		)
		producer.Publish(
			[]byte("raw-envelope"),
			"project-a",
			time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC),
			"forwarded",
		)
		producer.Publish(
			[]byte("raw-envelope"),
			"project-a",
			time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC),
			"forwarded",
		)

		Expect(metrics.KafkaPublishDroppedIncCallCount()).To(Equal(1))
	})

	It("publishes the durable record contract", func() {
		fake := &kafkamocks.KafkaJSONSender{}
		producer := pkg.NewProducer(
			func(ctx context.Context) (libkafka.JSONSender, error) { return fake, nil },
			libkafka.Topic("develop-raw-sentry-alert-input"),
			&mocks.Metrics{},
			10,
		)
		producer.Publish(
			[]byte("raw-envelope"),
			"project-a",
			time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC),
			"forwarded",
		)
		cancel()

		Expect(producer.Run(ctx)).To(BeNil())
		Expect(fake.SendUpdateCallCount()).To(Equal(1))

		_, topic, key, value, _ := fake.SendUpdateArgsForCall(0)
		Expect(topic).To(Equal(libkafka.Topic("develop-raw-sentry-alert-input")))
		Expect(string(key.Bytes())).To(Equal("project-a"))

		record, ok := value.(pkg.KafkaAlertRecord)
		Expect(ok).To(BeTrue())
		Expect(record.Body).To(Equal("raw-envelope"))
		Expect(record.Project).To(Equal("project-a"))
		Expect(record.ReceivedAt).To(Equal("2026-09-09T12:00:00Z"))
		Expect(record.Outcome).To(Equal("forwarded"))

		raw, err := json.Marshal(record)
		Expect(err).NotTo(HaveOccurred())
		var fields map[string]any
		Expect(json.Unmarshal(raw, &fields)).To(Succeed())
		Expect(fields).To(HaveLen(4))
		Expect(fields).To(HaveKey("body"))
		Expect(fields).To(HaveKey("project"))
		Expect(fields).To(HaveKey("received_at"))
		Expect(fields).To(HaveKey("outcome"))

		Expect(
			libkafka.Topic("develop-raw-sentry-alert-input").Validate(context.Background()),
		).To(BeNil())
	})

	It("counts a SendUpdate error as a publish failure", func() {
		metrics := &mocks.Metrics{}
		fake := &kafkamocks.KafkaJSONSender{}
		fake.SendUpdateReturns(errors.New(context.Background(), "send failed"))
		producer := pkg.NewProducer(
			func(ctx context.Context) (libkafka.JSONSender, error) { return fake, nil },
			libkafka.Topic("develop-raw-sentry-alert-input"),
			metrics,
			10,
		)
		producer.Publish(
			[]byte("raw-envelope"),
			"project-a",
			time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC),
			"forwarded",
		)
		cancel()

		Expect(producer.Run(ctx)).To(BeNil())
		Expect(metrics.KafkaPublishFailureIncCallCount()).To(Equal(1))
		Expect(metrics.KafkaPublishSuccessIncCallCount()).To(Equal(0))
	})

	It("defaults channel capacity to 1000 when 0 is passed", func() {
		metrics := &mocks.Metrics{}
		fake := &kafkamocks.KafkaJSONSender{}
		producer := pkg.NewProducer(
			func(ctx context.Context) (libkafka.JSONSender, error) { return fake, nil },
			libkafka.Topic("develop-raw-sentry-alert-input"),
			metrics,
			0,
		)
		for i := 0; i < 5; i++ {
			producer.Publish(
				[]byte("raw-envelope"),
				"project-a",
				time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC),
				"forwarded",
			)
		}

		Expect(metrics.KafkaPublishDroppedIncCallCount()).To(Equal(0))
		cancel()

		Expect(producer.Run(ctx)).To(BeNil())
		Expect(fake.SendUpdateCallCount()).To(Equal(5))
	})

	It("retries sender creation on the next record after a failure", func() {
		metrics := &mocks.Metrics{}
		fake := &kafkamocks.KafkaJSONSender{}
		calls := 0
		producer := pkg.NewProducer(
			func(ctx context.Context) (libkafka.JSONSender, error) {
				calls++
				if calls == 1 {
					return nil, errors.New(ctx, "broker unreachable")
				}
				return fake, nil
			},
			libkafka.Topic("develop-raw-sentry-alert-input"),
			metrics,
			10,
		)
		producer.Publish(
			[]byte("raw-envelope"),
			"project-a",
			time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC),
			"forwarded",
		)
		producer.Publish(
			[]byte("raw-envelope"),
			"project-a",
			time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC),
			"forwarded",
		)
		cancel()

		Expect(producer.Run(ctx)).To(BeNil())
		Expect(metrics.KafkaPublishFailureIncCallCount()).To(Equal(1))
		Expect(metrics.KafkaPublishSuccessIncCallCount()).To(Equal(1))
	})

	DescribeTable("record outcome validation",
		func(outcome string, valid bool) {
			err := pkg.KafkaAlertRecord{Outcome: outcome}.Validate(context.Background())
			if valid {
				Expect(err).To(BeNil())
			} else {
				Expect(err).To(HaveOccurred())
				Expect(errors.Is(err, validation.Error)).To(BeTrue())
			}
		},
		Entry("forwarded is valid", "forwarded", true),
		Entry("rejected is valid", "rejected", true),
		Entry("upstream_error is valid", "upstream_error", true),
		Entry("unknown outcome is invalid", "queued", false),
		Entry("empty outcome is invalid", "", false),
	)
})

func metricValue(registry *prometheus.Registry, familyName string, labelValue string) float64 {
	families, err := registry.Gather()
	Expect(err).NotTo(HaveOccurred())
	for _, family := range families {
		if family.GetName() != familyName {
			continue
		}
		for _, metric := range family.GetMetric() {
			for _, labelPair := range metric.GetLabel() {
				if labelPair.GetName() == "result" && labelPair.GetValue() == labelValue {
					return metric.GetGauge().GetValue()
				}
			}
		}
	}
	return -1
}
