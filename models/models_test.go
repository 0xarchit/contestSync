package models

import (
	"testing"
	"time"
)

func TestCacheKeysAndTTLs(t *testing.T) {
	if UserCacheTTL != 24*time.Hour {
		t.Errorf("expected UserCacheTTL to be 24h, got %v", UserCacheTTL)
	}
	if ContestsCacheTTL != 12*time.Hour {
		t.Errorf("expected ContestsCacheTTL to be 12h, got %v", ContestsCacheTTL)
	}

	uKey := UserCacheKey(123)
	if uKey != "cache:user:123" {
		t.Errorf("expected 'cache:user:123', got %q", uKey)
	}

	cKey := ContestsCacheKey("codeforces")
	if cKey != "cache:contests:codeforces" {
		t.Errorf("expected 'cache:contests:codeforces', got %q", cKey)
	}
}
