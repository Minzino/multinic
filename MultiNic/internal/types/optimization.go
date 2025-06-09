/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package types

import (
	"context"
	"net/http"
	"sync"
	"time"
)

// ==========================================
// Cache Related Types
// ==========================================

// CacheConfig represents cache configuration
type CacheConfig struct {
	TTL             time.Duration `json:"ttl"`
	CleanupInterval time.Duration `json:"cleanupInterval"`
}

// CacheEntry represents a cache entry
type CacheEntry struct {
	Value        interface{} `json:"value"`
	ExpiresAt    time.Time   `json:"expiresAt"`
	AccessCount  int64       `json:"accessCount"`
	LastAccessed time.Time   `json:"lastAccessed"`
}

// SimpleMemoryCache represents a simple in-memory cache
type SimpleMemoryCache struct {
	cache  sync.Map      `json:"-"`      // 실제 캐시 저장소
	Config CacheConfig   `json:"config"` // 캐시 설정
	TTL    time.Duration `json:"ttl"`    // 호환성을 위한 TTL
}

// NewSimpleMemoryCache creates a new memory cache
func NewSimpleMemoryCache(ttl time.Duration) *SimpleMemoryCache {
	cache := &SimpleMemoryCache{
		Config: CacheConfig{
			TTL:             ttl,
			CleanupInterval: 5 * time.Minute,
		},
		TTL: ttl,
	}

	// 정리 루틴 시작
	go cache.cleanup()

	return cache
}

// Set stores a value in cache
func (c *SimpleMemoryCache) Set(key string, value interface{}) {
	entry := &CacheEntry{
		Value:        value,
		ExpiresAt:    time.Now().Add(c.TTL),
		AccessCount:  0,
		LastAccessed: time.Now(),
	}
	c.cache.Store(key, entry)
}

// Get retrieves a value from cache
func (c *SimpleMemoryCache) Get(key string) (interface{}, bool) {
	if item, ok := c.cache.Load(key); ok {
		entry := item.(*CacheEntry)
		if time.Now().Before(entry.ExpiresAt) {
			// 접근 통계 업데이트
			entry.AccessCount++
			entry.LastAccessed = time.Now()
			return entry.Value, true
		}
		// 만료된 항목 삭제
		c.cache.Delete(key)
	}
	return nil, false
}

// cleanup removes expired items
func (c *SimpleMemoryCache) cleanup() {
	ticker := time.NewTicker(c.Config.CleanupInterval)
	defer ticker.Stop()

	for range ticker.C {
		c.cache.Range(func(key, value interface{}) bool {
			if entry, ok := value.(*CacheEntry); ok {
				if time.Now().After(entry.ExpiresAt) {
					c.cache.Delete(key)
				}
			}
			return true
		})
	}
}

// ==========================================
// HTTP Client Pool
// ==========================================

// SimpleHTTPClientPool represents HTTP client pool
type SimpleHTTPClientPool struct {
	client *http.Client `json:"-"`
	once   sync.Once    `json:"-"`
}

// GetClient returns a reusable HTTP client
func (p *SimpleHTTPClientPool) GetClient() *http.Client {
	p.once.Do(func() {
		p.client = &http.Client{
			Timeout: 120 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:          100,
				MaxIdleConnsPerHost:   20,
				IdleConnTimeout:       180 * time.Second,
				TLSHandshakeTimeout:   30 * time.Second,
				ResponseHeaderTimeout: 30 * time.Second,
				DisableKeepAlives:     false,
			},
		}
	})
	return p.client
}

// ==========================================
// Retry Logic
// ==========================================

// SimpleRetry represents simple retry mechanism
type SimpleRetry struct {
	MaxRetries int           `json:"maxRetries"`
	BaseDelay  time.Duration `json:"baseDelay"`
}

// Execute runs a function with retry logic
func (r *SimpleRetry) Execute(ctx context.Context, fn func() error) error {
	var lastErr error

	for attempt := 0; attempt <= r.MaxRetries; attempt++ {
		if attempt > 0 {
			delay := time.Duration(attempt) * r.BaseDelay
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return ctx.Err()
			}
		}

		if err := fn(); err != nil {
			lastErr = err
			continue
		}

		return nil // 성공
	}

	return lastErr
}

// ==========================================
// Basic Metrics
// ==========================================

// SimpleMetrics represents basic metrics collection
type SimpleMetrics struct {
	RequestCount int64         `json:"requestCount"`
	ErrorCount   int64         `json:"errorCount"`
	TotalLatency time.Duration `json:"totalLatency"`
	Mutex        sync.RWMutex  `json:"-"`
}

// RecordRequest records a request with latency
func (m *SimpleMetrics) RecordRequest(latency time.Duration) {
	m.Mutex.Lock()
	defer m.Mutex.Unlock()

	m.RequestCount++
	m.TotalLatency += latency
}

// RecordError records an error
func (m *SimpleMetrics) RecordError() {
	m.Mutex.Lock()
	defer m.Mutex.Unlock()

	m.ErrorCount++
}

// GetStats returns current statistics
func (m *SimpleMetrics) GetStats() (requests, errors int64, avgLatency time.Duration) {
	m.Mutex.RLock()
	defer m.Mutex.RUnlock()

	requests = m.RequestCount
	errors = m.ErrorCount

	if requests > 0 {
		avgLatency = m.TotalLatency / time.Duration(requests)
	}

	return
}
