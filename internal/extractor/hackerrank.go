package extractor

import (
	"encoding/json"
	"net/url"
	"strings"
	"time"
)

func FetchHackerrankContests() ([]Contest, error) {
	body, err := fetchJSON("GET", "https://www.hackerrank.com/community/engage/events", nil)
	if err != nil {
		return nil, err
	}

	var data struct {
		Data struct {
			Events struct {
				Ongoing []struct {
					Attrs struct {
						Name      string `json:"name"`
						URL       string `json:"microsite_url"`
						StartTime string `json:"start_time"`
						EndTime   string `json:"end_time"`
					} `json:"attributes"`
				} `json:"ongoing_events"`
			} `json:"events"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, err
	}

	var contests []Contest
	for _, e := range data.Data.Events.Ongoing {
		a := e.Attrs
		if a.Name == "" || a.URL == "" || a.StartTime == "" || a.EndTime == "" {
			continue
		}
		parsed, err := url.Parse(a.URL)
		if err != nil || parsed.Scheme != "https" || !strings.HasSuffix(parsed.Host, "hackerrank.com") {
			continue
		}
		startTime, err := parseISO(a.StartTime)
		if err != nil {
			continue
		}
		endTime, err := parseISO(a.EndTime)
		if err != nil {
			continue
		}

		contests = append(contests, Contest{
			ID:        GenerateContestID("hackerrank", startTime.Unix()),
			Name:      a.Name,
			URL:       a.URL,
			StartTime: startTime.Unix(),
			EndTime:   endTime.Unix(),
			Duration:  endTime.Unix() - startTime.Unix(),
			Platform:  "hackerrank",
		})
	}

	return contests, nil
}

func parseISO(s string) (time.Time, error) {
	s = strings.Replace(s, "Z", "+00:00", 1)
	t, err := time.Parse(time.RFC3339, s)
	return t, err
}
