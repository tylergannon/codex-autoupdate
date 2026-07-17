package appcast

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
)

func TestLatestSelectsHighestCompatibleBuild(t *testing.T) {
	t.Parallel()
	otherArchitecture := "amd64"
	if runtime.GOARCH == "amd64" {
		otherArchitecture = "arm64"
	}
	feed := fmt.Sprintf(`<?xml version="1.0"?><rss xmlns:sparkle="http://www.andymatuschak.org/xml-namespaces/sparkle"><channel>
<item><title>compatible</title><sparkle:version>12</sparkle:version><sparkle:shortVersionString>1.2</sparkle:shortVersionString><sparkle:hardwareRequirements>%s</sparkle:hardwareRequirements><enclosure url="https://example.test/12.zip" length="12" sparkle:edSignature="sig"/></item>
<item><title>older</title><sparkle:version>11</sparkle:version><sparkle:hardwareRequirements>%s</sparkle:hardwareRequirements><enclosure url="https://example.test/11.zip" length="11"/></item>
<item><title>wrong architecture</title><sparkle:version>99</sparkle:version><sparkle:hardwareRequirements>%s</sparkle:hardwareRequirements><enclosure url="https://example.test/99.zip" length="99"/></item>
</channel></rss>`, runtime.GOARCH, runtime.GOARCH, otherArchitecture)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte(feed))
	}))
	defer server.Close()

	release, err := (Client{HTTPClient: server.Client(), FeedURL: server.URL}).Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if release.Build != 12 || release.Version != "1.2" || release.Length != 12 {
		t.Fatalf("unexpected release: %+v", release)
	}
}

func TestLatestRejectsNonTLSRemoteFeed(t *testing.T) {
	t.Parallel()
	_, err := (Client{FeedURL: "http://example.com/appcast.xml"}).Latest(context.Background())
	if err == nil || !strings.Contains(err.Error(), "must use HTTPS") {
		t.Fatalf("expected HTTPS error, got %v", err)
	}
}
