package sync

import (
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
