package extractor

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"
)

type mockTransport struct {
	roundTripFunc func(req *http.Request) (*http.Response, error)
}

func (m *mockTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return m.roundTripFunc(req)
}

func setMockTransport(fn func(req *http.Request) (*http.Response, error)) func() {
	orig := httpClient.Transport
	httpClient.Transport = &mockTransport{roundTripFunc: fn}
	return func() {
		httpClient.Transport = orig
	}
}

func TestFetchCodeforces(t *testing.T) {
	restore := setMockTransport(func(req *http.Request) (*http.Response, error) {
		res := `{
			"status": "OK",
			"result": [
				{
					"id": 1234,
					"name": "Codeforces Round 999",
					"phase": "BEFORE",
					"startTimeSeconds": 1900000000,
					"durationSeconds": 7200
				},
				{
					"id": 5678,
					"name": "Old Contest",
					"phase": "FINISHED",
					"startTimeSeconds": 1000000000,
					"durationSeconds": 7200
				}
			]
		}`
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(bytes.NewBufferString(res)),
			Header:     make(http.Header),
		}, nil
	})
	defer restore()

	contests, err := FetchCodeforcesContests()
	if err != nil {
		t.Fatalf("failed to fetch: %v", err)
	}

	if len(contests) != 1 {
		t.Fatalf("expected 1 contest, got %d", len(contests))
	}

	if contests[0].Name != "Codeforces Round 999" {
		t.Errorf("expected contest name, got %q", contests[0].Name)
	}
}

func TestFetchCodechef(t *testing.T) {
	restore := setMockTransport(func(req *http.Request) (*http.Response, error) {
		res := `{
			"status": "success",
			"future_contests": [
				{
					"contest_code": "START999",
					"contest_name": "Starters 999",
					"contest_start_date_iso": "2026-05-25T14:00:00+05:30",
					"contest_end_date_iso": "2026-05-25T16:00:00+05:30",
					"contest_duration": "120"
				}
			]
		}`
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(bytes.NewBufferString(res)),
			Header:     make(http.Header),
		}, nil
	})
	defer restore()

	contests, err := FetchCodechefContests()
	if err != nil {
		t.Fatalf("failed to fetch: %v", err)
	}

	if len(contests) != 1 {
		t.Fatalf("expected 1 contest, got %d", len(contests))
	}

	if contests[0].Name != "Starters 999" {
		t.Errorf("expected contest name, got %q", contests[0].Name)
	}
}

func TestFetchLeetcode(t *testing.T) {
	restore := setMockTransport(func(req *http.Request) (*http.Response, error) {
		res := `{
			"data": {
				"allContests": [
					{
						"titleSlug": "weekly-contest-999",
						"title": "Weekly Contest 999",
						"startTime": 1900000000,
						"duration": 5400
					}
				]
			}
		}`
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(bytes.NewBufferString(res)),
			Header:     make(http.Header),
		}, nil
	})
	defer restore()

	contests, err := FetchLeetcodeContests()
	if err != nil {
		t.Fatalf("failed to fetch: %v", err)
	}

	if len(contests) != 1 {
		t.Fatalf("expected 1 contest, got %d", len(contests))
	}

	if contests[0].Name != "Weekly Contest 999" {
		t.Errorf("expected contest name, got %q", contests[0].Name)
	}
}

func TestFetchAtcoder(t *testing.T) {
	restore := setMockTransport(func(req *http.Request) (*http.Response, error) {
		res := `<html><body>
			<tbody>
				<tr><td>Dummy Table</td></tr>
			</tbody>
			<tbody>
				<tr>
					<td><a href="/contests/abc999?iso=20260525T2100">Time Link</a></td>
					<td><a href="/contests/abc999">AtCoder Beginner Contest 999</a></td>
					<td>01:40</td>
				</tr>
			</tbody>
		</body></html>`
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(bytes.NewBufferString(res)),
			Header:     make(http.Header),
		}, nil
	})
	defer restore()

	contests, err := FetchAtcoderContests()
	if err != nil {
		t.Fatalf("failed to fetch: %v", err)
	}

	if len(contests) != 1 {
		t.Fatalf("expected 1 contest, got %d", len(contests))
	}

	if contests[0].Name != "AtCoder Beginner Contest 999" {
		t.Errorf("expected contest name, got %q", contests[0].Name)
	}
}

