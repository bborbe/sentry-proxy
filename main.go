// Copyright (c) 2023 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/bborbe/errors"
	libhttp "github.com/bborbe/http"
	"github.com/bborbe/run"
	libsentry "github.com/bborbe/sentry"
	"github.com/bborbe/service"
	libtime "github.com/bborbe/time"
	"github.com/golang/glog"
	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/bborbe/sentry-proxy/pkg"
	"github.com/bborbe/sentry-proxy/pkg/factory"
	libmetrics "github.com/bborbe/sentry-proxy/pkg/metrics"
)

func main() {
	app := &application{}
	os.Exit(service.Main(context.Background(), app, &app.SentryDSN, &app.SentryProxy))
}

type application struct {
	SentryDSN       string            `required:"true"  arg:"sentry-dsn"       env:"SENTRY_DSN"       usage:"SentryDSN"                 display:"length"`
	SentryProxy     string            `required:"false" arg:"sentry-proxy"     env:"SENTRY_PROXY"     usage:"Sentry Proxy"`
	Listen          string            `required:"true"  arg:"listen"           env:"LISTEN"           usage:"address to listen to"`
	RequestLimit    int               `required:"true"  arg:"request-limit"    env:"REQUEST_LIMIT"    usage:"request limit"`
	RequestDuration time.Duration     `required:"true"  arg:"request-duration" env:"REQUEST_DURATION" usage:"request limit duration"`
	BuildGitCommit  string            `required:"false" arg:"build-git-commit" env:"BUILD_GIT_COMMIT" usage:"Build Git commit hash"                      default:"none"`
	BuildDate       *libtime.DateTime `required:"false" arg:"build-date"       env:"BUILD_DATE"       usage:"Build timestamp (RFC3339)"`
}

func (a *application) Run(ctx context.Context, sentryClient libsentry.Client) error {
	libmetrics.NewBuildInfoMetrics().SetBuildInfo(a.BuildDate)

	currentTime := libtime.NewCurrentTime()
	metrics := factory.CreateMetrics(prometheus.DefaultRegisterer)

	return service.Run(
		ctx,
		a.createHTTPServer(sentryClient, metrics, currentTime),
	)
}

func (a *application) createHTTPServer(
	sentryClient libsentry.Client,
	metrics pkg.Metrics,
	currentTime libtime.CurrentTimeGetter,
) run.Func {
	return func(ctx context.Context) error {
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()

		router := mux.NewRouter()
		router.Path("/healthz").Handler(libhttp.NewPrintHandler("OK"))
		router.Path("/readiness").Handler(libhttp.NewPrintHandler("OK"))
		router.Path("/metrics").Handler(promhttp.Handler())
		router.Path("/setloglevel/{level}").Handler(factory.CreateSetLoglevelHandler(ctx))
		parsedURL, err := url.Parse(a.SentryDSN)
		if err != nil {
			return errors.Wrapf(ctx, err, "parse sentryDSN failed")
		}
		parsedURL.Path = ""

		router.PathPrefix("/api").Handler(
			factory.CreateProxyHandler(
				metrics,
				sentryClient,
				currentTime,
				a.RequestLimit,
				a.RequestDuration,
				parsedURL,
			),
		)

		router.NotFoundHandler = http.HandlerFunc(
			func(writer http.ResponseWriter, request *http.Request) {
				glog.V(2).Infof("not found %s %s", request.Method, request.URL.Path)
			},
		)

		glog.V(2).Infof("starting http server listen on %s", a.Listen)
		return libhttp.NewServer(
			a.Listen,
			router,
		).Run(ctx)
	}
}
