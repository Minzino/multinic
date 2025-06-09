package controllers

import (
	"context"
	"net/http"
	"sync"
	"time"
)

// ==========================================
// 실용적인 최적화 - 현재 MultiNic에 적합한 수준
// ==========================================

// SimpleHTTPClientPool - 단순하고 효과적인 HTTP 클라이언트 풀
type SimpleHTTPClientPool struct {
	client *http.Client
	once   sync.Once
}

// GetClient returns a reusable HTTP client
func (p *SimpleHTTPClientPool) GetClient() *http.Client {
	p.once.Do(func() {
		p.client = &http.Client{
			Timeout: 120 * time.Second,
			Transport: &http.Transport{
				// 현실적인 최적화 설정
				MaxIdleConns:        100,               // 기존 25 → 100
				MaxIdleConnsPerHost: 20,                // 기존 10 → 20
				IdleConnTimeout:     180 * time.Second, // 기존 90s → 180s

				// 기본 타임아웃 설정
				TLSHandshakeTimeout:   30 * time.Second,
				ResponseHeaderTimeout: 30 * time.Second,

				// Keep-alive 설정
				DisableKeepAlives: false,
			},
		}
	})
	return p.client
}

// SimpleMemoryCache - Redis 없이 간단한 인메모리 캐시
type SimpleMemoryCache struct {
	cache sync.Map
	ttl   time.Duration
}

type CacheItem struct {
	Value     interface{}
	ExpiresAt time.Time
}

// NewSimpleCache creates a new memory cache
func NewSimpleCache(ttl time.Duration) *SimpleMemoryCache {
	cache := &SimpleMemoryCache{
		ttl: ttl,
	}

	// 주기적으로 만료된 항목 정리
	go cache.cleanup()

	return cache
}

// Set stores a value in cache
func (c *SimpleMemoryCache) Set(key string, value interface{}) {
	item := &CacheItem{
		Value:     value,
		ExpiresAt: time.Now().Add(c.ttl),
	}
	c.cache.Store(key, item)
}

// Get retrieves a value from cache
func (c *SimpleMemoryCache) Get(key string) (interface{}, bool) {
	if item, ok := c.cache.Load(key); ok {
		cacheItem := item.(*CacheItem)
		if time.Now().Before(cacheItem.ExpiresAt) {
			return cacheItem.Value, true
		}
		// 만료된 항목 삭제
		c.cache.Delete(key)
	}
	return nil, false
}

// cleanup removes expired items
func (c *SimpleMemoryCache) cleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		c.cache.Range(func(key, value interface{}) bool {
			if item, ok := value.(*CacheItem); ok {
				if time.Now().After(item.ExpiresAt) {
					c.cache.Delete(key)
				}
			}
			return true
		})
	}
}

// SimpleRetry - Circuit Breaker 대신 간단한 재시도
type SimpleRetry struct {
	MaxRetries int
	BaseDelay  time.Duration
}

// NewSimpleRetry creates a retry helper
func NewSimpleRetry(maxRetries int, baseDelay time.Duration) *SimpleRetry {
	return &SimpleRetry{
		MaxRetries: maxRetries,
		BaseDelay:  baseDelay,
	}
}

