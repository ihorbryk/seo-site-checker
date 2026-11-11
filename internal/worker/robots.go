package worker

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/ihorbryk/seo-site-checker/internal/check"
	"github.com/ihorbryk/seo-site-checker/internal/dbgen"
	"github.com/ihorbryk/seo-site-checker/internal/robots"
	"github.com/jackc/pgx/v5/pgtype"
)

const (
	// MaxRobotsBytes is how much of robots.txt is read and stored (RFC 9309 asks for at least 500 KiB).
	MaxRobotsBytes = 500 << 10
	// MaxRobotsRedirects is how many redirects a robots.txt fetch follows.
	MaxRobotsRedirects = 5

	SkipRobotsDisallowed  = "robots_disallowed"
	SkipRobotsUnreachable = "robots_unreachable"
)

// robotsResult is what fetching robots.txt produced: a response (Status, any
// code, Body its first 500 KiB) or a transport failure (FailKind).
type robotsResult struct {
	Status   int
	Body     []byte
	FailKind string
}

// fetchRobots fetches https://host/robots.txt with the Check's user agent,
// headers and timeout, following up to five redirects. As with fetch, the only
// error is the parent context ending.
func fetchRobots(ctx context.Context, client *http.Client, host string, s check.Settings) (robotsResult, error) {
	reqCtx, cancel := context.WithTimeout(ctx, time.Duration(s.TimeoutS)*time.Second)
	defer cancel()

	res, err := doFetchRobots(reqCtx, client, "https://"+host+"/robots.txt", s)
	if err != nil && ctx.Err() != nil {
		return robotsResult{}, ctx.Err()
	}
	if err != nil {
		return robotsResult{FailKind: classify(err)}, nil
	}
	return res, nil
}

func doFetchRobots(ctx context.Context, client *http.Client, target string, s check.Settings) (robotsResult, error) {
	for hop := 0; ; hop++ {
		req, err := newRequest(ctx, target, s)
		if err != nil {
			return robotsResult{}, err
		}
		resp, err := client.Do(req)
		if err != nil {
			return robotsResult{}, err
		}
		loc, redirect := redirectTarget(resp, req.URL)
		if redirect && hop < MaxRobotsRedirects {
			resp.Body.Close()
			target = loc
			continue
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(io.LimitReader(resp.Body, MaxRobotsBytes))
		if err != nil {
			return robotsResult{}, err
		}
		return robotsResult{Status: resp.StatusCode, Body: body}, nil
	}
}

// redirectTarget is the absolute Location of a redirect response.
func redirectTarget(resp *http.Response, from *url.URL) (string, bool) {
	switch resp.StatusCode {
	case 301, 302, 303, 307, 308:
	default:
		return "", false
	}
	next, err := from.Parse(resp.Header.Get("Location"))
	if err != nil || resp.Header.Get("Location") == "" {
		return "", false
	}
	return next.String(), true
}

// storeRobots records a robots.txt fetch on the Check.
func storeRobots(ctx context.Context, q *dbgen.Queries, checkID int64, res robotsResult) error {
	params := dbgen.SetCheckRobotsParams{ID: checkID, Body: res.Body}
	if res.FailKind != "" {
		params.FailureKind = pgtype.Text{String: res.FailKind, Valid: true}
	} else {
		params.Status = pgtype.Int4{Int32: int32(res.Status), Valid: true}
	}
	return q.SetCheckRobots(ctx, params)
}

// robotsSkip decides whether the Check's robots.txt forbids fetching pageURL.
// It returns the skip reason, or "" when the Fetch may go ahead.
func robotsSkip(chk dbgen.Check, settings check.Settings, pageURL string) string {
	if !settings.RespectRobots {
		return ""
	}
	if check.RobotsUnreachable(int(chk.RobotsStatus.Int32), chk.RobotsFailureKind.String) {
		return SkipRobotsUnreachable
	}
	if chk.RobotsStatus.Int32 < 200 || chk.RobotsStatus.Int32 >= 300 {
		return "" // 4xx (or a redirect loop): everything is allowed
	}
	u, err := url.Parse(pageURL)
	if err != nil {
		return ""
	}
	if !robots.Parse(chk.RobotsBody).Allowed(settings.UserAgent, u.RequestURI()) {
		return SkipRobotsDisallowed
	}
	return ""
}
