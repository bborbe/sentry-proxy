// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"strings"

	"github.com/golang/glog"
)

// gzipMagic is the two-byte header every gzip stream starts with.
var gzipMagic = []byte{0x1f, 0x8b}

// teeBody returns the bytes the Kafka tee should publish for one request body.
//
// Sentry SDKs gzip their envelope POSTs and set Content-Encoding: gzip. The tee
// reads the body verbatim, and the record carries it as a JSON string — so the
// compressed stream reaches the topic only after encoding/json has coerced
// every invalid-UTF-8 byte to U+FFFD. That coercion is lossy and irreversible:
// the gzip header's 0x8b becomes the three bytes of U+FFFD, and no consumer can
// ever decode the record. Measured on develop-raw-sentry-alert-input offset
// 1425, a forwarded alert: the body read `1f ef bf bd 08 00` where a gzip
// header `1f 8b 08 00` belongs, and gunzip failed on it. sentry-alert-consumer
// then failed every such record with `parse envelope header: invalid character
// '\x1f' looking for beginning of value` and never enriched it.
//
// Decompressing here, on the tee's own copy, puts the plain envelope on the
// topic. req.Body is untouched, so the upstream still receives the original
// compressed bytes and nothing about what Sentry sees changes.
//
// Best-effort by contract: a body that is not gzip, or that fails to
// decompress, is published unchanged. The tee must never drop a record over an
// encoding it did not recognise, and a wrong guess must not corrupt one either.
func teeBody(header http.Header, body []byte) []byte {
	if !isGzip(header, body) {
		return body
	}
	reader, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		glog.Warningf("gunzip tee body failed, publishing as-is err=%v", err)
		return body
	}
	defer reader.Close()
	decoded, err := io.ReadAll(reader)
	if err != nil {
		glog.Warningf("gunzip tee body failed, publishing as-is err=%v", err)
		return body
	}
	return decoded
}

// isGzip reports whether a request body is gzip-encoded. The Content-Encoding
// header is checked first because it is what the sender declared; the magic
// catches a sender that compressed without declaring it, which the tee
// otherwise has no way to notice.
func isGzip(header http.Header, body []byte) bool {
	if strings.Contains(strings.ToLower(header.Get("Content-Encoding")), "gzip") {
		return true
	}
	return len(body) >= len(gzipMagic) && bytes.Equal(body[:len(gzipMagic)], gzipMagic)
}
