package api

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func testServer(t *testing.T, status int, hits *atomic.Int32) *httptest.Server {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(status)
		w.Write([]byte(`{"pollInterval":60,"devices":[]}`))
	}))

	t.Cleanup(server.Close)

	return server
}

func TestClientFailsOver(t *testing.T) {
	var downHits, upHits atomic.Int32

	down := testServer(t, http.StatusServiceUnavailable, &downHits)
	up := testServer(t, http.StatusOK, &upHits)
	client := New([]string{down.URL, up.URL}, "token", "test", time.Second)

	for range 2 {
		if _, err := client.FetchDevices(context.Background()); err != nil {
			t.Fatalf("expected the request to fail over to the healthy server, got %v", err)
		}
	}

	if downHits.Load() != 1 || upHits.Load() != 2 {
		t.Fatalf("expected the failing server to be tried once and then skipped, got %d and %d requests", downHits.Load(), upHits.Load())
	}
}

func TestClientDoesNotRetryRejectedRequests(t *testing.T) {
	var rejectedHits, upHits atomic.Int32

	rejected := testServer(t, http.StatusUnauthorized, &rejectedHits)
	up := testServer(t, http.StatusOK, &upHits)
	client := New([]string{rejected.URL, up.URL}, "token", "test", time.Second)

	if _, err := client.FetchDevices(context.Background()); err == nil {
		t.Fatal("expected a rejected request to fail")
	}

	if upHits.Load() != 0 {
		t.Fatal("expected a request rejected with a client error not to be retried on another server")
	}
}

func TestClientReportsEveryFailure(t *testing.T) {
	var hits atomic.Int32

	client := New([]string{testServer(t, http.StatusBadGateway, &hits).URL, testServer(t, http.StatusServiceUnavailable, &hits).URL}, "token", "test", time.Second)

	if _, err := client.FetchDevices(context.Background()); err == nil || hits.Load() != 2 {
		t.Fatalf("expected both servers to be tried and the request to fail, got %d requests and %v", hits.Load(), err)
	}
}

func TestClientCompressesOnlyForServersThatAcceptGzip(t *testing.T) {
	for _, advertises := range []bool{true, false} {
		var encoding atomic.Value

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if advertises {
				w.Header().Set("Accept-Encoding", "gzip")
			}

			if r.Method == http.MethodPost {
				encoding.Store(r.Header.Get("Content-Encoding"))

				body := r.Body

				if r.Header.Get("Content-Encoding") == "gzip" {
					reader, err := gzip.NewReader(r.Body)

					if err != nil {
						t.Errorf("expected a valid gzip body, got %v", err)

						return
					}

					body = reader
				}

				var decoded submitResultsBody

				if err := json.NewDecoder(body).Decode(&decoded); err != nil || len(decoded.Results) != 1 {
					t.Errorf("expected the body to decode to one result, got %v", err)
				}
			}

			w.Write([]byte(`{"pollInterval":60,"devices":[],"accepted":1}`))
		}))

		t.Cleanup(server.Close)

		client := New([]string{server.URL}, "token", "test", time.Second)

		if _, err := client.FetchDevices(context.Background()); err != nil {
			t.Fatal(err)
		}

		if err := client.SubmitResults(context.Background(), []PollResult{{DeviceID: "device"}}); err != nil {
			t.Fatal(err)
		}

		if expected := map[bool]string{true: "gzip", false: ""}[advertises]; encoding.Load() != expected {
			t.Fatalf("expected Content-Encoding %q when the server advertises gzip is %v, got %q", expected, advertises, encoding.Load())
		}
	}
}
