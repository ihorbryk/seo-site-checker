package worker

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/ihorbryk/seo-site-checker/internal/check"
)

// MaxBodyBytes caps a stored HTML body, after content decoding.
const MaxBodyBytes = 10 << 20

// Failure kinds of a Fetch failure.
const (
	FailTimeout      = "timeout"
	FailDNS          = "dns"
	FailTLS          = "tls"
	FailConnection   = "connection"
	FailBodyTooLarge = "body_too_large"
	FailInternal     = "internal"
)

// NewHTTPClient returns the production fetch client: it never follows
// redirects (each hop is its own Fetch).
func NewHTTPClient() *http.Client {
	return &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// fetchResult is what one Fetch produced: a response (any status) or a failure.
type fetchResult struct {
	Status  int
	Headers [][2]string // sorted by name; Go's http.Header does not keep wire order
	Body    []byte      // HTML responses only
	HasBody bool

	FailKind string // empty when a response was received
	FailMsg  string

	Duration time.Duration
}

// fetch performs one request with the Check's settings. Transport errors are
// results, not errors; the only error is the parent context ending, which the
// caller must treat as a job error so River retries the lane.
func fetch(ctx context.Context, client *http.Client, url string, s check.Settings) (fetchResult, error) {
	start := time.Now()
	reqCtx, cancel := context.WithTimeout(ctx, time.Duration(s.TimeoutS)*time.Second)
	defer cancel()

	res, err := doFetch(reqCtx, client, url, s)
	res.Duration = time.Since(start)
	if err != nil && ctx.Err() != nil {
		return fetchResult{}, ctx.Err()
	}
	if err != nil {
		res.FailKind, res.FailMsg = classify(err), err.Error()
	}
	return res, nil
}

func doFetch(ctx context.Context, client *http.Client, url string, s check.Settings) (fetchResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fetchResult{}, err
	}
	for _, h := range s.CustomHeaders {
		req.Header.Add(h.Name, h.Value)
	}
	req.Header.Set("User-Agent", s.UserAgent)

	resp, err := client.Do(req)
	if err != nil {
		return fetchResult{}, err
	}
	defer resp.Body.Close()

	res := fetchResult{Status: resp.StatusCode, Headers: headerPairs(resp.Header)}
	if !isHTML(resp.Header.Get("Content-Type")) {
		return res, nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxBodyBytes+1))
	if err != nil {
		return fetchResult{}, err
	}
	if len(body) > MaxBodyBytes {
		return fetchResult{
			FailKind: FailBodyTooLarge,
			FailMsg:  "HTML body is larger than 10 MB",
		}, nil
	}
	res.Body, res.HasBody = body, true
	return res, nil
}

func isHTML(contentType string) bool {
	mt, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		mt = strings.ToLower(strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0]))
	}
	return mt == "text/html" || mt == "application/xhtml+xml"
}

func headerPairs(h http.Header) [][2]string {
	names := make([]string, 0, len(h))
	for n := range h {
		names = append(names, n)
	}
	sort.Strings(names)
	var pairs [][2]string
	for _, n := range names {
		for _, v := range h[n] {
			pairs = append(pairs, [2]string{n, v})
		}
	}
	return pairs
}

// classify maps a transport error to a Fetch failure kind.
func classify(err error) string {
	var (
		dnsErr    *net.DNSError
		netErr    net.Error
		certVerr  *tls.CertificateVerificationError
		unknownCA x509.UnknownAuthorityError
		hostErr   x509.HostnameError
		certErr   x509.CertificateInvalidError
		recErr    tls.RecordHeaderError
		alertErr  tls.AlertError
	)
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return FailTimeout
	case errors.As(err, &dnsErr):
		return FailDNS
	case errors.As(err, &certVerr), errors.As(err, &unknownCA), errors.As(err, &hostErr),
		errors.As(err, &certErr), errors.As(err, &recErr), errors.As(err, &alertErr):
		return FailTLS
	case errors.As(err, &netErr) && netErr.Timeout():
		return FailTimeout
	default:
		return FailConnection
	}
}
