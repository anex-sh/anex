package utils

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/hashicorp/go-retryablehttp"
)

func NewDefaultRetryClient() *retryablehttp.Client {
	rc := retryablehttp.NewClient()
	rc.HTTPClient.Timeout = 20 * time.Second
	rc.RetryWaitMin = 200 * time.Millisecond
	rc.RetryWaitMax = 2 * time.Second

	// Wire logging (optional)
	// rc.Logger = slog.NewLogLogger(logger.Handler(), slog.LevelInfo)
	return rc
}

type RetryOption func(*retryablehttp.Client)

func WithMaxRetries(n int) RetryOption {
	return func(c *retryablehttp.Client) { c.RetryMax = n }
}

var (
	ErrUnauthorized = errors.New("unauthorized")
	ErrBadPayload   = errors.New("bad payload")
)

func MakeRequest[T any](
	ctx context.Context,
	rc *retryablehttp.Client, // shared default client
	method string,
	url string,
	reqBody any,
	headers http.Header, // optional extra headers
	opts ...RetryOption, // per-call overrides
) (int, T, error) {
	var zero T
	client := *rc

	// Apply overrides (optional). Transport & pools remain shared.
	for _, opt := range opts {
		opt(&client)
	}

	var body io.ReadSeeker
	if reqBody != nil {
		b, err := json.Marshal(reqBody)
		if err != nil {
			return 0, zero, fmt.Errorf("marshal body: %w", err)
		}
		body = bytes.NewReader(b) // ReadSeeker: safe for retries
	}

	req, err := retryablehttp.NewRequest(method, url, body)
	if err != nil {
		return 0, zero, err
	}
	req = req.WithContext(ctx)

	// Headers
	req.Header.Set("Accept", "application/json")
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		for _, vv := range v {
			req.Header.Add(k, vv)
		}
	}

	// Execute with retries
	resp, err := client.Do(req)
	if err != nil {
		return 0, zero, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		return resp.StatusCode, zero, fmt.Errorf("http %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	if resp.StatusCode == http.StatusNoContent {
		return resp.StatusCode, zero, nil
	}

	// Read full body for debugging and then unmarshal
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, zero, fmt.Errorf("read body: %w", err)
	}

	// Pretty-print JSON response for temporary debugging
	//if len(raw) > 0 {
	//	var pretty bytes.Buffer
	//	if err := json.Indent(&pretty, raw, "", "  "); err == nil {
	//		fmt.Printf("HTTP response JSON (pretty):\n%s", pretty.String())
	//	} else {
	//		// Fallback to raw string if not valid JSON
	//		fmt.Printf("HTTP response (raw): %s", strings.TrimSpace(string(raw)))
	//	}
	//}
	//fmt.Println("")

	var out T
	if err := json.Unmarshal(raw, &out); err != nil {
		// On unmarshal error, also include raw body in the error for easier debugging
		return resp.StatusCode, zero, fmt.Errorf("unmarshal response: %w; raw=%s", err, strings.TrimSpace(string(raw)))
	}
	return resp.StatusCode, out, nil
}
