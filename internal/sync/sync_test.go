package sync

import (
	"context"
	"fmt"
	"regexp"
	"testing"
)

func TestGenerateDeterministicEventID(t *testing.T) {
	googleID := "1122334455667788"
	contestID := "codeforces_999"

	id1 := GenerateDeterministicEventID(googleID, contestID)
	id2 := GenerateDeterministicEventID(googleID, contestID)

	if id1 != id2 {
		t.Errorf("expected deterministic outputs, got %q and %q", id1, id2)
	}

	id3 := GenerateDeterministicEventID("8877665544332211", contestID)
	if id1 == id3 {
		t.Errorf("expected different outputs for different Google IDs, got matching %q", id1)
	}

	id4 := GenerateDeterministicEventID(googleID, "codeforces_888")
	if id1 == id4 {
		t.Errorf("expected different outputs for different contest IDs, got matching %q", id1)
	}

	matched, err := regexp.MatchString("^[a-v0-9]+$", id1)
	if err != nil {
		t.Fatalf("regex failed: %v", err)
	}
	if !matched {
		t.Errorf("expected base32hex compatible lowercase format, got %q", id1)
	}
}

func TestHandleSyncError(t *testing.T) {
	s := &Syncer{}

	deleted, err := s.handleSyncError(context.Background(), 1, nil)
	if deleted {
		t.Error("expected deleted to be false for nil error")
	}
	if err != nil {
		t.Errorf("expected err to be nil for nil error, got %v", err)
	}

	invalidGrantErr := fmt.Errorf("oauth2: cannot fetch token: 400 Bad Request\nResponse: {\"error\":\"invalid_grant\",\"error_description\":\"Token has been expired or revoked.\"}")
	deleted, err = s.handleSyncError(context.Background(), 1, invalidGrantErr)
	if !deleted {
		t.Error("expected deleted to be true for invalid_grant error")
	}
	if err != nil {
		t.Errorf("expected err to be nil for invalid_grant error, got %v", err)
	}

	otherErr := fmt.Errorf("connection timeout")
	deleted, err = s.handleSyncError(context.Background(), 1, otherErr)
	if deleted {
		t.Error("expected deleted to be false for non-oauth error")
	}
	if err == nil || err.Error() != "connection timeout" {
		t.Errorf("expected connection timeout error, got %v", err)
	}
}
