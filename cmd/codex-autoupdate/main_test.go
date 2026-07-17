package main

import "testing"

func TestBuildVersionHonorsLinkerOverride(t *testing.T) {
	original := version
	version = "v-test"
	t.Cleanup(func() { version = original })

	if got := buildVersion(); got != "v-test" {
		t.Fatalf("buildVersion() = %q, want v-test", got)
	}
}
