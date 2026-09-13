// Copyright (c) 2024 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"github.com/prometheus/client_golang/prometheus"
)

//counterfeiter:generate -o ../mocks/metrics.go --fake-name Metrics . Metrics
type Metrics interface {
	SentryAlertTotalInc()
	SentryAlertRejectedInc()
	SentryAlertForwardInc()
	KafkaPublishSuccessInc()
	KafkaPublishFailureInc()
	KafkaPublishDroppedInc()
}

func NewMetrics(registerer prometheus.Registerer) Metrics {
	sentryAlertTotalCounter := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "sentry_proxy_alerts_total",
		Help: "Counter for all sentryAlerts",
	})
	sentryAlertRejectCounter := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "sentry_proxy_alerts_rejected_total",
		Help: "Counter for rejected sentryAlerts",
	})
	sentryAlertForwardCounter := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "sentry_proxy_alerts_forwarded_total",
		Help: "Counter for forwarded sentryAlerts",
	})
	kafkaPublishCounter := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "sentry_proxy_kafka_publishes_total",
		Help: "Counter for kafka publishes by result",
	}, []string{"result"})
	for _, result := range []string{"success", "failure", "dropped"} {
		kafkaPublishCounter.WithLabelValues(result).Add(0)
	}

	registerer.MustRegister(
		sentryAlertTotalCounter,
		sentryAlertRejectCounter,
		sentryAlertForwardCounter,
		kafkaPublishCounter,
	)

	return &metrics{
		sentryAlertTotalCounter:   sentryAlertTotalCounter,
		sentryAlertRejectCounter:  sentryAlertRejectCounter,
		sentryAlertForwardCounter: sentryAlertForwardCounter,
		kafkaPublishCounter:       kafkaPublishCounter,
	}
}

type metrics struct {
	sentryAlertForwardCounter prometheus.Counter
	sentryAlertRejectCounter  prometheus.Counter
	sentryAlertTotalCounter   prometheus.Counter
	kafkaPublishCounter       *prometheus.CounterVec
}

func (m *metrics) SentryAlertTotalInc() {
	m.sentryAlertTotalCounter.Inc()
}

func (m *metrics) SentryAlertRejectedInc() {
	m.sentryAlertRejectCounter.Inc()
}

func (m *metrics) SentryAlertForwardInc() {
	m.sentryAlertForwardCounter.Inc()
}

func (m *metrics) KafkaPublishSuccessInc() {
	m.kafkaPublishCounter.WithLabelValues("success").Inc()
}

func (m *metrics) KafkaPublishFailureInc() {
	m.kafkaPublishCounter.WithLabelValues("failure").Inc()
}

func (m *metrics) KafkaPublishDroppedInc() {
	m.kafkaPublishCounter.WithLabelValues("dropped").Inc()
}
