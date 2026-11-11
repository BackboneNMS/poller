package dashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

type Client struct {
	url      string
	token    string
	version  string
	hostname string
	http     *http.Client
}

func New(url, token, version string, timeout time.Duration) *Client {
	hostname, _ := os.Hostname()

	return &Client{
		url:      url,
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

func (c *Client) request(ctx context.Context, method, path string, body, result any) error {
	var reader io.Reader

	if body != nil {
		data, err := json.Marshal(body)

		if err != nil {
			return err
		}

		reader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.url+path, reader)

	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("User-Agent", "backbone-poller/"+c.version)
	req.Header.Set("X-Poller-Version", c.version)
	req.Header.Set("X-Poller-Hostname", c.hostname)

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	res, err := c.http.Do(req)

	if err != nil {
		return err
	}

	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		message, _ := io.ReadAll(io.LimitReader(res.Body, 1024))

		return fmt.Errorf("%s %s: dashboard responded with %d: %s", method, path, res.StatusCode, bytes.TrimSpace(message))
	}

	if result == nil {
		return nil
	}

	return json.NewDecoder(res.Body).Decode(result)
}
