package fakesite_test

import (
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ihorbryk/seo-site-checker/internal/fakesite"
)

func get(t *testing.T, c *http.Client, url string) (*http.Response, string, error) {
	t.Helper()
	resp, err := c.Get(url)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return resp, string(b), err
}

func TestServesMutablePathMapOverTLS(t *testing.T) {
	site := fakesite.New(t, "shop.test")
	c := site.Client()
	site.Set("/", fakesite.Response{Body: "<h1>home</h1>", Header: http.Header{"X-Test": {"1"}}})

	resp, body, err := get(t, c, "https://shop.test/")
	if err != nil || resp.StatusCode != 200 || body != "<h1>home</h1>" || resp.Header.Get("X-Test") != "1" {
		t.Fatalf("got %v %q %v", resp, body, err)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("default content type = %q", ct)
	}

	site.Set("/", fakesite.Response{Status: 503, Body: "down"})
	if resp, body, _ := get(t, c, "https://shop.test/"); resp.StatusCode != 503 || body != "down" {
		t.Fatalf("Set should replace: %d %q", resp.StatusCode, body)
	}

	site.Remove("/")
	if resp, _, _ := get(t, c, "https://shop.test/"); resp.StatusCode != 404 {
		t.Fatalf("removed path should 404, got %d", resp.StatusCode)
	}
	if n := site.Hits("/"); n != 3 {
		t.Errorf("Hits = %d, want 3", n)
	}
}

func TestClientFailsDNSForOtherHostsAndDoesNotFollowRedirects(t *testing.T) {
	site := fakesite.New(t, "shop.test")
	c := site.Client()

	_, _, err := get(t, c, "https://elsewhere.test/")
	var dnsErr *net.DNSError
	if !errors.As(err, &dnsErr) {
		t.Fatalf("want a DNS error for another host, got %v", err)
	}

	site.Set("/old", fakesite.Response{Status: 301, Header: http.Header{"Location": {"https://shop.test/new"}}})
	site.Set("/new", fakesite.Response{Body: "new"})
	resp, _, err := get(t, c, "https://shop.test/old")
	if err != nil || resp.StatusCode != 301 {
		t.Fatalf("redirect must be returned, not followed: %v %v", resp, err)
	}
}

func TestFaultHooks(t *testing.T) {
	site := fakesite.New(t, "shop.test")
	c := site.Client()
	site.Set("/slow", fakesite.Response{Body: "ok"})
	site.Set("/cut", fakesite.Response{Body: strings.Repeat("x", 1000)})
	site.Set("/gone", fakesite.Response{Body: "x"})

	site.Fault("/slow", fakesite.Fault{Delay: 300 * time.Millisecond})
	short := *c
	short.Timeout = 50 * time.Millisecond
	if _, _, err := get(t, &short, "https://shop.test/slow"); err == nil {
		t.Error("delayed response should hit the client timeout")
	}

	site.Fault("/cut", fakesite.Fault{CloseMidResponse: true})
	if _, body, err := get(t, c, "https://shop.test/cut"); err == nil || len(body) >= 1000 {
		t.Errorf("connection closed mid-response should fail the body read, got %d bytes, err %v", len(body), err)
	}

	site.Fault("/gone", fakesite.Fault{Unreachable: true})
	if _, _, err := get(t, c, "https://shop.test/gone"); err == nil {
		t.Error("unreachable path should fail the request")
	}

	site.OversizedBody("/big", 2<<20)
	if _, body, err := get(t, c, "https://shop.test/big"); err != nil || len(body) != 2<<20 {
		t.Errorf("oversized body: %d bytes, err %v", len(body), err)
	}
}

func TestNetworkReachesEveryAddedSiteEvenAfterTheClientIsBuilt(t *testing.T) {
	n := fakesite.NewNetwork()
	c := n.Client()
	a := fakesite.New(t, "a.test")
	b := fakesite.New(t, "b.test")
	a.Set("/", fakesite.Response{Body: "A"})
	b.Set("/", fakesite.Response{Body: "B"})
	n.Add(a)
	n.Add(b)

	for url, want := range map[string]string{"https://a.test/": "A", "https://b.test/": "B"} {
		if _, body, err := get(t, c, url); err != nil || body != want {
			t.Errorf("%s: %q %v", url, body, err)
		}
	}
	_, _, err := get(t, c, "https://c.test/")
	var dnsErr *net.DNSError
	if !errors.As(err, &dnsErr) {
		t.Errorf("unknown host: want a DNS error, got %v", err)
	}
}
