package web_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestCrossOriginProtection(t *testing.T) {
	h := newTestApp(t).Anonymous
	post := func(hdr map[string]string) int {
		r := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(url.Values{}.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		for k, v := range hdr {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	for name, tc := range map[string]struct {
		hdr     map[string]string
		refused bool
	}{
		"no fetch metadata headers (httptest)": {nil, false},
		"same-origin":                          {map[string]string{"Sec-Fetch-Site": "same-origin"}, false},
		"matching Origin":                      {map[string]string{"Origin": "http://example.com"}, false},
		"cross-site":                           {map[string]string{"Sec-Fetch-Site": "cross-site"}, true},
		"foreign Origin":                       {map[string]string{"Origin": "https://evil.example"}, true},
	} {
		t.Run(name, func(t *testing.T) {
			got := post(tc.hdr)
			if tc.refused != (got == http.StatusForbidden) {
				t.Fatalf("status %d, refused want %v", got, tc.refused)
			}
		})
	}
}
