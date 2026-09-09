// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"time"

	"github.com/IBM/sarama"
	"github.com/bborbe/errors"
	libhttp "github.com/bborbe/http"
	libkafka "github.com/bborbe/kafka"
	kafkamocks "github.com/bborbe/kafka/mocks"
	libtime "github.com/bborbe/time"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/sentry-proxy/mocks"
	"github.com/bborbe/sentry-proxy/pkg"
)

var base = time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)

var _ = Describe("RateLimitRoundTripper", func() {
	It("AC1: publishes forwarded alerts with the buffered body and project", func() {
		fake := &mocks.Producer{}
		currentTime := libtime.NewCurrentTime()
		currentTime.SetNow(base)
		var receivedBodies [][]byte
		server, innerRT := newUpstream(&receivedBodies)
		defer server.Close()
		roundTripper := newRateLimitRoundTripper(currentTime, fake, innerRT)
		currentTime.SetNow(base.Add(2 * time.Hour))

		resp, err := roundTripper.RoundTrip(newTestRequest("/api/project-a/envelope/"))
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		resp.Body.Close()

		Expect(fake.PublishCallCount()).To(Equal(1))
		body, project, _, outcome := fake.PublishArgsForCall(0)
		Expect(body).To(Equal([]byte("envelope-bytes")))
		Expect(project).To(Equal("project-a"))
		Expect(outcome).To(Equal("forwarded"))
		Expect(receivedBodies).To(Equal([][]byte{[]byte("envelope-bytes")}))
	})

	It(
		"AC2: publishes rejected alerts with the byte-for-byte body and frozen 429 response",
		func() {
			fake := &mocks.Producer{}
			currentTime := libtime.NewCurrentTime()
			currentTime.SetNow(base)
			server, innerRT := newUpstream(nil)
			defer server.Close()
			// Sliding-window semantics forward the first request at startup, so
			// the single-slot window must be filled before the reject path is
			// exercised (the old cumulative-uptime "first request rejected"
			// premise no longer holds after the sliding-window fix).
			roundTripper := pkg.NewRateLimitRoundTripper(
				currentTime,
				1,
				time.Hour,
				&mocks.Metrics{},
				fake,
				innerRT,
			)

			// fill the window (forwarded)
			resp0, err := roundTripper.RoundTrip(newTestRequest("/api/project-a/envelope/"))
			Expect(err).NotTo(HaveOccurred())
			Expect(resp0.StatusCode).To(Equal(http.StatusOK))
			resp0.Body.Close()

			// rejected: frozen 429 body, record published with outcome "rejected"
			resp, err := roundTripper.RoundTrip(newTestRequest("/api/project-a/envelope/"))
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.StatusCode).To(Equal(http.StatusTooManyRequests))
			respBody, err := io.ReadAll(resp.Body)
			Expect(err).NotTo(HaveOccurred())
			resp.Body.Close()
			Expect(respBody).To(Equal([]byte("reached request limit => 429")))

			Expect(fake.PublishCallCount()).To(Equal(2))
			body, project, _, outcome := fake.PublishArgsForCall(1)
			Expect(body).To(Equal([]byte("envelope-bytes")))
			Expect(project).To(Equal("project-a"))
			Expect(outcome).To(Equal("rejected"))
		},
	)

	It("AC3: publishes exactly one record per request, never two", func() {
		fake := &mocks.Producer{}
		currentTime := libtime.NewCurrentTime()
		currentTime.SetNow(base)
		var receivedBodies [][]byte
		server, innerRT := newUpstream(&receivedBodies)
		defer server.Close()
		roundTripper := newRateLimitRoundTripper(currentTime, fake, innerRT)
		currentTime.SetNow(base.Add(2 * time.Hour))

		for i := 0; i < 3; i++ {
			resp, err := roundTripper.RoundTrip(newTestRequest("/api/project-a/envelope/"))
			Expect(err).NotTo(HaveOccurred())
			resp.Body.Close()
		}

		Expect(fake.PublishCallCount()).To(Equal(3))
		Expect(receivedBodies).To(HaveLen(3))
	})

	It("AC4: a Kafka publish failure does not prevent the alert reaching Sentry", func() {
		var receivedBodies [][]byte
		server, innerRT := newUpstream(&receivedBodies)
		defer server.Close()

		failingFake := &kafkamocks.KafkaJSONSender{}
		failingFake.SendUpdateReturns(errors.New(context.Background(), "kafka down"))
		statusA, bodyA, headerA, elapsedA := driveResponse(innerRT, failingFake)

		succeedingFake := &kafkamocks.KafkaJSONSender{}
		succeedingFake.SendUpdateReturns(nil)
		statusB, bodyB, headerB, elapsedB := driveResponse(innerRT, succeedingFake)

		Expect(elapsedA).To(BeNumerically("<", 100*time.Millisecond))
		Expect(elapsedB).To(BeNumerically("<", 100*time.Millisecond))
		Expect(statusA).To(Equal(statusB))
		Expect(bodyA).To(Equal(bodyB))
		Expect(normalizedHeader(headerA)).To(Equal(normalizedHeader(headerB)))
		Expect(
			receivedBodies,
		).To(Equal([][]byte{[]byte("envelope-bytes"), []byte("envelope-bytes")}))
	})

	It("AC5: an upstream failure does not prevent the publish", func() {
		fake := &mocks.Producer{}
		currentTime := libtime.NewCurrentTime()
		currentTime.SetNow(base)
		innerRT := libhttp.RoundTripperFunc(func(req *http.Request) (*http.Response, error) {
			return nil, errors.New(req.Context(), "connection refused")
		})
		roundTripper := newRateLimitRoundTripper(currentTime, fake, innerRT)
		currentTime.SetNow(base.Add(2 * time.Hour))

		resp, err := roundTripper.RoundTrip(newTestRequest("/api/project-a/envelope/"))
		Expect(resp).To(BeNil())
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("connection refused"))

		Expect(fake.PublishCallCount()).To(Equal(1))
		body, project, _, outcome := fake.PublishArgsForCall(0)
		Expect(body).To(Equal([]byte("envelope-bytes")))
		Expect(project).To(Equal("project-a"))
		Expect(outcome).To(Equal("upstream_error"))
	})

	It("AC11: a stalled broker does not stall the request path", func() {
		currentTime := libtime.NewCurrentTime()
		currentTime.SetNow(base)
		fake := &kafkamocks.KafkaJSONSender{}
		fake.SendUpdateCalls(
			func(_ context.Context, _ libkafka.Topic, _ libkafka.Key, _ libkafka.Value, _ ...sarama.RecordHeader) error {
				time.Sleep(2 * time.Second)
				return nil
			},
		)
		producer := pkg.NewProducer(
			func(ctx context.Context) (libkafka.JSONSender, error) { return fake, nil },
			libkafka.Topic("develop-raw-sentry-alert-input"),
			&mocks.Metrics{},
			10,
		)
		runCtx, runCancel := context.WithCancel(context.Background())
		defer runCancel()
		go func() {
			_ = producer.Run(runCtx)
		}()
		producer.Publish([]byte("first"), "project-a", base, "forwarded")

		server, innerRT := newUpstream(nil)
		defer server.Close()
		roundTripper := newRateLimitRoundTripper(currentTime, producer, innerRT)
		currentTime.SetNow(base.Add(2 * time.Hour))

		start := time.Now()
		resp, err := roundTripper.RoundTrip(newTestRequest("/api/project-a/envelope/"))
		elapsed := time.Since(start)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		resp.Body.Close()
		Expect(elapsed).To(BeNumerically("<", 100*time.Millisecond))
	})

	It("returns the wrapped body read error without publishing", func() {
		fake := &mocks.Producer{}
		currentTime := libtime.NewCurrentTime()
		currentTime.SetNow(base)
		roundTripper := newRateLimitRoundTripper(currentTime, fake, http.DefaultTransport)

		req := newTestRequest("/api/project-a/envelope/")
		req.Body = io.NopCloser(errReader{})
		resp, err := roundTripper.RoundTrip(req)
		Expect(resp).To(BeNil())
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("read body failed"))
		Expect(fake.PublishCallCount()).To(Equal(0))
	})

	It("extracts the project from the ingest path, falling back to unknown", func() {
		fake := &mocks.Producer{}
		currentTime := libtime.NewCurrentTime()
		currentTime.SetNow(base)
		var receivedBodies [][]byte
		server, innerRT := newUpstream(&receivedBodies)
		defer server.Close()
		roundTripper := newRateLimitRoundTripper(currentTime, fake, innerRT)
		currentTime.SetNow(base.Add(2 * time.Hour))

		for _, path := range []string{"/api/project-a/envelope/", "/api/", "/other/path"} {
			resp, err := roundTripper.RoundTrip(newTestRequest(path))
			Expect(err).NotTo(HaveOccurred())
			resp.Body.Close()
		}

		Expect(fake.PublishCallCount()).To(Equal(3))
		_, project0, _, _ := fake.PublishArgsForCall(0)
		Expect(project0).To(Equal("project-a"))
		_, project1, _, _ := fake.PublishArgsForCall(1)
		Expect(project1).To(Equal("unknown"))
		_, project2, _, _ := fake.PublishArgsForCall(2)
		Expect(project2).To(Equal("unknown"))
	})

	It("publishes a record for a non-matching path with project unknown", func() {
		fake := &mocks.Producer{}
		currentTime := libtime.NewCurrentTime()
		currentTime.SetNow(base)
		server, innerRT := newUpstream(nil)
		defer server.Close()
		roundTripper := newRateLimitRoundTripper(currentTime, fake, innerRT)
		currentTime.SetNow(base.Add(2 * time.Hour))

		resp, err := roundTripper.RoundTrip(newTestRequest("/other"))
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		resp.Body.Close()

		Expect(fake.PublishCallCount()).To(Equal(1))
		_, project, _, outcome := fake.PublishArgsForCall(0)
		Expect(project).To(Equal("unknown"))
		Expect(outcome).To(Equal("forwarded"))
	})
})

