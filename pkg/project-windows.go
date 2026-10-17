// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"time"
)

const (
	// defaultProjectSweepThreshold is how many admitted requests may pass
	// between two sweeps of the per-project map. Sweeping on a growth threshold
	// rather than on every request keeps the common path O(1) in the number of
	// tracked projects.
	defaultProjectSweepThreshold = 256
	// defaultMaxProjects bounds the number of tracked projects. The project id
	// is taken from the request path and is therefore caller-controlled, so an
	// unbounded map would be a memory-exhaustion vector.
	defaultMaxProjects = 256
)

// projectWindows holds one timestamp-pruned sliding window per project id.
//
// It is deliberately unsynchronised and owns no mutex: the caller must already
// hold the lock guarding it. NewRateLimitRoundTripper uses the same mux that
// guards the global window, so the global and per-project admission decisions
// are evaluated against one consistent snapshot under a single lock hold.
type projectWindows struct {
	limitPerProject int
	requestDuration time.Duration
	sweepThreshold  int
	maxProjects     int
	admitted        int
	byProject       map[string][]time.Time
}

// newProjectWindows creates an empty per-project window store. A
// sweepThreshold or maxProjects below 1 falls back to the package default, so
// the store is always bounded even when a caller passes a zero value.
func newProjectWindows(
	limitPerProject int,
	requestDuration time.Duration,
	sweepThreshold int,
	maxProjects int,
) *projectWindows {
	if sweepThreshold < 1 {
		sweepThreshold = defaultProjectSweepThreshold
	}
	if maxProjects < 1 {
		maxProjects = defaultMaxProjects
	}
	return &projectWindows{
		limitPerProject: limitPerProject,
		requestDuration: requestDuration,
		sweepThreshold:  sweepThreshold,
		maxProjects:     maxProjects,
		byProject:       map[string][]time.Time{},
	}
}

// admit prunes the requesting project's window, rejects the request when that
// window already holds limitPerProject live entries, and otherwise records now
// in it. It reports whether the request may be forwarded. The "unknown" key is
// an ordinary project here: exempting it would let a malformed path bypass the
// per-project cap entirely.
//
// The caller must already hold the mutex guarding this store.
func (p *projectWindows) admit(project string, now time.Time) bool {
	p.prune(project, now)
	if len(p.byProject[project]) >= p.limitPerProject {
		return false
	}
	p.admitted++
	if p.admitted%p.sweepThreshold == 0 {
		p.sweep(now)
	}
	p.byProject[project] = append(p.byProject[project], now)
	p.evictOldest()
	return true
}

// prune drops the leading timestamps of the given project's window that are at
// least requestDuration old — the same rule the global window uses, so an entry
// exactly one window old drops out — and removes the project once its window
// empties.
//
// The caller must already hold the mutex guarding this store.
func (p *projectWindows) prune(project string, now time.Time) {
	timestamps := p.byProject[project]
	pruned := 0
	for pruned < len(timestamps) && now.Sub(timestamps[pruned]) >= p.requestDuration {
		pruned++
	}
	if pruned == 0 {
		return
	}
	timestamps = timestamps[pruned:]
	if len(timestamps) == 0 {
		delete(p.byProject, project)
		return
	}
	p.byProject[project] = timestamps
}

// sweep removes every project whose newest timestamp is at least
// requestDuration old, i.e. which holds no live entry at all. It is the second
// of the three bounds on the store, after the per-request prune.
//
// The caller must already hold the mutex guarding this store.
func (p *projectWindows) sweep(now time.Time) {
	for project, timestamps := range p.byProject {
		if now.Sub(timestamps[len(timestamps)-1]) >= p.requestDuration {
			delete(p.byProject, project)
		}
	}
}

// evictOldest removes the least-recently-seen project until the store is back
// within maxProjects. This is the hard bound and is not optional: the project
// key comes from an unauthenticated request path, so without it a caller could
// mint unlimited distinct ids and grow the map without limit.
//
// The caller must already hold the mutex guarding this store.
func (p *projectWindows) evictOldest() {
	for len(p.byProject) > p.maxProjects {
		oldestProject := ""
		var oldestSeen time.Time
		for project, timestamps := range p.byProject {
			newest := timestamps[len(timestamps)-1]
			if oldestProject == "" || newest.Before(oldestSeen) {
				oldestProject = project
				oldestSeen = newest
			}
		}
		if oldestProject == "" {
			return
		}
		delete(p.byProject, oldestProject)
	}
}
