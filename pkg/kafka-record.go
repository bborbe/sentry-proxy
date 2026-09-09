// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"context"

	"github.com/bborbe/errors"
	"github.com/bborbe/validation"
)

// KafkaAlertRecord is the JSON record published to the Kafka topic for every
// alert the proxy receives. It is a durable contract; consumers parse it
// verbatim. Schema is documented in docs/kafka-alert-record.md.
type KafkaAlertRecord struct {
	Body       string `json:"body"`
	Project    string `json:"project"`
	ReceivedAt string `json:"received_at"`
	Outcome    string `json:"outcome"`
}

// Validate checks that the Outcome is one of the documented values. Body,
// project and received_at are carried verbatim and are not validated.
func (r KafkaAlertRecord) Validate(ctx context.Context) error {
	switch r.Outcome {
	case "forwarded", "rejected", "upstream_error":
		return nil
	default:
		return errors.Wrapf(ctx, validation.Error, "invalid outcome %q", r.Outcome)
	}
}
