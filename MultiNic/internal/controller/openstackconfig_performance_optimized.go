package controllers

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/gophercloud/gophercloud"
)

// OptimizedReconciler는 성능 최적화된 reconciler입니다
type OptimizedReconciler struct {
	*OpenstackConfigReconciler

	// Connection pools for reuse
	clientPools    sync.Map // map[string]*ClientPool
	httpClientPool *HTTPClientPool

	// Caching
	tokenCache   *TokenCache
	networkCache *NetworkCache

	// Rate limiting
	rateLimiter *TokenBucketLimiter
}

// ClientPool은 OpenStack 클라이언트 풀입니다
type ClientPool struct {
	clients chan *gophercloud.ServiceClient
	factory func() (*gophercloud.ServiceClient, error)
	maxSize int
	created int
	mutex   sync.Mutex
}

// HTTPClientPool은 HTTP 클라이언트 풀입니다
type HTTPClientPool struct {
	pool sync.Pool
}

// TokenCache는 인증 토큰 캐시입니다
type TokenCache struct {
	cache sync.Map // map[string]*CachedToken
}

type CachedToken struct {
	Token     string
	ExpiresAt time.Time
	Mutex     sync.RWMutex
}

// NetworkCache는 네트워크 정보 캐시입니다
type NetworkCache struct {
	networks    sync.Map // map[string]*CachedNetwork
	subnets     sync.Map // map[string]*CachedSubnet
	cacheExpiry time.Duration
}

type CachedNetwork struct {
	Data      interface{}
	ExpiresAt time.Time
}

type CachedSubnet struct {
	Data      interface{}
	ExpiresAt time.Time
}

// TokenBucketLimiter는 요청 제한을 위한 토큰 버킷입니다
type TokenBucketLimiter struct {
	tokens   chan struct{}
	interval time.Duration
	stop     chan struct{}
}

// NewOptimizedReconciler creates a new optimized reconciler
func NewOptimizedReconciler(base *OpenstackConfigReconciler) *OptimizedReconciler {
	return &OptimizedReconciler{
		OpenstackConfigReconciler: base,
		httpClientPool:            newHTTPClientPool(),
		tokenCache:                newTokenCache(),
		networkCache:              newNetworkCache(10 * time.Minute),       // 10분 캐시
		rateLimiter:               newTokenBucketLimiter(100, time.Second), // 초당 100 요청
	}
}

// newHTTPClientPool creates a new HTTP client pool
func newHTTPClientPool() *HTTPClientPool {
	return &HTTPClientPool{
		pool: sync.Pool{
			New: func() interface{} {
				return createOptimizedHTTPClientV2()
			},
		},
	}
}

// createOptimizedHTTPClientV2 creates an improved HTTP client
func createOptimizedHTTPClientV2() *http.Client {
	transport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,

		// 최적화된 커넥션 풀 설정
		MaxIdleConns:        200,               // 증가
		MaxIdleConnsPerHost: 50,                // 증가
		IdleConnTimeout:     180 * time.Second, // 증가

		// HTTP/2 지원
		ForceAttemptHTTP2: true,
		MaxConnsPerHost:   0, // 무제한

		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
			MinVersion:         tls.VersionTLS12,
		},
	}

	return &http.Client{
		Transport: transport,
		Timeout:   120 * time.Second,
	}
}

// GetHTTPClient returns a client from the pool
func (p *HTTPClientPool) GetHTTPClient() *http.Client {
	return p.pool.Get().(*http.Client)
}

// PutHTTPClient returns a client to the pool
func (p *HTTPClientPool) PutHTTPClient(client *http.Client) {
	p.pool.Put(client)
}

// newTokenCache creates a new token cache
func newTokenCache() *TokenCache {
	return &TokenCache{}
}

// GetToken gets a cached token
func (tc *TokenCache) GetToken(key string) (string, bool) {
	if val, ok := tc.cache.Load(key); ok {
		cached := val.(*CachedToken)
		cached.Mutex.RLock()
		defer cached.Mutex.RUnlock()

		if time.Now().Before(cached.ExpiresAt) {
			return cached.Token, true
		}

		// 만료된 토큰 삭제
		tc.cache.Delete(key)
	}
	return "", false
}

// SetToken sets a token in cache
func (tc *TokenCache) SetToken(key, token string, expiresIn time.Duration) {
	cached := &CachedToken{
		Token:     token,
		ExpiresAt: time.Now().Add(expiresIn - 5*time.Minute), // 5분 여유
	}
	tc.cache.Store(key, cached)
}

