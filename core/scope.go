package core

import (
	"fmt"
	"sync"
	"time"
)

type scopeSnapshot struct {
	Tags        []string
	User        map[string]any
	Release     string
	Environment string
	Dist        string
	ServerName  string
	Service     string
	RequestID   string
	Transaction string
	TraceID     string
	SpanID      string
	Fingerprint string
	Breadcrumbs []map[string]any
	SDK         map[string]any
	Runtime     map[string]any
	Extra       map[string]any
	Platform    string
}

type scope struct {
	mu          sync.RWMutex
	tags        map[string]string
	user        map[string]any
	release     string
	environment string
	dist        string
	serverName  string
	service     string
	requestID   string
	transaction string
	traceID     string
	spanID      string
	fingerprint string
	breadcrumbs []map[string]any
	sdk         map[string]any
	runtime     map[string]any
	extra       map[string]any
	platform    string
}

func newScope(snapshot scopeSnapshot) *scope {
	s := &scope{
		tags:     make(map[string]string),
		sdk:      cloneMapStringAny(snapshot.SDK),
		runtime:  cloneMapStringAny(snapshot.Runtime),
		extra:    cloneMapStringAny(snapshot.Extra),
		platform: snapshot.Platform,
	}
	if s.platform == "" {
		s.platform = "go"
	}
	s.user = cloneMapStringAny(snapshot.User)
	s.release = snapshot.Release
	s.environment = snapshot.Environment
	s.dist = snapshot.Dist
	s.serverName = snapshot.ServerName
	s.service = snapshot.Service
	s.requestID = snapshot.RequestID
	s.transaction = snapshot.Transaction
	s.traceID = snapshot.TraceID
	s.spanID = snapshot.SpanID
	s.fingerprint = snapshot.Fingerprint
	if len(snapshot.Tags) > 0 {
		for _, tag := range snapshot.Tags {
			key := tag
			if idx := indexByte(tag, ':'); idx > -1 {
				key = tag[:idx]
			}
			s.tags[key] = tag
		}
	}
	if len(snapshot.Breadcrumbs) > 0 {
		s.breadcrumbs = make([]map[string]any, 0, len(snapshot.Breadcrumbs))
		for _, item := range snapshot.Breadcrumbs {
			s.breadcrumbs = append(s.breadcrumbs, cloneMapStringAny(item))
		}
	}
	return s
}

func (s *scope) setTag(key string, value any) {
	s.mu.Lock()
	defer s.mu.Unlock()

	normalizedKey := normalizeString(key)
	if normalizedKey == "" {
		return
	}

	if value == nil {
		s.tags[normalizedKey] = normalizedKey
		return
	}

	s.tags[normalizedKey] = normalizedKey + ":" + normalizeString(fmt.Sprint(value))
}

func (s *scope) setTags(values map[string]any) {
	for key, value := range values {
		s.setTag(key, value)
	}
}

func (s *scope) clearTags() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tags = make(map[string]string)
}

func (s *scope) setUser(user map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.user = normalizeStringMap(user)
}

func (s *scope) clearUser() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.user = nil
}

func (s *scope) setRelease(value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.release = normalizeString(value)
}

func (s *scope) setEnvironment(value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.environment = normalizeString(value)
}

func (s *scope) setDist(value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dist = normalizeString(value)
}

func (s *scope) setServerName(value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.serverName = normalizeString(value)
}

func (s *scope) setService(value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.service = normalizeString(value)
}

func (s *scope) setRequestID(value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requestID = normalizeString(value)
}

func (s *scope) setTransaction(value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.transaction = normalizeString(value)
}

func (s *scope) setTrace(traceID, spanID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.traceID = normalizeString(traceID)
	s.spanID = normalizeString(spanID)
}

func (s *scope) setFingerprint(value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fingerprint = normalizeString(value)
}

func (s *scope) setSDK(value map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sdk = normalizeStringMap(value)
}

func (s *scope) setRuntime(value map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runtime = normalizeStringMap(value)
}

func (s *scope) setExtra(value map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.extra = normalizeStringMap(value)
}

func (s *scope) setPlatform(value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if trimmed := normalizeString(value); trimmed != "" {
		s.platform = trimmed
	}
}

func (s *scope) addBreadcrumb(breadcrumb map[string]any) {
	if breadcrumb == nil {
		return
	}

	normalized := normalizeStringMap(breadcrumb)
	if normalized == nil {
		return
	}
	if _, ok := normalized["timestamp"]; !ok {
		normalized["timestamp"] = time.Now().UnixMilli()
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.breadcrumbs = append(s.breadcrumbs, normalized)
}

func (s *scope) clearBreadcrumbs() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.breadcrumbs = nil
}

func (s *scope) snapshot() scopeSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	snapshot := scopeSnapshot{
		Tags:        make([]string, 0, len(s.tags)),
		User:        cloneMapStringAny(s.user),
		Release:     s.release,
		Environment: s.environment,
		Dist:        s.dist,
		ServerName:  s.serverName,
		Service:     s.service,
		RequestID:   s.requestID,
		Transaction: s.transaction,
		TraceID:     s.traceID,
		SpanID:      s.spanID,
		Fingerprint: s.fingerprint,
		SDK:         cloneMapStringAny(s.sdk),
		Runtime:     cloneMapStringAny(s.runtime),
		Extra:       cloneMapStringAny(s.extra),
		Platform:    s.platform,
	}

	for _, value := range s.tags {
		snapshot.Tags = append(snapshot.Tags, value)
	}
	if len(s.breadcrumbs) > 0 {
		snapshot.Breadcrumbs = make([]map[string]any, 0, len(s.breadcrumbs))
		for _, item := range s.breadcrumbs {
			snapshot.Breadcrumbs = append(snapshot.Breadcrumbs, cloneMapStringAny(item))
		}
	}

	return snapshot
}

func indexByte(value string, ch byte) int {
	for i := 0; i < len(value); i++ {
		if value[i] == ch {
			return i
		}
	}
	return -1
}
