package extractor

import (
	"encoding/json"
	"fmt"
	"time"
)

func FetchCodeforcesContests() ([]Contest, error) {
	body, err := fetchJSON("GET", "https://codeforces.com/api/contest.list", nil)
	if err != nil {
		return nil, err
	}

	var data struct {
		Status string `json:"status"`
		Result []struct {
			ID              float64 `json:"id"`
			Name            string  `json:"name"`
			Phase           string  `json:"phase"`
			StartTimeSec    float64 `json:"startTimeSeconds"`
			DurationSeconds float64 `json:"durationSeconds"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, err
	}

	if data.Status != "OK" {
		return nil, fmt.Errorf("codeforces: API returned non-OK status")
	}

	now := time.Now().Unix()
	var contests []Contest
	for _, c := range data.Result {
		if c.Phase != "BEFORE" || int64(c.StartTimeSec) < now {
			continue
		}
		url := "https://codeforces.com/contestRegistration/" + fmt.Sprintf("%.0f", c.ID)
		contests = append(contests, Contest{
			ID:        GenerateContestID("codeforces", int64(c.StartTimeSec)),
			Name:      c.Name,
			URL:       url,
			StartTime: int64(c.StartTimeSec),
			EndTime:   int64(c.StartTimeSec) + int64(c.DurationSeconds),
			Duration:  int64(c.DurationSeconds),
			Platform:  "codeforces",
		})
	}

	return contests, nil
}