// newNetworkCache creates a new network cache
func newNetworkCache(expiry time.Duration) *NetworkCache {
	return &NetworkCache{
		cacheExpiry: expiry,
	}
}

// GetNetwork gets cached network data
func (nc *NetworkCache) GetNetwork(key string) (interface{}, bool) {
	if val, ok := nc.networks.Load(key); ok {
		cached := val.(*CachedNetwork)
		if time.Now().Before(cached.ExpiresAt) {
			return cached.Data, true
		}
		nc.networks.Delete(key)
	}
	return nil, false
}

// SetNetwork sets network data in cache
func (nc *NetworkCache) SetNetwork(key string, data interface{}) {
	cached := &CachedNetwork{
		Data:      data,
		ExpiresAt: time.Now().Add(nc.cacheExpiry),
	}
	nc.networks.Store(key, cached)
}

// newTokenBucketLimiter creates a new rate limiter
func newTokenBucketLimiter(capacity int, interval time.Duration) *TokenBucketLimiter {
	limiter := &TokenBucketLimiter{
		tokens:   make(chan struct{}, capacity),
		interval: interval / time.Duration(capacity),
		stop:     make(chan struct{}),
	}

	// 토큰 버킷 채우기
	for i := 0; i < capacity; i++ {
		limiter.tokens <- struct{}{}
	}

	// 주기적으로 토큰 추가
	go limiter.refillTokens()

	return limiter
}

// Acquire waits for a token
func (tbl *TokenBucketLimiter) Acquire(ctx context.Context) error {
	select {
	case <-tbl.tokens:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// refillTokens periodically adds tokens
func (tbl *TokenBucketLimiter) refillTokens() {
	ticker := time.NewTicker(tbl.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			select {
			case tbl.tokens <- struct{}{}:
			default:
				// 버킷이 가득 참
			}
		case <-tbl.stop:
			return
		}
	}
}

// Stop stops the rate limiter
func (tbl *TokenBucketLimiter) Stop() {
	close(tbl.stop)
}

// NewClientPool creates a new client pool
func NewClientPool(maxSize int, factory func() (*gophercloud.ServiceClient, error)) *ClientPool {
	return &ClientPool{
		clients: make(chan *gophercloud.ServiceClient, maxSize),
		factory: factory,
		maxSize: maxSize,
	}
}

// Get gets a client from the pool
func (cp *ClientPool) Get() (*gophercloud.ServiceClient, error) {
	select {
	case client := <-cp.clients:
		return client, nil
	default:
		cp.mutex.Lock()
		defer cp.mutex.Unlock()

		if cp.created < cp.maxSize {
			client, err := cp.factory()
			if err != nil {
				return nil, err
			}
			cp.created++
			return client, nil
		}

		// 블로킹으로 대기
		return <-cp.clients, nil
	}
}

// Put returns a client to the pool
func (cp *ClientPool) Put(client *gophercloud.ServiceClient) {
	select {
	case cp.clients <- client:
	default:
		// 풀이 가득 찬 경우 클라이언트 폐기
	}
}

// BatchRequest는 배치 요청을 위한 구조체입니다
type BatchRequest struct {
	Requests []RequestItem
	Results  chan BatchResult
}

type RequestItem struct {
	ID      string
	Request func() (interface{}, error)
}

type BatchResult struct {
	ID     string
	Result interface{}
	Error  error
}

// ProcessBatchRequests processes multiple requests concurrently
func (or *OptimizedReconciler) ProcessBatchRequests(ctx context.Context, batch *BatchRequest) {
	var wg sync.WaitGroup
	semaphore := make(chan struct{}, 10) // 최대 10개 동시 요청

	for _, req := range batch.Requests {
		wg.Add(1)
		go func(item RequestItem) {
			defer wg.Done()

			// Rate limiting
			if err := or.rateLimiter.Acquire(ctx); err != nil {
				batch.Results <- BatchResult{
					ID:    item.ID,
					Error: err,
				}
				return
			}

			// Semaphore
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			result, err := item.Request()
			batch.Results <- BatchResult{
				ID:     item.ID,
				Result: result,
				Error:  err,
			}
		}(req)
	}

	go func() {
		wg.Wait()
		close(batch.Results)
	}()
}
