package extractor

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

func FetchAtcoderContests() ([]Contest, error) {
	body, err := fetchPage("https://atcoder.jp/contests/")
	if err != nil {
		return nil, err
	}

	html := string(body)

	tbodyIdx := strings.Index(html, "<tbody>")
	if tbodyIdx == -1 {
		return nil, fmt.Errorf("atcoder: no tbody")
	}
	secondTbody := strings.Index(html[tbodyIdx+7:], "<tbody>")
	if secondTbody == -1 {
		return nil, fmt.Errorf("atcoder: no contests table")
	}
	start := tbodyIdx + 7 + secondTbody + 7
	endIdx := strings.Index(html[start:], "</tbody>")
	if endIdx == -1 {
		return nil, fmt.Errorf("atcoder: malformed table")
	}
	table := html[start : start+endIdx]

	var contests []Contest
	for {
		trIdx := strings.Index(table, "<tr>")
		if trIdx == -1 {
			break
		}
		table = table[trIdx+4:]
		endTr := strings.Index(table, "</tr>")
		if endTr == -1 {
			break
		}
		row := table[:endTr]
		table = table[endTr+5:]

		cells := extractCells(row)
		if len(cells) < 3 {
			continue
		}

		href := extractHref(cells[0])
		if href == "" {
			continue
		}

		isoIdx := strings.Index(href, "?iso=")
		if isoIdx == -1 {
			continue
		}
		startTimeStr := href[isoIdx+5:]
		if ampIdx := strings.Index(startTimeStr, "&"); ampIdx != -1 {
			startTimeStr = startTimeStr[:ampIdx]
		}

		startTime, err := time.Parse("20060102T1504", startTimeStr)
		if err != nil {
			continue
		}

		durationStr := stripTags(cells[2])
		colonIdx := strings.Index(durationStr, ":")
		if colonIdx == -1 {
			continue
		}
		hours, _ := strconv.Atoi(durationStr[:colonIdx])
		minutes, _ := strconv.Atoi(durationStr[colonIdx+1:])
		duration := time.Duration(hours)*time.Hour + time.Duration(minutes)*time.Minute

		name := stripTags(extractHrefText(cells[1]))
		if name == "" {
			continue
		}

		url := "https://atcoder.jp" + href
		contests = append(contests, Contest{
			ID:        GenerateContestID("atcoder", startTime.Unix()),
			Name:      name,
			URL:       url,
			StartTime: startTime.Unix(),
			EndTime:   startTime.Add(duration).Unix(),
			Duration:  int64(duration.Seconds()),
			Platform:  "atcoder",
		})
	}

	return contests, nil
}

func extractCells(row string) []string {
	var cells []string
	for {
		tdIdx := strings.Index(row, "<td")
		if tdIdx == -1 {
			break
		}
		closeIdx := strings.Index(row[tdIdx:], ">")
		if closeIdx == -1 {
			break
		}
		contentStart := tdIdx + closeIdx + 1
		endTd := strings.Index(row[contentStart:], "</td>")
		if endTd == -1 {
			break
		}
		cells = append(cells, row[contentStart:contentStart+endTd])
		row = row[contentStart+endTd+5:]
	}
	return cells
}

func extractHref(s string) string {
	hrefIdx := strings.Index(s, `href="`)
	if hrefIdx == -1 {
		return ""
	}
	start := hrefIdx + 6
	end := strings.Index(s[start:], `"`)
	if end == -1 {
		return ""
	}
	return s[start : start+end]
}

func extractHrefText(s string) string {
	aIdx := strings.Index(s, "<a")
	if aIdx == -1 {
		return ""
	}
	closeA := strings.Index(s[aIdx:], ">")
	if closeA == -1 {
		return ""
	}
	contentStart := aIdx + closeA + 1
	endA := strings.Index(s[contentStart:], "</a>")
	if endA == -1 {
		return ""
	}
	return s[contentStart : contentStart+endA]
}

func stripTags(s string) string {
	var b strings.Builder
	inTag := false
	for _, c := range s {
		if c == '<' {
			inTag = true
			continue
		}
		if c == '>' {
			inTag = false
			continue
		}
		if !inTag {
			b.WriteRune(c)
		}
	}
	return strings.TrimSpace(b.String())
}
