package claudefeed

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/tylergannon/codex-autoupdate/internal/release"
)

const DefaultURL = "https://api.anthropic.com/api/desktop/darwin/universal/squirrel/update?device_id=codex-autoupdate"

type Client struct {
	HTTPClient *http.Client
	FeedURL    string
}

func (c Client) Latest(ctx context.Context) (release.Release, error) {
	feedURL := c.FeedURL
	if feedURL == "" {
		feedURL = DefaultURL
	}
	parsed, err := url.Parse(feedURL)
	if err != nil {
		return release.Release{}, fmt.Errorf("parse Claude update URL: %w", err)
	}
	if parsed.Scheme != "https" && (parsed.Scheme != "http" || !isLoopback(parsed.Hostname())) {
		return release.Release{}, fmt.Errorf("claude update URL must use HTTPS: %s", feedURL)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, feedURL, nil)
	if err != nil {
		return release.Release{}, fmt.Errorf("create Claude update request: %w", err)
	}
	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return release.Release{}, fmt.Errorf("fetch Claude update feed: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return release.Release{}, fmt.Errorf("fetch Claude update feed: unexpected HTTP status %s", response.Status)
	}
	var payload feed
	if err := json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&payload); err != nil {
		return release.Release{}, fmt.Errorf("decode Claude update feed: %w", err)
	}
	current := strings.TrimSpace(payload.CurrentRelease)
	for _, candidate := range payload.Releases {
		if strings.TrimSpace(candidate.Version) != current {
			continue
		}
		result := release.Release{
			Build:   current,
			Version: current,
			URL:     strings.TrimSpace(candidate.UpdateTo.URL),
		}
		if publication := strings.TrimSpace(candidate.UpdateTo.PublicationTime); publication != "" {
			result.PublicationTime, _ = time.Parse("2006-01-02T15:04:05.999999", publication)
		}
		if err := result.Validate(); err != nil {
			return release.Release{}, fmt.Errorf("invalid Claude release: %w", err)
		}
		return result, nil
	}
	return release.Release{}, fmt.Errorf("claude update feed does not contain current release %q", current)
}

type feed struct {
	CurrentRelease string          `json:"currentRelease"`
	Releases       []releaseRecord `json:"releases"`
}

type releaseRecord struct {
	Version  string `json:"version"`
	UpdateTo struct {
		URL             string `json:"url"`
		PublicationTime string `json:"pub_date"`
	} `json:"updateTo"`
}

func isLoopback(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}
