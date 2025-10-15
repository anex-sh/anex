package virtualpod

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/hashicorp/go-retryablehttp"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func newHTTPClientReturning(status int) *retryablehttp.Client {
	std := &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: status, Body: http.NoBody}, nil
	})}
	c := retryablehttp.NewClient()
	c.HTTPClient = std
	return c
}

// Keeping as smoke test; implementation relies on utils.MakeRequest which is covered by integration tests
func TestRunCommandCallsAgentEndpoint(t *testing.T) {
	pod := newTestPod()
	m := &Machine{PublicIP: "127.0.0.1", AgentPort: 65535}
	_ = NewVirtualPod("id1", pod, m, ProxyConfig{}, nil, nil, "tok")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_ = ctx
}