// newTestRequest builds a manually constructed ingest request; its Context()
// returns context.Background, which the round tripper closure requires.
func newTestRequest(path string) *http.Request {
	return &http.Request{
		Method: http.MethodPost,
		URL:    &url.URL{Path: path},
		Body:   io.NopCloser(bytes.NewBufferString("envelope-bytes")),
		Header: http.Header{},
	}
}

// newRateLimitRoundTripper wires a fixed budget of 100 requests/hour around
// the given producer and inner round tripper, with no-op metrics.
func newRateLimitRoundTripper(
	currentTime libtime.CurrentTimeGetter,
	producer pkg.Producer,
	innerRT http.RoundTripper,
) http.RoundTripper {
	return pkg.NewRateLimitRoundTripper(
		currentTime,
		100,
		time.Hour,
		&mocks.Metrics{},
		producer,
		innerRT,
	)
}

// newUpstream returns an httptest server (which must be closed by the caller)
// and a round tripper that rewrites the request scheme/host onto it, recording
// every received request body when received is non-nil.
func newUpstream(received *[][]byte) (*httptest.Server, http.RoundTripper) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if received != nil {
			*received = append(*received, body)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("upstream-ok"))
	}))
	serverURL, _ := url.Parse(server.URL)
	innerRT := libhttp.RoundTripperFunc(func(req *http.Request) (*http.Response, error) {
		req.URL.Scheme = serverURL.Scheme
		req.URL.Host = serverURL.Host
		return http.DefaultTransport.RoundTrip(req)
	})
	return server, innerRT
}

