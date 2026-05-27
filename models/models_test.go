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
	if SyncedEventsCacheTTL != 24*time.Hour {
		t.Errorf("expected SyncedEventsCacheTTL to be 24h, got %v", SyncedEventsCacheTTL)
	}
	if PlatformsCacheTTL != 24*time.Hour {
		t.Errorf("expected PlatformsCacheTTL to be 24h, got %v", PlatformsCacheTTL)
	}

	uKey := UserCacheKey(123)
	if uKey != "cache:user:123" {
		t.Errorf("expected 'cache:user:123', got %q", uKey)
	}

	cKey := ContestsCacheKey("codeforces")
	if cKey != "cache:contests:codeforces" {
		t.Errorf("expected 'cache:contests:codeforces', got %q", cKey)
	}

	seKey := SyncedEventsCacheKey(123)
	if seKey != "cache:synced_events:123" {
		t.Errorf("expected 'cache:synced_events:123', got %q", seKey)
	}

	pKey := PlatformsCacheKey()
	if pKey != "cache:platforms" {
		t.Errorf("expected 'cache:platforms', got %q", pKey)
	}
}
