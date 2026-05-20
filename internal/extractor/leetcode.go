package extractor

import (
	"encoding/json"
	"fmt"
	"time"
)

func FetchLeetcodeContests() ([]Contest, error) {
	query := `{"query":"{allContests{title,titleSlug,startTime,duration}}"}`
	body, err := fetchJSON("POST", "https://leetcode.com/graphql", []byte(query))
	if err != nil {
		return nil, err
	}

	var data struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
		Data struct {
			Contests []struct {
				Title     string  `json:"title"`
				TitleSlug string  `json:"titleSlug"`
				StartTime float64 `json:"startTime"`
				Duration  float64 `json:"duration"`
			} `json:"allContests"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, err
	}

	if len(data.Errors) > 0 {
		return nil, fmt.Errorf("leetcode graphql error: %s", data.Errors[0].Message)
	}

	now := float64(time.Now().Unix())
	var contests []Contest
	for _, c := range data.Data.Contests {
		if c.StartTime+c.Duration < now {
			continue
		}
		url := "https://leetcode.com/contest/" + c.TitleSlug
		contests = append(contests, Contest{
			ID:        GenerateContestID("leetcode", int64(c.StartTime)),
			Name:      c.Title,
			URL:       url,
			StartTime: int64(c.StartTime),
			EndTime:   int64(c.StartTime + c.Duration),
			Duration:  int64(c.Duration),
			Platform:  "leetcode",
		})
	}

	return contests, nil
}
