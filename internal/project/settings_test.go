package project_test

import (
	"net/url"
	"reflect"
	"testing"

	"github.com/ihorbryk/seo-site-checker/internal/check"
	"github.com/ihorbryk/seo-site-checker/internal/project"
)

func validForm() url.Values {
	return url.Values{
		"schedule":     {"weekly"},
		"user_agent":   {"Googlebot/2.1"},
		"timeout_s":    {"45"},
		"max_parallel": {"4"},
	}
}

func TestParseRequestSettingsAcceptsAValidForm(t *testing.T) {
	f := validForm()
	f["header_name"] = []string{"X-Env", "", "Authorization", "X-Env"}
	f["header_value"] = []string{"staging", "", "Bearer abc", "again"}
	f.Set("respect_robots", "on")

	got, errs := project.ParseRequestSettings(f)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	want := project.RequestSettings{
		Schedule: "weekly", UserAgent: "Googlebot/2.1", TimeoutS: 45, MaxParallel: 4, RespectRobots: true,
		Headers: []check.Header{{Name: "X-Env", Value: "staging"}, {Name: "Authorization", Value: "Bearer abc"}, {Name: "X-Env", Value: "again"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestParseRequestSettingsRefusesInvalidValues(t *testing.T) {
	cases := []struct {
		name  string
		edit  func(url.Values)
		field string
	}{
		{"schedule outside the enum", func(f url.Values) { f.Set("schedule", "monthly") }, "schedule"},
		{"empty user agent", func(f url.Values) { f.Set("user_agent", "  ") }, "user_agent"},
		{"timeout 0", func(f url.Values) { f.Set("timeout_s", "0") }, "timeout_s"},
		{"timeout 121", func(f url.Values) { f.Set("timeout_s", "121") }, "timeout_s"},
		{"timeout not a number", func(f url.Values) { f.Set("timeout_s", "soon") }, "timeout_s"},
		{"max parallel 0", func(f url.Values) { f.Set("max_parallel", "0") }, "max_parallel"},
		{"max parallel 11", func(f url.Values) { f.Set("max_parallel", "11") }, "max_parallel"},
		{"User-Agent as a custom header", func(f url.Values) {
			f["header_name"], f["header_value"] = []string{"user-agent"}, []string{"x"}
		}, "headers"},
		{"header name with a space", func(f url.Values) {
			f["header_name"], f["header_value"] = []string{"X Bad"}, []string{"x"}
		}, "headers"},
		{"header value with a newline", func(f url.Values) {
			f["header_name"], f["header_value"] = []string{"X-Ok"}, []string{"a\r\nInjected: 1"}
		}, "headers"},
		{"header value without a name", func(f url.Values) {
			f["header_name"], f["header_value"] = []string{""}, []string{"orphan"}
		}, "headers"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := validForm()
			tc.edit(f)
			_, errs := project.ParseRequestSettings(f)
			if len(errs[tc.field]) == 0 {
				t.Errorf("want an error for %q, got %v", tc.field, errs)
			}
		})
	}
}

func TestParseRequestSettingsAcceptsBoundaries(t *testing.T) {
	for _, tc := range [][2]string{{"1", "1"}, {"120", "10"}} {
		f := validForm()
		f.Set("timeout_s", tc[0])
		f.Set("max_parallel", tc[1])
		if _, errs := project.ParseRequestSettings(f); len(errs) != 0 {
			t.Errorf("%v: %v", tc, errs)
		}
	}
}
