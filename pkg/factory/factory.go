// Copyright (c) 2024 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package factory

import (
	"net/http"
	"net/url"
	"time"

	libhttp "github.com/bborbe/http"
	libsentry "github.com/bborbe/sentry"
	libtime "github.com/bborbe/time"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/bborbe/sentry-proxy/pkg"
)

func CreateMetrics(registerer prometheus.Registerer) pkg.Metrics {
	return pkg.NewMetrics(registerer)
}

func CreateRoundTripper(
	metrics pkg.Metrics,
	currentTime libtime.CurrentTimeGetter,
	requestLimit int,
	requestDuration time.Duration,
) http.RoundTripper {
	return pkg.NewRateLimitRoundTripper(
		currentTime,
		requestLimit,
		requestDuration,
		metrics,
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
) http.Handler {
	return libhttp.NewProxy(
		CreateRoundTripper(metrics, currentTime, requestLimit, requestDuration),
		parsedURL,
		libhttp.NewSentryProxyErrorHandler(sentryClient),
	)
}
