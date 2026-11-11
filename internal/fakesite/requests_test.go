package fakesite_test

import (
	"net/http"
	"testing"

	"github.com/ihorbryk/seo-site-checker/internal/fakesite"
)

func TestRecordsTheHeadersOfEachRequestInOrderReceived(t *testing.T) {
	site := fakesite.New(t, "shop.test")
	site.Set("/", fakesite.Response{Body: "ok"})
	c := site.Client()
	for _, ua := range []string{"first", "second"} {
		req, _ := http.NewRequest("GET", "https://shop.test/", nil)
		req.Header.Set("User-Agent", ua)
		req.Header.Add("X-Env", "staging")
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}
	got := site.Requests("/")
	if len(got) != 2 || got[0].Get("User-Agent") != "first" || got[1].Get("User-Agent") != "second" || got[1].Get("X-Env") != "staging" {
		t.Errorf("requests = %v", got)
	}
	if len(site.Requests("/other")) != 0 {
		t.Error("requests recorded for an unrequested path")
	}
}
