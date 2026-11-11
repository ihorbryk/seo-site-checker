package page

import "testing"

func TestTheSeedPageIsNeverExcluded(t *testing.T) {
	patterns := []string{"/**"}
	if Excluded(patterns, OriginSeed, "https://shop.test/") {
		t.Error("seed Page excluded")
	}
	for _, origin := range []string{OriginDiscovered, OriginManual} {
		if !Excluded(patterns, origin, "https://shop.test/a") {
			t.Errorf("%s Page not excluded by /**", origin)
		}
	}
}
