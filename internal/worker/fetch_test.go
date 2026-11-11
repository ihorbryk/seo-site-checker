package worker

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/url"
	"testing"
)

func TestClassifyMapsTransportErrorsToFailureKinds(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"deadline", context.DeadlineExceeded, FailTimeout},
		{"wrapped deadline", &url.Error{Op: "Get", Err: context.DeadlineExceeded}, FailTimeout},
		{"dns", &url.Error{Op: "Get", Err: &net.DNSError{Err: "no such host", IsNotFound: true}}, FailDNS},
		{"unknown authority", &url.Error{Op: "Get", Err: &tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}}}, FailTLS},
		{"hostname", x509.HostnameError{Host: "a"}, FailTLS},
		{"not tls", tls.RecordHeaderError{}, FailTLS},
		{"refused", &net.OpError{Op: "dial", Err: errors.New("connection refused")}, FailConnection},
		{"eof", fmt.Errorf("Get: %w", errors.New("EOF")), FailConnection},
	}
	for _, c := range cases {
		if got := classify(c.err); got != c.want {
			t.Errorf("%s: classify = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestIsHTMLAcceptsHtmlAndXhtmlOnly(t *testing.T) {
	for ct, want := range map[string]bool{
		"text/html":                true,
		"text/html; charset=utf-8": true,
		"TEXT/HTML":                true,
		"application/xhtml+xml":    true,
		"application/pdf":          false,
		"application/json":         false,
		"":                         false,
		"text/plain":               false,
	} {
		if got := isHTML(ct); got != want {
			t.Errorf("isHTML(%q) = %v", ct, got)
		}
	}
}
