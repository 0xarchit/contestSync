package extractor

import (
	"encoding/json"
	"time"
)

func FetchCode360Contests() ([]Contest, error) {
	body, err := fetchJSON("GET", "https://www.naukri.com/code360/api/v4/public_section/contest_list", nil)
	if err != nil {
		return nil, err
	}

	var data struct {
		Data struct {
			Events []struct {
				Name     string  `json:"name"`
				Slug     string  `json:"slug"`
				RegStart float64 `json:"registration_start_time"`
				RegEnd   float64 `json:"registration_end_time"`
				EventEnd float64 `json:"event_end_time"`
			} `json:"events"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, err
	}

	now := float64(time.Now().Unix())
	var contests []Contest
	for _, e := range data.Data.Events {
		if e.RegEnd < now || e.Name == "" || e.Slug == "" {
			continue
		}

		url := "https://www.naukri.com/code360/contests/" + e.Slug
		contests = append(contests, Contest{
			ID:        GenerateContestID("code360", int64(e.RegStart)),
			Name:      e.Name,
			URL:       url,
			StartTime: int64(e.RegStart),
			EndTime:   int64(e.EventEnd),
			Duration:  int64(e.EventEnd - e.RegStart),
			Platform:  "code360",
		})
	}

	return contests, nil
}
