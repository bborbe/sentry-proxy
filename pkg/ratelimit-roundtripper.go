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
// within any sliding requestDuration window, answering 429 once that window is
// full.
//
// Admission is a two-level decision evaluated under a single lock hold. The
// global window is checked first and is unchanged: at most requestLimit requests
// are forwarded in any requestDuration span. Only then is the per-project window
// consulted, rejecting the request when the requesting project already holds
// requestLimitPerProject entries in that same span. The per-project window is a
// second veto only — it never admits a request the global window would have
// refused, so the total allowance does not rise.
func NewRateLimitRoundTripper(
	currentTimeGetter libtime.CurrentTimeGetter,
	requestLimit int,
	requestLimitPerProject int,
	requestDuration time.Duration,
	metrics Metrics,
	producer Producer,
	roundTripper http.RoundTripper,
) http.RoundTripper {
	var mux sync.Mutex
	var timestamps []time.Time
	projects := newProjectWindows(
		requestLimitPerProject,
		requestDuration,
		defaultProjectSweepThreshold,
		defaultMaxProjects,
	)
	return libhttp.RoundTripperFunc(func(req *http.Request) (*http.Response, error) {
		ctx := req.Context()
		metrics.SentryAlertTotalInc()

		now := currentTimeGetter.Now()
		body, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, errors.Wrap(ctx, err, "read body failed")
		}
		req.Body = io.NopCloser(bytes.NewReader(body))

		project := extractProject(req.URL.Path)
		outcome := "forwarded"

		var response *http.Response
		rejectScope := ""
		mux.Lock()
		// prune leading window entries that are at least one full window old; an
		// entry exactly requestDuration old drops out, so a request arriving at
		// the boundary is allowed through.
		pruned := 0
		for pruned < len(timestamps) && now.Sub(timestamps[pruned]) >= requestDuration {
			pruned++
		}
		timestamps = timestamps[pruned:]
		switch {
		case len(timestamps) >= requestLimit:
			rejectScope = "global"
			glog.Warningf("requestCounter(%d) >= limit(%d) => 429", len(timestamps), requestLimit)
		case !projects.admit(project, now):
			rejectScope = "project"
			glog.Warningf(
				"project(%s) reached requestLimitPerProject(%d) => 429",
				project,
				requestLimitPerProject,
			)
		default:
			timestamps = append(timestamps, now)
		}
		if rejectScope != "" {
			// a rejected request appends to neither window, so rejections never
			// extend the global or the per-project window.
			outcome = "rejected"
			metrics.SentryAlertLimitTrippedInc(rejectScope)
			metrics.SentryAlertRejectedInc()
			defer req.Body.Close()
			glog.Warningf("sentry alert rejected: %s", string(body))
			response = &http.Response{
				Body:       io.NopCloser(bytes.NewBufferString("reached request limit => 429")),
				StatusCode: http.StatusTooManyRequests,
			}
			mux.Unlock()
		} else {
			metrics.SentryAlertForwardInc()
			glog.V(4).Infof("increase requestCounter to %d", len(timestamps))
			mux.Unlock()
			response, err = roundTripper.RoundTrip(req)
			if err != nil {
				outcome = "upstream_error"
			}
		}

		producer.Publish(body, project, now, outcome)
		return response, err
	})
}
