package extractor

import (
	"encoding/json"
	"time"
)

func FetchCodechefContests() ([]Contest, error) {
	body, err := fetchJSON("GET", "https://www.codechef.com/api/list/contests/all?sort_by=START&sorting_order=asc&offset=0&mode=all", nil)
	if err != nil {
		return nil, err
	}

	var data struct {
		FutureContests []struct {
			Name  string `json:"contest_name"`
			Code  string `json:"contest_code"`
			Start string `json:"contest_start_date_iso"`
			End   string `json:"contest_end_date_iso"`
		} `json:"future_contests"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, err
	}

	var contests []Contest
	for _, c := range data.FutureContests {
		if c.Name == "" || c.Code == "" || c.Start == "" || c.End == "" {
			continue
		}
		startTime, err := time.Parse(time.RFC3339, c.Start)
		if err != nil {
			startTime, err = time.Parse("2006-01-02T15:04:05", c.Start)
			if err != nil {
				continue
			}
		}
		endTime, err := time.Parse(time.RFC3339, c.End)
		if err != nil {
			endTime, err = time.Parse("2006-01-02T15:04:05", c.End)
			if err != nil {
				continue
			}
		}

		url := "https://www.codechef.com/" + c.Code
		contests = append(contests, Contest{
			ID:        GenerateContestID("codechef", startTime.Unix()),
			Name:      c.Name,
			URL:       url,
			StartTime: startTime.Unix(),
			EndTime:   endTime.Unix(),
			Duration:  endTime.Unix() - startTime.Unix(),
			Platform:  "codechef",
		})
	}

	return contests, nil
}
