// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"github.com/bborbe/sentry-proxy/pkg"
)

var _ = Describe("Metrics", func() {
	var registry *prometheus.Registry
	var metrics pkg.Metrics

	BeforeEach(func() {
		registry = prometheus.NewRegistry()
		metrics = pkg.NewMetrics(registry)
	})

	It("exposes every metric as a counter with a _total name", func() {
		families, err := registry.Gather()
		Expect(err).NotTo(HaveOccurred())
		names := make([]string, 0, len(families))
		for _, family := range families {
			Expect(family.GetName()).To(HaveSuffix("_total"))
			Expect(family.GetType()).To(Equal(dto.MetricType_COUNTER))
			names = append(names, family.GetName())
		}
		Expect(names).To(ConsistOf(
			"sentry_proxy_alerts_total",
			"sentry_proxy_alerts_rejected_total",
			"sentry_proxy_alerts_forwarded_total",
			"sentry_proxy_kafka_publishes_total",
		))
	})

	It("pre-initializes every kafka publish result series to zero", func() {
		Expect(
			metricValue(registry, "sentry_proxy_kafka_publishes_total", "success"),
		).To(Equal(0.0))
		Expect(
			metricValue(registry, "sentry_proxy_kafka_publishes_total", "failure"),
		).To(Equal(0.0))
		Expect(
			metricValue(registry, "sentry_proxy_kafka_publishes_total", "dropped"),
		).To(Equal(0.0))
	})

	It("increments every alert counter through the real registry", func() {
		metrics.SentryAlertTotalInc()
		metrics.SentryAlertRejectedInc()
		metrics.SentryAlertForwardInc()

		Expect(
			alertCounterValue(registry, "sentry_proxy_alerts_total"),
		).To(Equal(1.0))
		Expect(
			alertCounterValue(registry, "sentry_proxy_alerts_rejected_total"),
		).To(Equal(1.0))
		Expect(
			alertCounterValue(registry, "sentry_proxy_alerts_forwarded_total"),
		).To(Equal(1.0))
	})
})

// alertCounterValue returns the value of the single metric in the named
// label-less family, or -1 when the family or its metric is absent.
func alertCounterValue(registry *prometheus.Registry, familyName string) float64 {
	families, err := registry.Gather()
	Expect(err).NotTo(HaveOccurred())
	for _, family := range families {
		if family.GetName() != familyName {
			continue
		}
		metricsList := family.GetMetric()
		if len(metricsList) != 1 {
			return -1
		}
		return metricsList[0].GetCounter().GetValue()
	}
	return -1
}
