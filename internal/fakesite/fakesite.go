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
	"slices"
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
	reqs   map[string][]http.Header
}

// New starts a fake site answering for host (e.g. "shop.test"); it is closed
// when the test ends.
func New(t testing.TB, host string) *Site {
	s := &Site{
		host:   host,
		pages:  map[string]Response{},
		faults: map[string]Fault{},
		hits:   map[string]int{},
		reqs:   map[string][]http.Header{},
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

// Requests returns the headers of each request received for path, oldest
// first (query string excluded).
func (s *Site) Requests(path string) []http.Header {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.reqs[path])
}

// Host is the fake host the site answers for.
func (s *Site) Host() string { return s.host }

// Client returns an *http.Client for the system under test that reaches only
// this site; see Network for several.
func (s *Site) Client() *http.Client {
	n := NewNetwork()
	n.Add(s)
	return n.Client()
}

// Network is the fake internet the system under test sees: the hosts of the
// sites added to it, and nothing else.
type Network struct {
	mu    sync.Mutex
	sites map[string]*Site
}

// NewNetwork returns an empty Network.
func NewNetwork() *Network { return &Network{sites: map[string]*Site{}} }

// Add makes site reachable at its host. Sites may be added while clients are in use.
func (n *Network) Add(site *Site) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.sites[site.host] = site
}

// Client returns an *http.Client that dials the added sites over TLS
// (trusting each server's certificate), fails DNS for every other host, and
// never follows redirects.
func (n *Network) Client() *http.Client {
	dialer := &net.Dialer{}
	return &http.Client{
		Transport: &http.Transport{
			DialTLSContext: func(ctx context.Context, network, address string) (net.Conn, error) {
				host, _, err := net.SplitHostPort(address)
				n.mu.Lock()
				site := n.sites[host]
				n.mu.Unlock()
				if err != nil || site == nil {
					return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
				}
				raw, err := dialer.DialContext(ctx, network, site.server.Listener.Addr().String())
				if err != nil {
					return nil, err
				}
				roots := x509.NewCertPool()
				roots.AddCert(site.server.Certificate())
				// httptest's certificate is issued for example.com.
				conn := tls.Client(raw, &tls.Config{RootCAs: roots, ServerName: "example.com"})
				if err := conn.HandshakeContext(ctx); err != nil {
					_ = raw.Close()
					return nil, err
				}
				return conn, nil
			},
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func (s *Site) serve(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	s.mu.Lock()
	s.hits[path]++
	s.reqs[path] = append(s.reqs[path], r.Header.Clone())
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
