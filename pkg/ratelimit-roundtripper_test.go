// Copyright (c) 2024 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"time"

	libhttp "github.com/bborbe/http"
	libtime "github.com/bborbe/time"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/sentry-proxy/mocks"
	"github.com/bborbe/sentry-proxy/pkg"
)

type failingReadCloser struct{}

func (failingReadCloser) Read([]byte) (int, error) {
	return 0, errors.New("read failed")
}

func (failingReadCloser) Close() error {
	return nil
}

var _ = Describe("RateLimitRoundTripper", func() {
	var (
		baseTime     time.Time
		currentTime  libtime.CurrentTime
		fakeMetrics  *mocks.Metrics
		upstream     http.RoundTripper
		roundTripper http.RoundTripper
	)

	BeforeEach(func() {
		baseTime = time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
		currentTime = libtime.NewCurrentTime()
		currentTime.SetNow(baseTime)
		fakeMetrics = &mocks.Metrics{}
		upstream = libhttp.RoundTripperFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewBufferString("ok")),
			}, nil
		})
	})

	newEnvelopeRequest := func() *http.Request {
		req, err := http.NewRequest(
			http.MethodPost,
			"https://example.com/api/123/envelope/",
			bytes.NewBufferString("sentry envelope body"),
		)
		Expect(err).NotTo(HaveOccurred())
		return req
	}

	doRequest := func() *http.Response {
		resp, err := roundTripper.RoundTrip(newEnvelopeRequest())
		Expect(err).NotTo(HaveOccurred())
		return resp
	}

	Describe("first-minute allowance", func() {
		BeforeEach(func() {
			roundTripper = pkg.NewRateLimitRoundTripper(
				currentTime,
				5,
				1*time.Hour,
				fakeMetrics,
				upstream,
			)
		})

		It("forwards the first request at startup instead of rejecting it", func() {
			resp := doRequest()
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
			Expect(fakeMetrics.SentryAlertForwardIncCallCount()).To(Equal(1))
			Expect(fakeMetrics.SentryAlertRejectedIncCallCount()).To(Equal(0))
			Expect(fakeMetrics.SentryAlertTotalIncCallCount()).To(Equal(1))
		})
	})

	Describe("sliding-window expiry", func() {
		BeforeEach(func() {
			roundTripper = pkg.NewRateLimitRoundTripper(
				currentTime,
				2,
				1*time.Hour,
				fakeMetrics,
				upstream,
			)
		})

		It("prunes entries exactly one window old and forwards again", func() {
			Expect(doRequest().StatusCode).To(Equal(http.StatusOK))
			Expect(doRequest().StatusCode).To(Equal(http.StatusOK))

			rejected := doRequest()
			Expect(rejected.StatusCode).To(Equal(http.StatusTooManyRequests))
			Expect(fakeMetrics.SentryAlertRejectedIncCallCount()).To(Equal(1))

			currentTime.SetNow(baseTime.Add(1 * time.Hour))
			Expect(doRequest().StatusCode).To(Equal(http.StatusOK))
			Expect(fakeMetrics.SentryAlertForwardIncCallCount()).To(Equal(3))
			Expect(fakeMetrics.SentryAlertRejectedIncCallCount()).To(Equal(1))
		})
	})

	Describe("burst-then-quiet recovery", func() {
		BeforeEach(func() {
			roundTripper = pkg.NewRateLimitRoundTripper(
				currentTime,
				5,
				1*time.Hour,
				fakeMetrics,
				upstream,
			)
		})

		It("recovers after a full burst window expires", func() {
			for range 5 {
				Expect(doRequest().StatusCode).To(Equal(http.StatusOK))
			}
			Expect(doRequest().StatusCode).To(Equal(http.StatusTooManyRequests))

			currentTime.SetNow(baseTime.Add(1 * time.Hour))
			Expect(doRequest().StatusCode).To(Equal(http.StatusOK))

			Expect(fakeMetrics.SentryAlertForwardIncCallCount()).To(Equal(6))
			Expect(fakeMetrics.SentryAlertRejectedIncCallCount()).To(Equal(1))
		})
	})

	Describe("boundary-straddling burst", func() {
		BeforeEach(func() {
			roundTripper = pkg.NewRateLimitRoundTripper(
				currentTime,
				2,
				1*time.Hour,
				fakeMetrics,
				upstream,
			)
		})

		It("never forwards more than requestLimit in any window span", func() {
			// 12:00 — window empty, both forwarded
			Expect(doRequest().StatusCode).To(Equal(http.StatusOK))
			Expect(doRequest().StatusCode).To(Equal(http.StatusOK))

			// 12:59 — 12:00 entries still in the window, both rejected
			currentTime.SetNow(baseTime.Add(59 * time.Minute))
			Expect(doRequest().StatusCode).To(Equal(http.StatusTooManyRequests))
			Expect(doRequest().StatusCode).To(Equal(http.StatusTooManyRequests))

			// 13:00 — 12:00 entries are exactly one window old and pruned, both forwarded
			currentTime.SetNow(baseTime.Add(1 * time.Hour))
			Expect(doRequest().StatusCode).To(Equal(http.StatusOK))
			Expect(doRequest().StatusCode).To(Equal(http.StatusOK))

			// 13:30 — 13:00 entries still in the window, both rejected
			currentTime.SetNow(baseTime.Add(90 * time.Minute))
			Expect(doRequest().StatusCode).To(Equal(http.StatusTooManyRequests))
			Expect(doRequest().StatusCode).To(Equal(http.StatusTooManyRequests))

			Expect(fakeMetrics.SentryAlertForwardIncCallCount()).To(Equal(4))
			Expect(fakeMetrics.SentryAlertRejectedIncCallCount()).To(Equal(4))
		})
	})

	Context("rejection body read failure", func() {
		BeforeEach(func() {
			roundTripper = pkg.NewRateLimitRoundTripper(
				currentTime,
				1,
				1*time.Hour,
				fakeMetrics,
				upstream,
			)
		})

		It("returns a wrapped error when reading the rejected body fails", func() {
			// fill the single-slot window so the next request is rejected
			Expect(doRequest().StatusCode).To(Equal(http.StatusOK))

			req, err := http.NewRequest(
				http.MethodPost,
				"https://example.com/api/123/envelope/",
				failingReadCloser{},
			)
			Expect(err).NotTo(HaveOccurred())

			resp, roundTripErr := roundTripper.RoundTrip(req)
			Expect(resp).To(BeNil())
			Expect(roundTripErr).To(MatchError(ContainSubstring("read body failed")))
			Expect(fakeMetrics.SentryAlertRejectedIncCallCount()).To(Equal(1))
		})
	})
})
