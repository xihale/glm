package httputil

import (
	"net/http"
	"net/url"
	"time"
)

// NewHttpClient builds a client honoring the shared top-level proxy.
func NewHttpClient(timeout time.Duration) *http.Client {
	return NewHttpClientWithProxy(timeout, "")
}

// NewHttpClientWithProxy builds a client with an explicit proxy URL
// (http:// or socks5://; empty = direct). Callers holding a config snapshot
// pass their per-provider proxy here.
func NewHttpClientWithProxy(timeout time.Duration, proxyURL string) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()

	if proxyURL != "" {
		if u, err := url.Parse(proxyURL); err == nil {
			transport.Proxy = http.ProxyURL(u)
		}
	}

	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
	}
}
