package systemtest_test

import (
	"net/http"
	"testing"

	"github.com/ihorbryk/seo-site-checker/internal/systemtest"
	"github.com/ihorbryk/seo-site-checker/internal/testfixtures"
)

func TestAnotherUsersResourcesAnswer404WhileTheOwnerUsesThemNormally(t *testing.T) {
	s := systemtest.New(t)
	other := s.NewUser()

	p := testfixtures.Project(t, s.Pool, testfixtures.WithOwner(s.User))
	pg := testfixtures.Page(t, s.Pool, p, "/")
	c := testfixtures.Check(t, s.Pool, p)
	snap := testfixtures.Snapshot(t, s.Pool, c, pg)

	screens := map[string]string{
		"project":        "/p/" + itoa(p.ID),
		"settings":       "/p/" + itoa(p.ID) + "/settings",
		"pages":          "/p/" + itoa(p.ID) + "/pages",
		"page":           "/pg/" + itoa(pg.ID),
		"check":          s.CheckPath(c.ID),
		"old check link": "/c/" + itoa(c.ID),
		"snapshot":       "/s/" + itoa(snap.ID),
		"diff":           "/d/" + itoa(snap.ID),
	}
	for name, path := range screens {
		if w := other.Get(path); w.Code != http.StatusNotFound {
			t.Errorf("%s as another User: status %d, want 404", name, w.Code)
		}
		if w := other.GetHTMX(path); w.Code != http.StatusNotFound {
			t.Errorf("%s fragment as another User: status %d, want 404", name, w.Code)
		}
		if w := s.Get(path); w.Code == http.StatusNotFound {
			t.Errorf("%s as its owner: status 404", name)
		}
	}

	// A mutating POST is refused too, and changes nothing.
	if w := other.Post("/p/"+itoa(p.ID)+"/checks", nil); w.Code != http.StatusNotFound {
		t.Errorf("Run Check as another User: status %d, want 404", w.Code)
	}
	var checks int
	if err := s.Pool.QueryRow(t.Context(), `SELECT count(*) FROM checks WHERE project_id = $1`, p.ID).Scan(&checks); err != nil || checks != 1 {
		t.Errorf("checks = %d (err %v), want the one fixture Check", checks, err)
	}
}
