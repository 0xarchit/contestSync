package extractor

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"time"
)

const maxResponseSize = 10 * 1024 * 1024

type Contest struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	URL       string `json:"url"`
	StartTime int64  `json:"start_time"`
	EndTime   int64  `json:"end_time"`
	Duration  int64  `json:"duration"`
	Platform  string `json:"platform"`
}

var Platforms = []string{
	"codechef",
	"codeforces",
	"leetcode",
	"atcoder",
	"hackerrank",
	"geeksforgeeks",
	"code360",
}

type Fetcher func() ([]Contest, error)

var Fetchers = map[string]Fetcher{
	"codechef":      FetchCodechefContests,
	"codeforces":    FetchCodeforcesContests,
	"leetcode":      FetchLeetcodeContests,
	"atcoder":       FetchAtcoderContests,
	"hackerrank":    FetchHackerrankContests,
	"geeksforgeeks": FetchGeeksforGeeksContests,
	"code360":       FetchCode360Contests,
}

func GenerateContestID(platform string, startTime int64) string {
	return fmt.Sprintf("%s_%d", platform, startTime)
}

func fetchJSON(method, url string, body []byte) ([]byte, error) {
	var reqBody io.Reader
	if len(body) > 0 {
		reqBody = bytes.NewReader(body)
	}

	req, err := http.NewRequest(method, url, reqBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP request failed with status %d", resp.StatusCode)
	}

	return io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
}

func fetchPage(url string) ([]byte, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP request failed with status %d", resp.StatusCode)
	}

	return io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
}
