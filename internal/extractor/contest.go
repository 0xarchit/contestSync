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

var httpClient = &http.Client{
	Timeout: 12 * time.Second,
	Transport: &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
		DisableKeepAlives:   false,
	},
}

func GenerateContestID(platform string, startTime int64) string {
	return fmt.Sprintf("%s_%d", platform, startTime)
}

func fetchJSON(method, url string, body []byte) ([]byte, error) {
	var respBody []byte
	var lastErr error

	for attempt := 1; attempt <= 3; attempt++ {
		var reqBody io.Reader
		if len(body) > 0 {
			reqBody = bytes.NewReader(body)
		}

		req, err := http.NewRequest(method, url, reqBody)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")
		req.Header.Set("Accept", "application/json, text/plain, */*")
		req.Header.Set("Accept-Language", "en-US,en;q=0.9")

		resp, err := httpClient.Do(req)
		if err != nil {
			lastErr = err
			if attempt < 3 {
				time.Sleep(time.Duration(attempt) * 500 * time.Millisecond)
				continue
			}
			return nil, err
		}

		respBody, err = io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
		resp.Body.Close()
		if err != nil {
			lastErr = err
			if attempt < 3 {
				time.Sleep(time.Duration(attempt) * 500 * time.Millisecond)
				continue
			}
			return nil, err
		}

		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("HTTP request failed with status %d", resp.StatusCode)
			if attempt < 3 {
				time.Sleep(time.Duration(attempt) * 1 * time.Second)
				continue
			}
			return nil, lastErr
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("HTTP request failed with status %d", resp.StatusCode)
		}

		return respBody, nil
	}

	return nil, lastErr
}

func fetchPage(url string) ([]byte, error) {
	var respBody []byte
	var lastErr error

	for attempt := 1; attempt <= 3; attempt++ {
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")
		req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
		req.Header.Set("Accept-Language", "en-US,en;q=0.9")

		resp, err := httpClient.Do(req)
		if err != nil {
			lastErr = err
			if attempt < 3 {
				time.Sleep(time.Duration(attempt) * 500 * time.Millisecond)
				continue
			}
			return nil, err
		}

		respBody, err = io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
		resp.Body.Close()
		if err != nil {
			lastErr = err
			if attempt < 3 {
				time.Sleep(time.Duration(attempt) * 500 * time.Millisecond)
				continue
			}
			return nil, err
		}

		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("HTTP request failed with status %d", resp.StatusCode)
			if attempt < 3 {
				time.Sleep(time.Duration(attempt) * 1 * time.Second)
				continue
			}
			return nil, lastErr
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("HTTP request failed with status %d", resp.StatusCode)
		}

		return respBody, nil
	}

	return nil, lastErr
}
