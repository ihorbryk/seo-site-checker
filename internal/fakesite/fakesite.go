// Package fakesite is a TLS test server standing in for a monitored site.
// Fixture sites are built in Go: Set and Remove edit a path map between
// Checks, and Fault injects the failures real sites have.
package fakesite

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Response is what a path serves. Status defaults to 200 and Content-Type to
// text/html; charset=utf-8.
type Response struct {
	Status int
	Header http.Header
	Body   string
}

// Fault changes how a path misbehaves.
type Fault struct {
	// Delay waits before answering.
	Delay time.Duration
	// CloseMidResponse sends the headers and half the body, then drops the connection.
	CloseMidResponse bool
	// Unreachable drops the connection without answering.
	Unreachable bool
}

// Site is a fake host served by an httptest TLS server.
type Site struct {
	host   string
	server *httptest.Server

	mu     sync.Mutex
	pages  map[string]Response
	faults map[string]Fault
	hits   map[string]int
}

// New starts a fake site answering for host (e.g. "shop.test"); it is closed
// when the test ends.
func New(t testing.TB, host string) *Site {
	s := &Site{
		host:   host,
		pages:  map[string]Response{},
		faults: map[string]Fault{},
		hits:   map[string]int{},
	}
	s.server = httptest.NewTLSServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.server.Close)
	return s
}

// Set serves resp at path, replacing what was there.
func (s *Site) Set(path string, resp Response) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pages[path] = resp
}

// Remove makes path answer 404 again.
func (s *Site) Remove(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.pages, path)
}

// Fault applies f to path until replaced; the zero Fault clears it.
func (s *Site) Fault(path string, f Fault) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.faults[path] = f
}

// OversizedBody serves n bytes of HTML at path.
func (s *Site) OversizedBody(path string, n int) {
	s.Set(path, Response{Body: strings.Repeat("a", n)})
}

// Hits counts requests received for path (query string excluded).
func (s *Site) Hits(path string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hits[path]
}

// Client returns an *http.Client for the system under test: it reaches the
// fake host (trusting the server's certificate), fails DNS for every other
// host, and never follows redirects.
func (s *Site) Client() *http.Client {
	pool := x509.NewCertPool()
	pool.AddCert(s.server.Certificate())
	addr := s.server.Listener.Addr().String()
	dialer := &net.Dialer{}
	return &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs: pool,
				// httptest's certificate is issued for example.com.
				ServerName: "example.com",
			},
			DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
				host, _, err := net.SplitHostPort(address)
				if err != nil || host != s.host {
					return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
				}
				return dialer.DialContext(ctx, network, addr)
			},
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func (s *Site) serve(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	s.mu.Lock()
	s.hits[path]++
	resp, found := s.pages[path]
	fault := s.faults[path]
	s.mu.Unlock()

	if fault.Delay > 0 {
		select {
		case <-time.After(fault.Delay):
		case <-r.Context().Done():
			return
		}
	}
	if fault.Unreachable {
		dropConnection(w)
		return
	}
	if !found {
		http.NotFound(w, r)
		return
	}
	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	}
	status := resp.Status
	if status == 0 {
		status = http.StatusOK
	}
	if fault.CloseMidResponse {
		w.Header().Set("Content-Length", strconv.Itoa(len(resp.Body)))
		w.WriteHeader(status)
		_, _ = w.Write([]byte(resp.Body[:len(resp.Body)/2]))
		w.(http.Flusher).Flush()
		dropConnection(w)
		return
	}
	w.WriteHeader(status)
	_, _ = w.Write([]byte(resp.Body))
}

func dropConnection(w http.ResponseWriter) {
	if conn, _, err := w.(http.Hijacker).Hijack(); err == nil {
		_ = conn.Close()
	}
}
