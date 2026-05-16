package extractor

import (
	"encoding/json"
	"time"
)

func FetchGeeksforGeeksContests() ([]Contest, error) {
	body, err := fetchJSON("GET", "https://practiceapi.geeksforgeeks.org/api/vr/events/?page_number=1&sub_type=all&type=contest", nil)
	if err != nil {
		return nil, err
	}

	var data struct {
		Results struct {
			Upcoming []struct {
				Name      string `json:"name"`
				Slug      string `json:"slug"`
				StartTime string `json:"start_time"`
				EndTime   string `json:"end_time"`
			} `json:"upcoming"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, err
	}

	var contests []Contest
	for _, c := range data.Results.Upcoming {
		if c.Name == "" || c.Slug == "" || c.StartTime == "" || c.EndTime == "" {
			continue
		}
		startTime, err := time.Parse("2006-01-02T15:04:05", c.StartTime)
		if err != nil {
			continue
		}
		endTime, err := time.Parse("2006-01-02T15:04:05", c.EndTime)
		if err != nil {
			continue
		}

		url := "https://practice.geeksforgeeks.org/contest/" + c.Slug
		contests = append(contests, Contest{
			ID:        GenerateContestID("geeksforgeeks", startTime.Unix()),
			Name:      c.Name,
			URL:       url,
			StartTime: startTime.Unix(),
			EndTime:   endTime.Unix(),
			Duration:  endTime.Unix() - startTime.Unix(),
			Platform:  "geeksforgeeks",
		})
	}

	return contests, nil
}
