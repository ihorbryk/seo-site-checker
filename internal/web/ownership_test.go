package web

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/ihorbryk/seo-site-checker/internal/check"
	"github.com/ihorbryk/seo-site-checker/internal/dbgen"
	"github.com/ihorbryk/seo-site-checker/internal/testdb"
	"github.com/ihorbryk/seo-site-checker/internal/testfixtures"
)

func TestResolverAnswersOwnerOtherOwnerAndMissingForEveryKind(t *testing.T) {
	pool := testdb.New(t)
	s := &server{pool: pool, q: dbgen.New(pool)}
	a, b := testfixtures.User(t, pool), testfixtures.User(t, pool)
	p := testfixtures.Project(t, pool, testfixtures.WithOwner(a))
	pg := testfixtures.Page(t, pool, p, "/")
	c := testfixtures.Check(t, pool, p)
	snap := testfixtures.Snapshot(t, pool, c, pg)
	rows := map[ownerKind]int64{projectKind: p.ID, pageKind: pg.ID, checkKind: c.ID, snapshotKind: snap.ID}

	for kind, id := range rows {
		for _, tc := range []struct {
			name string
			user int64
			id   int64
			want bool
		}{
			{"owner", a.ID, id, true},
			{"other owner", b.ID, id, false},
			{"missing", a.ID, id + 1000, false},
		} {
			got, err := s.owns(context.Background(), kind, tc.id, tc.user)
			if err != nil || got != tc.want {
				t.Errorf("kind %d, %s: owns = %v, err %v; want %v", kind, tc.name, got, err, tc.want)
			}
		}
	}
}

// pathFor fills a route pattern's wildcards: {id} from ids, others as given.
func pathFor(pattern string, id string) (method, path string) {
	method, path, _ = strings.Cut(pattern, " ")
	path = strings.ReplaceAll(path, "{$}", "")
	path = strings.ReplaceAll(path, "{id}", id)
	path = strings.ReplaceAll(path, "{section}", "changed")
	return method, path
}

func TestEveryRouteIsGatedByItsDeclaredAccess(t *testing.T) {
	pool := testdb.New(t)
	rc, err := check.NewInsertClient(pool)
	if err != nil {
		t.Fatal(err)
	}
	h, err := New(pool, rc, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{Argon2: testfixtures.Argon2})
	if err != nil {
		t.Fatal(err)
	}
	a, b := testfixtures.User(t, pool), testfixtures.User(t, pool)
	p := testfixtures.Project(t, pool, testfixtures.WithOwner(a))
	pg := testfixtures.Page(t, pool, p, "/")
	c := testfixtures.Check(t, pool, p)
	snap := testfixtures.Snapshot(t, pool, c, pg)
	aIDs := map[ownerKind]int64{projectKind: p.ID, pageKind: pg.ID, checkKind: c.ID, snapshotKind: snap.ID}
	const missing = "999999999"

	send := func(method, path, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(url.Values{}.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if token != "" {
			r.AddCookie(&http.Cookie{Name: CookieName(false), Value: token})
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	tokenB := testfixtures.Session(t, pool, b)
	owned := 0
	for _, rt := range (&server{}).routes() {
		if rt.access.level == publicLevel {
			continue
		}
		method, path := pathFor(rt.pattern, "1")
		if w := send(method, path, ""); w.Code != http.StatusSeeOther || !strings.HasPrefix(w.Header().Get("Location"), "/login?next=") {
			t.Errorf("%s signed out: status %d, Location %q; want redirect to /login?next=", rt.pattern, w.Code, w.Header().Get("Location"))
		}
		if !rt.access.isOwned() {
			continue
		}
		owned++
		_, path = pathFor(rt.pattern, strconv.FormatInt(aIDs[rt.access.kind], 10))
		if w := send(method, path, tokenB); w.Code != http.StatusNotFound {
			t.Errorf("%s as another User: status %d, want 404", rt.pattern, w.Code)
		}
		_, path = pathFor(rt.pattern, missing)
		if w := send(method, path, tokenB); w.Code != http.StatusNotFound {
			t.Errorf("%s with a missing id: status %d, want 404", rt.pattern, w.Code)
		}
	}
	if owned == 0 {
		t.Fatal("no owned routes in the table")
	}
}
