package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync/atomic"
	"time"
)

type Client struct {
	urls     []string
	token    string
	version  string
	hostname string
	http     *http.Client
	current  atomic.Int32
}

// statusError is a response from a polling server that was not successful
type statusError struct {
	url     string
	code    int
	message []byte
}

func (e *statusError) Error() string {
	return fmt.Sprintf("%s responded with %d: %s", e.url, e.code, e.message)
}

func New(urls []string, token, version string, timeout time.Duration) *Client {
	hostname, _ := os.Hostname()

	return &Client{
		urls:     urls,
		token:    token,
		version:  version,
		hostname: hostname,
		http:     &http.Client{Timeout: timeout},
	}
}

func (c *Client) FetchDevices(ctx context.Context) (*DeviceList, error) {
	var result DeviceList

	if err := c.request(ctx, http.MethodGet, "/api/poller/devices", nil, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

func (c *Client) SubmitResults(ctx context.Context, results []PollResult) error {
	return c.request(ctx, http.MethodPost, "/api/poller/results", submitResultsBody{Results: results}, nil)
}

// request sends a request to the polling server that answered last, failing over to the others in order when it cannot
// be reached or fails with a server error. Requests a server rejects (an invalid token, for example) are not retried,
// since every server would reject them the same way. Resubmitting results is safe: the servers ignore duplicates.
func (c *Client) request(ctx context.Context, method, path string, body, result any) error {
	var data []byte

	if body != nil {
		var err error

		if data, err = json.Marshal(body); err != nil {
			return err
		}
	}

	start := int(c.current.Load())
	errs := make([]error, 0, len(c.urls))

	for attempt := range len(c.urls) {
		index := (start + attempt) % len(c.urls)
		err := c.send(ctx, c.urls[index], method, path, data, result)

		if err == nil {
			c.current.Store(int32(index))

			return nil
		}

		errs = append(errs, err)

		var status *statusError

		if ctx.Err() != nil || errors.As(err, &status) && status.code < http.StatusInternalServerError {
			break
		}
	}

	return fmt.Errorf("%s %s: %w", method, path, errors.Join(errs...))
}

func (c *Client) send(ctx context.Context, url, method, path string, data []byte, result any) error {
	var reader io.Reader

	if data != nil {
		reader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, url+path, reader)

	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("User-Agent", "backbone-poller/"+c.version)
	req.Header.Set("X-Poller-Version", c.version)
	req.Header.Set("X-Poller-Hostname", c.hostname)

	if data != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	res, err := c.http.Do(req)

	if err != nil {
		return err
	}

	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		message, _ := io.ReadAll(io.LimitReader(res.Body, 1024))

		return &statusError{url: url, code: res.StatusCode, message: bytes.TrimSpace(message)}
	}

	if result == nil {
		return nil
	}

	return json.NewDecoder(res.Body).Decode(result)
}
