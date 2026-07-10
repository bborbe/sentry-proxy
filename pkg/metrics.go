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
}

func NewMetrics(registerer prometheus.Registerer) Metrics {
	sentryAlertTotalCounter := prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "sentry_proxy",
		Subsystem: "total",
		Name:      "counter",
		Help:      "Counter for all sentryAlerts",
	})
	sentryAlertRejectCounter := prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "sentry_proxy",
		Subsystem: "reject",
		Name:      "counter",
		Help:      "Counter for rejected sentryAlerts",
	})
	sentryAlertForwardCounter := prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "sentry_proxy",
		Subsystem: "forward",
		Name:      "counter",
		Help:      "Counter for forwarded sentryAlerts",
	})

	registerer.MustRegister(
		sentryAlertTotalCounter,
		sentryAlertRejectCounter,
		sentryAlertForwardCounter,
	)

	return &metrics{
		sentryAlertTotalCounter:   sentryAlertTotalCounter,
		sentryAlertRejectCounter:  sentryAlertRejectCounter,
		sentryAlertForwardCounter: sentryAlertForwardCounter,
	}
}

type metrics struct {
	sentryAlertForwardCounter prometheus.Gauge
	sentryAlertRejectCounter  prometheus.Gauge
	sentryAlertTotalCounter   prometheus.Gauge
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
