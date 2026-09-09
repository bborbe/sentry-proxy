// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"strings"
)

// extractProject returns the second path segment of a Sentry ingest path of
// the form /api/{project_id}/... and the literal "unknown" when the path does
// not match that shape.
func extractProject(path string) string {
	segments := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(segments) >= 2 && segments[0] == "api" && segments[1] != "" {
		return segments[1]
	}
	return "unknown"
}
