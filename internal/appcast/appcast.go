package appcast

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const DefaultURL = "https://persistent.oaistatic.com/codex-app-prod/appcast.xml"

type Release struct {
	Build            int64
	Version          string
	MinimumSystem    string
	Architecture     string
	URL              string
	Length           int64
	SparkleSignature string
	PublicationTime  time.Time
}

type Client struct {
	HTTPClient *http.Client
	FeedURL    string
}

func (c Client) Latest(ctx context.Context) (Release, error) {
	feedURL := c.FeedURL
	if feedURL == "" {
		feedURL = DefaultURL
	}
	parsed, err := url.Parse(feedURL)
	if err != nil {
		return Release{}, fmt.Errorf("parse appcast URL: %w", err)
	}
	if parsed.Scheme != "https" && (parsed.Scheme != "http" || !isLoopback(parsed.Hostname())) {
		return Release{}, fmt.Errorf("appcast URL must use HTTPS: %s", feedURL)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, feedURL, nil)
	if err != nil {
		return Release{}, fmt.Errorf("create appcast request: %w", err)
	}
	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return Release{}, fmt.Errorf("fetch appcast: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("fetch appcast: unexpected HTTP status %s", resp.Status)
	}

	var feed rss
	decoder := xml.NewDecoder(io.LimitReader(resp.Body, 4<<20))
	if err := decoder.Decode(&feed); err != nil {
		return Release{}, fmt.Errorf("decode appcast: %w", err)
	}

	var latest Release
	for _, item := range feed.Channel.Items {
		release, err := item.release()
		if err != nil {
			continue
		}
		if release.Architecture != "" && release.Architecture != runtime.GOARCH {
			continue
		}
		if release.Build > latest.Build {
			latest = release
		}
	}
	if latest.Build == 0 {
		return Release{}, fmt.Errorf("appcast contains no compatible %s release", runtime.GOARCH)
	}
	return latest, nil
}

type rss struct {
	Channel channel `xml:"channel"`
}

type channel struct {
	Items []item `xml:"item"`
}

type item struct {
	Title         string    `xml:"title"`
	PubDate       string    `xml:"pubDate"`
	Build         string    `xml:"version"`
	ShortVersion  string    `xml:"shortVersionString"`
	MinimumSystem string    `xml:"minimumSystemVersion"`
	Hardware      string    `xml:"hardwareRequirements"`
	Enclosure     enclosure `xml:"enclosure"`
}

type enclosure struct {
	URL              string `xml:"url,attr"`
	Length           int64  `xml:"length,attr"`
	SparkleSignature string `xml:"edSignature,attr"`
}

func (i item) release() (Release, error) {
	build, err := strconv.ParseInt(strings.TrimSpace(i.Build), 10, 64)
	if err != nil || build <= 0 {
		return Release{}, fmt.Errorf("invalid appcast build %q", i.Build)
	}
	downloadURL, err := url.Parse(i.Enclosure.URL)
	if err != nil || downloadURL.Scheme != "https" {
		return Release{}, fmt.Errorf("invalid enclosure URL %q", i.Enclosure.URL)
	}
	if i.Enclosure.Length <= 0 {
		return Release{}, fmt.Errorf("invalid enclosure length %d", i.Enclosure.Length)
	}
	version := strings.TrimSpace(i.ShortVersion)
	if version == "" {
		version = strings.TrimSpace(i.Title)
	}
	publicationTime, _ := time.Parse(time.RFC1123Z, strings.TrimSpace(i.PubDate))
	return Release{
		Build:            build,
		Version:          version,
		MinimumSystem:    strings.TrimSpace(i.MinimumSystem),
		Architecture:     normalizeArchitecture(strings.TrimSpace(i.Hardware)),
		URL:              downloadURL.String(),
		Length:           i.Enclosure.Length,
		SparkleSignature: strings.TrimSpace(i.Enclosure.SparkleSignature),
		PublicationTime:  publicationTime,
	}, nil
}

func normalizeArchitecture(value string) string {
	switch value {
	case "arm64":
		return "arm64"
	case "x86_64", "amd64":
		return "amd64"
	default:
		return value
	}
}

func isLoopback(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}
