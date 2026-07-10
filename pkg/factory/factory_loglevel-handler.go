// Copyright (c) 2025 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package factory

import (
	"context"
	"net/http"
	"time"

	"github.com/bborbe/log"
)

// CreateSetLoglevelHandler returns an HTTP handler that lets operators bump
// the glog verbosity at runtime via POST /setloglevel/{level}. The bump is
// reverted to the baseline (v=2) after the given window. Inlined from the
// former trading/lib/factory package.
func CreateSetLoglevelHandler(ctx context.Context) http.Handler {
	return log.NewSetLoglevelHandler(ctx, log.NewLogLevelSetter(2, 5*time.Minute))
}
