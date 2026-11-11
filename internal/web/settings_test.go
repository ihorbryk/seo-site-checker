package web_test

import (
	"github.com/PuerkitoBio/goquery"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func settingsForm() url.Values {
	return url.Values{
		"schedule":     {"hourly"},
		"user_agent":   {"Googlebot/2.1"},
		"timeout_s":    {"20"},
		"max_parallel": {"3"},
	}
}

func projectWithSettings(t *testing.T) http.Handler {
	t.Helper()
	h := newServer(t)
	if w := do(t, h, "POST", "/projects", url.Values{"domain": {"shop.test"}}, true); w.Code != 200 {
		t.Fatalf("add project: %d", w.Code)
	}
	return h
}

func TestSettingsTabShowsDomainReadOnlyAndTheRequestForm(t *testing.T) {
	h := projectWithSettings(t)
	w := do(t, h, "GET", "/p/1/settings", nil, false)
	if w.Code != 200 {
		t.Fatalf("status %d", w.Code)
	}
	d := doc(t, w)
	if got := strings.TrimSpace(d.Find("#domain").Text()); got != "shop.test" {
		t.Errorf("domain = %q", got)
	}
	if d.Find(`form#request-settings input[name="domain"]`).Length() != 0 {
		t.Error("domain must not be editable")
	}
	for _, sel := range []string{
		`select[name="schedule"]`, `input[name="user_agent"]`, `input[name="timeout_s"]`,
		`input[name="max_parallel"]`, `input[name="respect_robots"]`, "datalist option",
	} {
		if d.Find("form#request-settings "+sel).Length() == 0 {
			t.Errorf("form lacks %s", sel)
		}
	}
	if got := d.Find(`input[name="user_agent"]`).AttrOr("value", ""); got != "SEOSiteChecker/0.1" {
		t.Errorf("user agent default = %q", got)
	}
	if !strings.Contains(d.Find("form#request-settings").Text(), "apply from the next Check") {
		t.Error("form does not say changes apply from the next Check")
	}
	if d.Find(".tabs a[href='/p/1']").Length() != 1 || d.Find(".tabs b").Text() != "Settings" {
		t.Error("tabs should link to Checks and mark Settings as current")
	}
}

func TestSettingsTabForUnknownProjectIsNotFound(t *testing.T) {
	if w := do(t, newServer(t), "GET", "/p/99/settings", nil, false); w.Code != 404 {
		t.Errorf("status %d", w.Code)
	}
}

func TestSavingSettingsStoresThemInOrderAndShowsThemAgain(t *testing.T) {
	h := projectWithSettings(t)
	f := settingsForm()
	f["header_name"] = []string{"X-B", "X-A"}
	f["header_value"] = []string{"2", "1"}
	f.Set("respect_robots", "on")

	w := do(t, h, "POST", "/p/1/settings", f, true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "next Check") {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}

	d := doc(t, do(t, h, "GET", "/p/1/settings", nil, false))
	if got := d.Find(`select[name="schedule"] option[selected]`).AttrOr("value", ""); got != "hourly" {
		t.Errorf("schedule = %q", got)
	}
	if got := d.Find(`input[name="timeout_s"]`).AttrOr("value", ""); got != "20" {
		t.Errorf("timeout = %q", got)
	}
	if _, ok := d.Find(`input[name="respect_robots"]`).Attr("checked"); !ok {
		t.Error("respect robots not checked")
	}
	var names, values []string
	d.Find(`#headers input[name="header_name"]`).Each(func(_ int, s *goquery.Selection) { names = append(names, s.AttrOr("value", "")) })
	d.Find(`#headers input[name="header_value"]`).Each(func(_ int, s *goquery.Selection) { values = append(values, s.AttrOr("value", "")) })
	if strings.Join(names, ",") != "X-B,X-A" || strings.Join(values, ",") != "2,1" {
		t.Errorf("headers = %v %v", names, values)
	}
}

func TestInvalidSettingsAreRefusedAndNotSaved(t *testing.T) {
	h := projectWithSettings(t)
	f := settingsForm()
	f.Set("timeout_s", "500")
	f["header_name"], f["header_value"] = []string{"User-Agent"}, []string{"x"}

	w := do(t, h, "POST", "/p/1/settings", f, true)
	if w.Code != 422 {
		t.Fatalf("status %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Timeout") || !strings.Contains(body, "User-Agent") {
		t.Errorf("errors not listed: %s", body)
	}
	d := doc(t, do(t, h, "GET", "/p/1/settings", nil, false))
	if got := d.Find(`input[name="timeout_s"]`).AttrOr("value", ""); got != "30" {
		t.Errorf("invalid save changed the timeout to %q", got)
	}
}

func TestInvalidPlainFormPostRerendersTheTabWithTheErrorsAndTheInput(t *testing.T) {
	h := projectWithSettings(t)
	f := settingsForm()
	f.Set("max_parallel", "11")
	w := do(t, h, "POST", "/p/1/settings", f, false)
	if w.Code != 422 {
		t.Fatalf("status %d", w.Code)
	}
	d := doc(t, w)
	if d.Find("form#request-settings .flash.err").Length() == 0 {
		t.Error("no error shown")
	}
	if got := d.Find(`input[name="user_agent"]`).AttrOr("value", ""); got != "Googlebot/2.1" {
		t.Errorf("input not kept: %q", got)
	}
}

func TestHeaderRowFragmentIsABlankRow(t *testing.T) {
	h := projectWithSettings(t)
	w := do(t, h, "GET", "/p/1/settings/header-row", nil, true)
	d := doc(t, w)
	if w.Code != 200 || d.Find(`input[name="header_name"]`).Length() != 1 || d.Find(`input[name="header_value"]`).Length() != 1 {
		t.Errorf("bad row: %d %s", w.Code, w.Body.String())
	}
}
