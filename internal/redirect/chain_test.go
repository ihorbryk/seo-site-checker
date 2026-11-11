package redirect_test

import (
	"reflect"
	"testing"

	"github.com/ihorbryk/seo-site-checker/internal/redirect"
)

func TestBuildChain(t *testing.T) {
	const host = "shop.test"
	u := func(p string) string { return "https://shop.test" + p }
	snaps := map[string]redirect.Snap{
		u("/a"):    {Status: 301, Target: u("/b")},
		u("/b"):    {Status: 302, Target: u("/c")},
		u("/c"):    {Status: 200},
		u("/x"):    {Status: 301, Target: u("/y")},
		u("/y"):    {Status: 301, Target: u("/x")},
		u("/self"): {Status: 301, Target: u("/self")},
		u("/off"):  {Status: 301, Target: "https://other.test/"},
		u("/http"): {Status: 301, Target: "http://shop.test/p"},
		u("/gone"): {Status: 301, Target: u("/nosnap")},
		u("/300"):  {Status: 300},
		u("/404"):  {Status: 404},
	}
	long := map[string]redirect.Snap{}
	name := func(i int) string { return u("/h" + string(rune('a'+i))) }
	for i := 0; i < 12; i++ {
		long[name(i)] = redirect.Snap{Status: 301, Target: name(i + 1)}
	}
	long[name(12)] = redirect.Snap{Status: 200}
	exact10 := map[string]redirect.Snap{}
	for i := 0; i < 10; i++ {
		exact10[name(i)] = redirect.Snap{Status: 301, Target: name(i + 1)}
	}
	exact10[name(10)] = redirect.Snap{Status: 200}

	lookup := func(m map[string]redirect.Snap) func(string) (redirect.Snap, bool) {
		return func(url string) (redirect.Snap, bool) { s, ok := m[url]; return s, ok }
	}
	tests := []struct {
		name  string
		snaps map[string]redirect.Snap
		start string
		urls  []string
		stop  redirect.Stop
	}{
		{"on-domain chain ends at non-3xx", snaps, u("/a"), []string{u("/a"), u("/b"), u("/c")}, redirect.StopFinal},
		{"loop is marked", snaps, u("/x"), []string{u("/x"), u("/y"), u("/x")}, redirect.StopLoop},
		{"self redirect is a loop", snaps, u("/self"), []string{u("/self"), u("/self")}, redirect.StopLoop},
		{"off-domain target ends the chain", snaps, u("/off"), []string{u("/off"), "https://other.test/"}, redirect.StopOffDomain},
		{"http target ends the chain", snaps, u("/http"), []string{u("/http"), "http://shop.test/p"}, redirect.StopInsecure},
		{"missing snapshot", snaps, u("/gone"), []string{u("/gone"), u("/nosnap")}, redirect.StopMissing},
		{"start without snapshot", snaps, u("/none"), []string{u("/none")}, redirect.StopMissing},
		{"3xx without target is final", snaps, u("/300"), []string{u("/300")}, redirect.StopFinal},
		{"non-redirect start", snaps, u("/404"), []string{u("/404")}, redirect.StopFinal},
		{"10 hops reach the end", exact10, name(0), func() []string {
			var out []string
			for i := 0; i <= 10; i++ {
				out = append(out, name(i))
			}
			return out
		}(), redirect.StopFinal},
		{"stops after 10 hops", long, name(0), func() []string {
			var out []string
			for i := 0; i <= 10; i++ {
				out = append(out, name(i))
			}
			return out
		}(), redirect.StopMaxHops},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := redirect.BuildChain(tt.start, host, lookup(tt.snaps))
			if !reflect.DeepEqual(got.URLs, tt.urls) || got.Stop != tt.stop {
				t.Errorf("chain = %v (%v), want %v (%v)", got.URLs, got.Stop, tt.urls, tt.stop)
			}
		})
	}
}
