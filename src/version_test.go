package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"main/src/api"
	"main/src/semver"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestVersionIsSemantic(t *testing.T) {
	if _, err := semver.Parse(Version); err != nil {
		t.Fatalf("Version must be MAJOR.MINOR.PATCH to be compared with the latest release: %v", err)
	}
}

func TestCheckVersion(t *testing.T) {
	previousClient, previousLogger := client, logger

	t.Cleanup(func() { client, logger = previousClient, previousLogger })

	current, _ := semver.Parse(Version)
	cases := map[string]string{
		semver.Version{Major: current.Major + 1}.String():                                             "major",
		semver.Version{Major: current.Major, Minor: current.Minor + 1}.String():                       "minor",
		semver.Version{Major: current.Major, Minor: current.Minor, Patch: current.Patch + 1}.String(): "patch",
		Version: "",
	}

	for latest, change := range cases {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]string{"version": latest})
		}))

		var output bytes.Buffer

		client = api.New([]string{server.URL}, "token", Version, time.Second)
		logger = slog.New(slog.NewJSONHandler(&output, nil))

		checkVersion(context.Background())
		server.Close()

		var line map[string]string

		if len(change) < 1 {
			if output.Len() > 0 {
				t.Errorf("expected nothing logged when %s is the latest version, got %s", latest, output.String())
			}

			continue
		}

		if err := json.Unmarshal(output.Bytes(), &line); err != nil {
			t.Fatalf("expected one log line for latest version %s, got %q", latest, output.String())
		}

		if line["level"] != "WARN" || line["latestVersion"] != latest || line["change"] != change {
			t.Errorf("expected a %s update warning for %s, got %s", change, latest, output.String())
		}
	}
}
