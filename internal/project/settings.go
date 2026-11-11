package project

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/ihorbryk/seo-site-checker/internal/check"
	"github.com/ihorbryk/seo-site-checker/internal/dbgen"
)

// Schedules is the schedule enum, in the order the form lists it.
var Schedules = []string{"manual", "hourly", "daily", "weekly"}

// UserAgentSuggestions are offered next to the user agent field.
var UserAgentSuggestions = []string{
	"SEOSiteChecker/0.1",
	"Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)",
	"Mozilla/5.0 (Linux; Android 6.0.1; Nexus 5X Build/MMB29P) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/W.X.Y.Z Mobile Safari/537.36 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)",
}

// RequestSettings are the Request settings the Settings tab's request form
// edits. Exclude patterns are edited elsewhere.
type RequestSettings struct {
	Schedule      string
	UserAgent     string
	Headers       []check.Header // ordered
	TimeoutS      int
	MaxParallel   int
	RespectRobots bool
}

// FieldErrors maps a form field ("schedule", "user_agent", "headers",
// "timeout_s", "max_parallel") to its validation messages.
type FieldErrors map[string][]string

// ParseRequestSettings validates the Request settings form. Header rows are the
// parallel header_name / header_value values, in order; rows with both blank
// are dropped.
func ParseRequestSettings(f url.Values) (RequestSettings, FieldErrors) {
	errs := FieldErrors{}
	s := RequestSettings{
		Schedule:      f.Get("schedule"),
		UserAgent:     strings.TrimSpace(f.Get("user_agent")),
		RespectRobots: f.Get("respect_robots") != "",
	}
	if !slices.Contains(Schedules, s.Schedule) {
		errs["schedule"] = append(errs["schedule"], "Schedule must be one of "+strings.Join(Schedules, ", ")+".")
	}
	if s.UserAgent == "" {
		errs["user_agent"] = append(errs["user_agent"], "User agent is required.")
	} else if strings.ContainsAny(s.UserAgent, "\r\n\x00") {
		errs["user_agent"] = append(errs["user_agent"], "User agent must be a single line.")
	}
	s.TimeoutS = intInRange(errs, f, "timeout_s", "Timeout", 1, 120)
	s.MaxParallel = intInRange(errs, f, "max_parallel", "Max parallel requests", 1, 10)

	names, values := f["header_name"], f["header_value"]
	for i := range max(len(names), len(values)) {
		name, value := strings.TrimSpace(at(names, i)), strings.TrimSpace(at(values, i))
		if name == "" && value == "" {
			continue
		}
		switch {
		case name == "":
			errs["headers"] = append(errs["headers"], fmt.Sprintf("Header row %d has a value but no name.", i+1))
		case !validHeaderName(name):
			errs["headers"] = append(errs["headers"], fmt.Sprintf("%q is not a valid header name.", name))
		case strings.EqualFold(name, "User-Agent"):
			errs["headers"] = append(errs["headers"], "User-Agent is set by the User agent field, not as a custom header.")
		case strings.ContainsAny(value, "\r\n\x00"):
			errs["headers"] = append(errs["headers"], fmt.Sprintf("The value of %s must be a single line.", name))
		default:
			s.Headers = append(s.Headers, check.Header{Name: name, Value: value})
		}
	}
	if len(errs) == 0 {
		return s, nil
	}
	return s, errs
}

func at(xs []string, i int) string {
	if i < len(xs) {
		return xs[i]
	}
	return ""
}

func intInRange(errs FieldErrors, f url.Values, field, label string, lo, hi int) int {
	n, err := strconv.Atoi(strings.TrimSpace(f.Get(field)))
	if err != nil || n < lo || n > hi {
		errs[field] = append(errs[field], fmt.Sprintf("%s must be a whole number from %d to %d.", label, lo, hi))
	}
	return n
}

// validHeaderName reports whether name is an HTTP token (RFC 9110).
func validHeaderName(name string) bool {
	for _, c := range name {
		ok := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
			strings.ContainsRune("!#$%&'*+-.^_`|~", c)
		if !ok {
			return false
		}
	}
	return name != ""
}

// SettingsOf reads a Project's request settings.
func SettingsOf(p dbgen.Project) (RequestSettings, error) {
	cs, err := check.SettingsOf(p)
	if err != nil {
		return RequestSettings{}, err
	}
	return RequestSettings{
		Schedule: p.Schedule, UserAgent: cs.UserAgent, Headers: cs.CustomHeaders,
		TimeoutS: cs.TimeoutS, MaxParallel: cs.MaxParallel, RespectRobots: cs.RespectRobots,
	}, nil
}

// SaveRequestSettings stores s. A running Check keeps the settings it started
// with; the next Check uses these.
func SaveRequestSettings(ctx context.Context, q *dbgen.Queries, projectID int64, s RequestSettings) error {
	headers := s.Headers
	if headers == nil {
		headers = []check.Header{}
	}
	raw, err := json.Marshal(headers)
	if err != nil {
		return err
	}
	return q.UpdateProjectRequestSettings(ctx, dbgen.UpdateProjectRequestSettingsParams{
		ID: projectID, Schedule: s.Schedule, UserAgent: s.UserAgent, CustomHeaders: raw,
		TimeoutS: int32(s.TimeoutS), MaxParallel: int32(s.MaxParallel), RespectRobots: s.RespectRobots,
	})
}