// driveResponse runs one forwarded request through a round tripper backed by a
// real producer whose sender is the given fake, and returns the upstream
// response (status, body, headers) plus the RoundTrip elapsed time.
func driveResponse(
	innerRT http.RoundTripper,
	sender libkafka.JSONSender,
) (int, []byte, http.Header, time.Duration) {
	currentTime := libtime.NewCurrentTime()
	currentTime.SetNow(base)
	producer := pkg.NewProducer(
		func(ctx context.Context) (libkafka.JSONSender, error) { return sender, nil },
		libkafka.Topic("develop-raw-sentry-alert-input"),
		&mocks.Metrics{},
		10,
	)
	runCtx, runCancel := context.WithCancel(context.Background())
	defer runCancel()
	go func() {
		_ = producer.Run(runCtx)
	}()
	roundTripper := newRateLimitRoundTripper(currentTime, producer, innerRT)
	currentTime.SetNow(base.Add(2 * time.Hour))

	start := time.Now()
	resp, err := roundTripper.RoundTrip(newTestRequest("/api/project-a/envelope/"))
	elapsed := time.Since(start)
	Expect(err).NotTo(HaveOccurred())
	Expect(resp.StatusCode).To(Equal(http.StatusOK))
	respBody, err := io.ReadAll(resp.Body)
	Expect(err).NotTo(HaveOccurred())
	resp.Body.Close()
	return resp.StatusCode, respBody, resp.Header, elapsed
}

// normalizedHeader returns a copy of h without the Date header, which the
// server stamps from wall clock time and may differ between two sequential
// responses that straddle a second boundary.
func normalizedHeader(h http.Header) http.Header {
	out := http.Header{}
	for key, values := range h {
		if key == "Date" {
			continue
		}
		out[key] = values
	}
	return out
}

// errReader is a reader whose every read fails, exercising the top-of-closure
// body buffering failure mode.
type errReader struct{}

func (errReader) Read([]byte) (int, error) {
	return 0, errors.New(context.Background(), "read boom")
}

var _ = Describe("RateLimitRoundTripper sliding window", func() {
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

	newSlidingWindowRoundTripper := func(limit int) http.RoundTripper {
		return pkg.NewRateLimitRoundTripper(
			currentTime,
			limit,
			1*time.Hour,
			fakeMetrics,
			&mocks.Producer{},
			upstream,
		)
	}

	Describe("first-minute allowance", func() {
		BeforeEach(func() {
			roundTripper = newSlidingWindowRoundTripper(5)
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
			roundTripper = newSlidingWindowRoundTripper(2)
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
			roundTripper = newSlidingWindowRoundTripper(5)
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
			roundTripper = newSlidingWindowRoundTripper(2)
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
})
