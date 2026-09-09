// Copyright (c) 2024 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package factory

import (
	"context"
	"net/http"
	"net/url"
	"time"

	"github.com/bborbe/errors"
	libhttp "github.com/bborbe/http"
	libkafka "github.com/bborbe/kafka"
	"github.com/bborbe/log"
	libsentry "github.com/bborbe/sentry"
	libtime "github.com/bborbe/time"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/bborbe/sentry-proxy/pkg"
)

func CreateMetrics(registerer prometheus.Registerer) pkg.Metrics {
	return pkg.NewMetrics(registerer)
}

func CreateProducer(
	brokers libkafka.Brokers,
	topic libkafka.Topic,
	metrics pkg.Metrics,
) pkg.Producer {
	return pkg.NewProducer(
		func(ctx context.Context) (libkafka.JSONSender, error) {
			syncProducer, err := libkafka.NewSyncProducer(ctx, brokers)
			if err != nil {
				return nil, errors.Wrapf(ctx, err, "create kafka sync producer failed")
			}
			return libkafka.NewJSONSender(syncProducer, log.DefaultSamplerFactory), nil
		},
		topic,
		metrics,
		0,
	)
}

func CreateRoundTripper(
	metrics pkg.Metrics,
	currentTime libtime.CurrentTimeGetter,
	requestLimit int,
	requestDuration time.Duration,
	producer pkg.Producer,
) http.RoundTripper {
	return pkg.NewRateLimitRoundTripper(
		currentTime,
		requestLimit,
		requestDuration,
		metrics,
		producer,
		libhttp.CreateDefaultRoundTripper(),
	)
}

func CreateProxyHandler(
	metrics pkg.Metrics,
	sentryClient libsentry.Client,
	currentTime libtime.CurrentTimeGetter,
	requestLimit int,
	requestDuration time.Duration,
	parsedURL *url.URL,
	producer pkg.Producer,
) http.Handler {
	return libhttp.NewProxy(
		CreateRoundTripper(metrics, currentTime, requestLimit, requestDuration, producer),
		parsedURL,
		libhttp.NewSentryProxyErrorHandler(sentryClient),
	)
}
