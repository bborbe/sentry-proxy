// Copyright (c) 2024 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"bytes"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/bborbe/errors"
	libhttp "github.com/bborbe/http"
	libtime "github.com/bborbe/time"
	"github.com/golang/glog"
)

// NewRateLimitRoundTripper prevent request if more than requestLimit
func NewRateLimitRoundTripper(
	currentTimeGetter libtime.CurrentTimeGetter,
	requestLimit int,
	requestDuration time.Duration,
	metrics Metrics,
	roundTripper http.RoundTripper,
) http.RoundTripper {
	var mux sync.Mutex
	var requestCounter uint64
	started := currentTimeGetter.Now()
	return libhttp.RoundTripperFunc(func(req *http.Request) (*http.Response, error) {
		ctx := req.Context()
		metrics.SentryAlertTotalInc()
		uptime := currentTimeGetter.Now().Sub(started)
		limit := uint64(float64(requestLimit) * float64(uptime/requestDuration))
		glog.V(4).
			Infof("requestLimit(%d) * uptime(%v)/requestDuration(%v) = %d", requestLimit, uptime, requestDuration, limit)
		if requestCounter >= limit {
			glog.V(2).Infof("requestCounter(%d) >= limit(%d) => 429", requestCounter, limit)
			metrics.SentryAlertRejectedInc()
			defer req.Body.Close()
			body, err := io.ReadAll(req.Body)
			if err != nil {
				return nil, errors.Wrap(ctx, err, "read body failed")
			}
			glog.V(2).Infof("sentry alert rejected: %s", string(body))
			return &http.Response{
				Body:       io.NopCloser(bytes.NewBufferString("reached request limit => 429")),
				StatusCode: http.StatusTooManyRequests,
			}, nil
		}
		mux.Lock()
		metrics.SentryAlertForwardInc()
		requestCounter = requestCounter + 1
		glog.V(4).Infof("increase requestCounter to %d", requestCounter)
		mux.Unlock()
		return roundTripper.RoundTrip(req)
	})
}
