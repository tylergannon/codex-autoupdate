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
<item><title>newer macOS required</title><sparkle:version>100</sparkle:version><sparkle:minimumSystemVersion>99.0</sparkle:minimumSystemVersion><sparkle:hardwareRequirements>%s</sparkle:hardwareRequirements><enclosure url="https://example.test/100.zip" length="100"/></item>
</channel></rss>`, runtime.GOARCH, runtime.GOARCH, otherArchitecture, runtime.GOARCH)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte(feed))
	}))
	defer server.Close()

	release, err := (Client{HTTPClient: server.Client(), FeedURL: server.URL, HostVersion: "26.1"}).Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if release.Build != 12 || release.Version != "1.2" || release.Length != 12 {
		t.Fatalf("unexpected release: %+v", release)
	}
}

func TestCompareNumericVersions(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		left, right string
		want        int
	}{
		{"15.4", "15.3.9", 1},
		{"15.4", "15.4.0", 0},
		{"14.9", "15.0", -1},
	} {
		if got := compareNumericVersions(test.left, test.right); got != test.want {
			t.Errorf("compareNumericVersions(%q, %q) = %d, want %d", test.left, test.right, got, test.want)
		}
	}
}

func TestNumericVersionValidation(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"15.4", "15.4.0", "26"} {
		if !isNumericVersion(value) {
			t.Errorf("expected %q to be valid", value)
		}
	}
	for _, value := range []string{"", "15.beta", "15.", "v15"} {
		if isNumericVersion(value) {
			t.Errorf("expected %q to be invalid", value)
		}
	}
}

func TestLatestRejectsNonTLSRemoteFeed(t *testing.T) {
	t.Parallel()
	_, err := (Client{FeedURL: "http://example.com/appcast.xml"}).Latest(context.Background())
	if err == nil || !strings.Contains(err.Error(), "must use HTTPS") {
		t.Fatalf("expected HTTPS error, got %v", err)
	}
}