// Execute runs a function with retry logic
func (r *SimpleRetry) Execute(ctx context.Context, fn func() error) error {
	var lastErr error

	for attempt := 0; attempt <= r.MaxRetries; attempt++ {
		if attempt > 0 {
			// Exponential backoff
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

// SimpleMetrics - 복잡한 메트릭 대신 기본적인 카운터
type SimpleMetrics struct {
	RequestCount int64
	ErrorCount   int64
	TotalLatency time.Duration
	mutex        sync.RWMutex
}

// NewSimpleMetrics creates basic metrics
func NewSimpleMetrics() *SimpleMetrics {
	return &SimpleMetrics{}
}

// RecordRequest records a request with latency
func (m *SimpleMetrics) RecordRequest(latency time.Duration) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	m.RequestCount++
	m.TotalLatency += latency
}

// RecordError records an error
func (m *SimpleMetrics) RecordError() {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	m.ErrorCount++
}

// GetStats returns current statistics
func (m *SimpleMetrics) GetStats() (requests, errors int64, avgLatency time.Duration) {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	requests = m.RequestCount
	errors = m.ErrorCount

	if requests > 0 {
		avgLatency = m.TotalLatency / time.Duration(requests)
	}

	return
}

// OptimizedController - 실용적으로 최적화된 컨트롤러
type OptimizedController struct {
	httpPool     *SimpleHTTPClientPool
	tokenCache   *SimpleMemoryCache
	networkCache *SimpleMemoryCache
	retry        *SimpleRetry
	metrics      *SimpleMetrics
}

// NewOptimizedController creates an optimized controller
func NewOptimizedController() *OptimizedController {
	return &OptimizedController{
		httpPool:     &SimpleHTTPClientPool{},
		tokenCache:   NewSimpleCache(45 * time.Minute), // OpenStack 토큰 캐시
		networkCache: NewSimpleCache(10 * time.Minute), // 네트워크 정보 캐시
		retry:        NewSimpleRetry(3, 1*time.Second), // 3회 재시도
		metrics:      NewSimpleMetrics(),
	}
}

// GetHTTPClient returns optimized HTTP client
func (oc *OptimizedController) GetHTTPClient() *http.Client {
	return oc.httpPool.GetClient()
}

// CacheToken stores authentication token
func (oc *OptimizedController) CacheToken(key, token string) {
	oc.tokenCache.Set(key, token)
}

// GetCachedToken retrieves cached token
func (oc *OptimizedController) GetCachedToken(key string) (string, bool) {
	if value, ok := oc.tokenCache.Get(key); ok {
		return value.(string), true
	}
	return "", false
}

// CacheNetworkInfo stores network information
func (oc *OptimizedController) CacheNetworkInfo(key string, data interface{}) {
	oc.networkCache.Set(key, data)
}

// GetCachedNetworkInfo retrieves cached network info
func (oc *OptimizedController) GetCachedNetworkInfo(key string) (interface{}, bool) {
	return oc.networkCache.Get(key)
}

// ExecuteWithRetry runs operation with retry
func (oc *OptimizedController) ExecuteWithRetry(ctx context.Context, operation func() error) error {
	start := time.Now()

	err := oc.retry.Execute(ctx, operation)

	latency := time.Since(start)
	if err != nil {
		oc.metrics.RecordError()
	} else {
		oc.metrics.RecordRequest(latency)
	}

	return err
}

// GetMetrics returns performance metrics
func (oc *OptimizedController) GetMetrics() (int64, int64, time.Duration) {
	return oc.metrics.GetStats()
}

// ==========================================
// 실제 사용 예시
// ==========================================

// Example: OpenStack API 호출 최적화
func (oc *OptimizedController) OptimizedOpenStackCall(ctx context.Context, url string) (*http.Response, error) {
	// 1. 캐시된 토큰 확인
	if token, exists := oc.GetCachedToken("openstack_token"); exists {
		// 토큰이 있으면 바로 사용
		_ = token
	}

	// 2. 최적화된 HTTP 클라이언트 사용
	client := oc.GetHTTPClient()

	// 3. 재시도와 메트릭 수집을 포함한 실행
	var response *http.Response
	err := oc.ExecuteWithRetry(ctx, func() error {
		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			return err
		}

		response, err = client.Do(req)
		return err
	})

	return response, err
}

// Example: 네트워크 정보 캐싱
func (oc *OptimizedController) GetNetworkWithCache(ctx context.Context, networkName string) (interface{}, error) {
	cacheKey := "network_" + networkName

	// 1. 캐시 확인
	if cached, exists := oc.GetCachedNetworkInfo(cacheKey); exists {
		return cached, nil
	}

	// 2. 캐시 미스 - API 호출
	var networkInfo interface{}
	err := oc.ExecuteWithRetry(ctx, func() error {
		// OpenStack API 호출 로직
		// networkInfo = ...
		return nil
	})

	if err != nil {
		return nil, err
	}

	// 3. 결과 캐싱
	oc.CacheNetworkInfo(cacheKey, networkInfo)

	return networkInfo, nil
}
