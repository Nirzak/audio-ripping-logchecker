package main

import (
	"sync"
	"time"
)

// resultEntry holds a one-shot HTML result with a creation timestamp for TTL eviction.
type resultEntry struct {
	html      string
	createdAt time.Time
}

// resultStore is a concurrency-safe, TTL-bounded in-memory store for
// rendered HTML results. Each entry is consumed exactly once (pop).
type resultStore struct {
	mu      sync.Mutex
	entries map[string]*resultEntry
}

// newResultStore creates a store and starts a background TTL cleaner.
func newResultStore() *resultStore {
	s := &resultStore{entries: make(map[string]*resultEntry)}
	go s.clean()
	return s
}

// set stores an HTML result under the given ID.
func (s *resultStore) set(id, html string) {
	s.mu.Lock()
	s.entries[id] = &resultEntry{html: html, createdAt: time.Now()}
	s.mu.Unlock()
}

// pop retrieves and removes the result for id. Returns ("", false) if not found.
func (s *resultStore) pop(id string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[id]
	if !ok {
		return "", false
	}
	delete(s.entries, id)
	return e.html, true
}

// clean periodically removes entries older than resultsTTL.
func (s *resultStore) clean() {
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()
	for range ticker.C {
		cutoff := time.Now().Add(-resultsTTL)
		s.mu.Lock()
		for id, e := range s.entries {
			if e.createdAt.Before(cutoff) {
				delete(s.entries, id)
			}
		}
		s.mu.Unlock()
	}
}
