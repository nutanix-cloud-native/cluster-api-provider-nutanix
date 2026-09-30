package version

import (
	"strings"
	"testing"
)

func TestUserAgent(t *testing.T) {
	// Default should be capx/dev when GitCommit is dev
	original := GitCommit
	defer func() { GitCommit = original }()

	GitCommit = "dev"
	ua := UserAgent()
	if ua != "capx/dev" {
		t.Fatalf("expected capx/dev, got %s", ua)
	}

	GitCommit = "abc123"
	ua = UserAgent()
	if ua != "capx/abc123" {
		t.Fatalf("expected capx/abc123, got %s", ua)
	}

	GitCommit = ""
	ua = UserAgent()
	if ua != "capx/dev" {
		t.Fatalf("expected capx/dev for empty, got %s", ua)
	}

	if !strings.HasPrefix(ua, "capx/") {
		t.Fatalf("UserAgent should start with capx/, got %s", ua)
	}
}

func TestUserAgentWithComponent(t *testing.T) {
	original := GitCommit
	defer func() { GitCommit = original }()
	GitCommit = "v1.10.0"

	ua := UserAgentWithComponent("cluster-api-provider-nutanix")
	if ua != "cluster-api-provider-nutanix/v1.10.0" {
		t.Fatalf("expected cluster-api-provider-nutanix/v1.10.0, got %s", ua)
	}
}