func TestFetchHackerrank(t *testing.T) {
	restore := setMockTransport(func(req *http.Request) (*http.Response, error) {
		res := `{
			"data": {
				"events": {
					"ongoing_events": [
						{
							"attributes": {
								"name": "HackerRank Hack 999",
								"microsite_url": "https://www.hackerrank.com/hack999",
								"start_time": "2026-05-25T14:00:00Z",
								"end_time": "2026-05-25T16:00:00Z"
							}
						},
						{
							"attributes": {
								"name": "Untrusted Event",
								"microsite_url": "http://evil.com/hack",
								"start_time": "2026-05-25T14:00:00Z",
								"end_time": "2026-05-25T16:00:00Z"
							}
						}
					]
				}
			}
		}`
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(bytes.NewBufferString(res)),
			Header:     make(http.Header),
		}, nil
	})
	defer restore()

	contests, err := FetchHackerrankContests()
	if err != nil {
		t.Fatalf("failed to fetch: %v", err)
	}

	if len(contests) != 1 {
		t.Fatalf("expected 1 contest (untrusted skipped), got %d", len(contests))
	}

	if contests[0].Name != "HackerRank Hack 999" {
		t.Errorf("expected contest name, got %q", contests[0].Name)
	}
}

func TestFetchGeeksforgeeks(t *testing.T) {
	restore := setMockTransport(func(req *http.Request) (*http.Response, error) {
		res := `{
			"results": {
				"upcoming": [
					{
						"slug": "gfg-weekly-999",
						"name": "GFG Weekly 999",
						"start_time": "2026-05-25T14:00:00",
						"end_time": "2026-05-25T16:00:00"
					}
				]
			}
		}`
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(bytes.NewBufferString(res)),
			Header:     make(http.Header),
		}, nil
	})
	defer restore()

	contests, err := FetchGeeksforGeeksContests()
	if err != nil {
		t.Fatalf("failed to fetch: %v", err)
	}

	if len(contests) != 1 {
		t.Fatalf("expected 1 contest, got %d", len(contests))
	}

	if contests[0].Name != "GFG Weekly 999" {
		t.Errorf("expected contest name, got %q", contests[0].Name)
	}
}

func TestFetchCode360(t *testing.T) {
	restore := setMockTransport(func(req *http.Request) (*http.Response, error) {
		res := `{
			"data": {
				"events": [
					{
						"slug": "code360-contest-999",
						"name": "Code360 Contest 999",
						"registration_end_time": 1900000000,
						"event_start_time": 1900000000,
						"event_end_time": 1900007200
					}
				]
			}
		}`
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(bytes.NewBufferString(res)),
			Header:     make(http.Header),
		}, nil
	})
	defer restore()

	contests, err := FetchCode360Contests()
	if err != nil {
		t.Fatalf("failed to fetch: %v", err)
	}

	if len(contests) != 1 {
		t.Fatalf("expected 1 contest, got %d", len(contests))
	}

	if contests[0].Name != "Code360 Contest 999" {
		t.Errorf("expected contest name, got %q", contests[0].Name)
	}
}

func TestFetchErrorStatus(t *testing.T) {
	restore := setMockTransport(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 500,
			Body:       io.NopCloser(strings.NewReader("internal error")),
			Header:     make(http.Header),
		}, nil
	})
	defer restore()

	_, err := FetchCodeforcesContests()
	if err == nil {
		t.Error("expected error for 500 status code, got nil")
	}
}
