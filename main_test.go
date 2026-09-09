// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestAC8MissingKafkaConfigFailsStartup(t *testing.T) {
	if os.Getenv("SENTRY_PROXY_AC8_HELPER") == "1" {
		main()
		return
	}

	for _, tc := range []struct {
		missing    string
		wantOutput string
	}{
		{missing: "KAFKA_BROKERS", wantOutput: "KAFKA_BROKERS"},
		{missing: "KAFKA_TOPIC", wantOutput: "KAFKA_TOPIC"},
	} {
		t.Run(tc.missing, func(t *testing.T) {
			extra := "KAFKA_TOPIC=develop-raw-sentry-alert-input"
			if tc.missing == "KAFKA_TOPIC" {
				extra = "KAFKA_BROKERS=localhost:9092"
			}
			out := runAC8Subprocess(t, extra)
			if !bytes.Contains(out, []byte(tc.wantOutput)) {
				t.Fatalf("expected output to mention %q, got: %s", tc.wantOutput, out)
			}
		})
	}

	t.Run("KAFKA_TOPIC-invalid", func(t *testing.T) {
		out := runAC8Subprocess(t, "KAFKA_BROKERS=localhost:9092", "KAFKA_TOPIC=invalid topic!!")
		if !bytes.Contains(out, []byte("validate kafka topic failed")) {
			t.Fatalf("expected output to mention %q, got: %s", "validate kafka topic failed", out)
		}
	})
}

// runAC8Subprocess re-executes the current test binary in helper mode, stripping
// both kafka env vars from the inherited environment so only the vars supplied
// via extraEnv are effective. It asserts the subprocess exits non-zero (the
// service.Main argument-parsing failure path) and returns its combined output.
func runAC8Subprocess(t *testing.T, extraEnv ...string) []byte {
	t.Helper()
	// #nosec G204, G702 -- re-exec of the current test binary; os.Args[0] and
	// the -test.run flag are trusted constants in this re-exec pattern
	cmd := exec.Command(os.Args[0], "-test.run=^TestAC8MissingKafkaConfigFailsStartup$")
	env := withoutEnv(withoutEnv(os.Environ(), "KAFKA_BROKERS"), "KAFKA_TOPIC")
	env = append(env, ac8BaseEnv...)
	env = append(env, extraEnv...)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected subprocess to exit non-zero, got err=%v output=%s", err, out)
	}
	if exitErr.ExitCode() == 0 {
		t.Fatalf("expected non-zero exit, got 0; output=%s", out)
	}
	return out
}

// ac8BaseEnv supplies every OTHER required flag so only the kafka flag under
// test is missing.
var ac8BaseEnv = []string{
	"SENTRY_DSN=https://00000000000000000000000000000000@ingest.example.com/1",
	"LISTEN=:9090",
	"REQUEST_LIMIT=100",
	"REQUEST_DURATION=1h",
	"SENTRY_PROXY_AC8_HELPER=1",
}

func withoutEnv(env []string, key string) []string {
	result := []string{}
	for _, entry := range env {
		if strings.HasPrefix(entry, key+"=") {
			continue
		}
		result = append(result, entry)
	}
	return result
}
