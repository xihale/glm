package httputil

import (
	"glm/pkg/config"
	"net/http"
	"net/url"
	"time"
)

func NewHttpClient(timeout time.Duration) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()

	if config.Current.Proxy != "" {
		proxyURL, err := url.Parse(config.Current.Proxy)
		if err == nil {
			transport.Proxy = http.ProxyURL(proxyURL)
		}
	}

	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
	}
}
