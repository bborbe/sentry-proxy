// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// This file is an internal test of package pkg — a deliberate deviation from the
// external package pkg_test convention used by every other test file here. The
// type under test (projectWindows) is unexported, so an external test cannot
// reach it at all; do not "fix" this by moving the file to package pkg_test.
//
// It also uses Gomega's NewWithT rather than a Ginkgo RunSpecs bootstrap: a Go
// test binary for this directory links the internal (package pkg) and external
// (package pkg_test) test files into one binary, and Ginkgo supports only one
// RunSpecs per binary — a second one aborts with "It looks like you are calling
// RunSpecs more than once". Plain subtests keep the internal cases runnable
// alongside the external Ginkgo suite.
package pkg

import (
	"testing"
	"time"

	. "github.com/onsi/gomega"
)

func TestProjectWindows(t *testing.T) {
	baseTime := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)

	t.Run("prunes a project's entries once they are one window old", func(t *testing.T) {
		g := NewWithT(t)
		// A sweep threshold beyond the number of admissions keeps the sweep out
		// of the way, so this pins the per-request prune alone.
		windows := newProjectWindows(1, 1*time.Hour, 100, 100)

		g.Expect(windows.admit("project-a", baseTime)).To(BeTrue())
		g.Expect(windows.admit("project-a", baseTime.Add(30*time.Minute))).To(BeFalse())

		g.Expect(windows.admit("project-a", baseTime.Add(1*time.Hour))).To(BeTrue())
		g.Expect(windows.byProject["project-a"]).To(HaveLen(1))
		g.Expect(windows.byProject["project-a"][0]).To(Equal(baseTime.Add(1 * time.Hour)))
	})

	t.Run("sweeps away a project whose newest entry has aged out", func(t *testing.T) {
		g := NewWithT(t)
		// A sweep threshold of 1 runs the sweep on every admission, so the second
		// admission is what evicts the aged-out project.
		windows := newProjectWindows(1, 1*time.Hour, 1, 100)

		g.Expect(windows.admit("project-a", baseTime)).To(BeTrue())
		g.Expect(windows.admit("project-b", baseTime.Add(2*time.Hour))).To(BeTrue())

		g.Expect(windows.byProject).To(HaveLen(1))
		g.Expect(windows.byProject).To(HaveKey("project-b"))
	})

	t.Run(
		"enforces the hard maximum by evicting the least-recently-seen project",
		func(t *testing.T) {
			g := NewWithT(t)
			// No sweep (threshold above the admissions) so the hard bound is the only
			// thing that can shrink the map.
			windows := newProjectWindows(1, 1*time.Hour, 100, 2)

			g.Expect(windows.admit("project-a", baseTime)).To(BeTrue())
			g.Expect(windows.admit("project-b", baseTime.Add(10*time.Minute))).To(BeTrue())
			g.Expect(windows.admit("project-c", baseTime.Add(20*time.Minute))).To(BeTrue())

			g.Expect(windows.byProject).To(HaveLen(2))
			g.Expect(windows.byProject).NotTo(HaveKey("project-a"))
			g.Expect(windows.byProject).To(HaveKey("project-b"))
			g.Expect(windows.byProject).To(HaveKey("project-c"))
		},
	)
}
