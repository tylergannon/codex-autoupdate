package claudefeed

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLatestParsesCurrentRelease(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte(`{
			"currentRelease":"1.24012.1",
			"releases":[{"version":"1.24012.1","updateTo":{
				"version":"1.24012.1",
				"pub_date":"2026-07-21T21:39:38.128532",
				"url":"https://downloads.claude.ai/releases/darwin/universal/1.24012.1/Claude.zip"
			}}]
		}`))
	}))
	defer server.Close()
	got, err := (Client{HTTPClient: server.Client(), FeedURL: server.URL}).Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Build != "1.24012.1" || got.Version != "1.24012.1" {
		t.Fatalf("unexpected release: %+v", got)
	}
}

func TestLatestRejectsMismatchedCurrentRelease(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte(`{"currentRelease":"2.0.0","releases":[{"version":"1.0.0","updateTo":{"url":"https://example.test/Claude.zip"}}]}`))
	}))
	defer server.Close()
	if _, err := (Client{HTTPClient: server.Client(), FeedURL: server.URL}).Latest(context.Background()); err == nil {
		t.Fatal("expected mismatch error")
	}
}
