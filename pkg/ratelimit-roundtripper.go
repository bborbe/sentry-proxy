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

// NewRateLimitRoundTripper prevents forwarding more than requestLimit requests
// within any sliding requestDuration window, answering 429 once the window is full.
func NewRateLimitRoundTripper(
	currentTimeGetter libtime.CurrentTimeGetter,
	requestLimit int,
	requestDuration time.Duration,
	metrics Metrics,
	roundTripper http.RoundTripper,
) http.RoundTripper {
	var mux sync.Mutex
	var timestamps []time.Time
	return libhttp.RoundTripperFunc(func(req *http.Request) (*http.Response, error) {
		ctx := req.Context()
		metrics.SentryAlertTotalInc()
		mux.Lock()
		now := currentTimeGetter.Now()
		// prune leading window entries that are at least one full window old; an
		// entry exactly requestDuration old drops out, so a request arriving at
		// the boundary is allowed through.
		pruned := 0
		for pruned < len(timestamps) && now.Sub(timestamps[pruned]) >= requestDuration {
			pruned++
		}
		timestamps = timestamps[pruned:]
		if len(timestamps) >= requestLimit {
			glog.Warningf("requestCounter(%d) >= limit(%d) => 429", len(timestamps), requestLimit)
			mux.Unlock()
			metrics.SentryAlertRejectedInc()
			defer req.Body.Close()
			body, err := io.ReadAll(req.Body)
			if err != nil {
				return nil, errors.Wrap(ctx, err, "read body failed")
			}
			glog.Warningf("sentry alert rejected: %s", string(body))
			return &http.Response{
				Body:       io.NopCloser(bytes.NewBufferString("reached request limit => 429")),
				StatusCode: http.StatusTooManyRequests,
			}, nil
		}
		timestamps = append(timestamps, now)
		metrics.SentryAlertForwardInc()
		glog.V(4).Infof("increase requestCounter to %d", len(timestamps))
		mux.Unlock()
		return roundTripper.RoundTrip(req)
	})
}
