package web

import (
	"fmt"
	"net/url"
	"strconv"
)

// perPageChoices are the rows-per-page values a paginated screen offers; the
// first is the default and the fallback for any other value.
var perPageChoices = []int{50, 100, 200, 500}

// pager is one page of a paginated list, read from the page and per query
// parameters, plus the links of the shared "pager" template. Its links keep
// every other query parameter of the request.
//
// A Report section's pager (paginateSection) reads <id>_page and <id>_per
// instead, its links end in the section's #<id> anchor, and each link also
// carries Hx, the URL of the section alone, which htmx swaps into Target.
type pager struct {
	Page, Per, Last    int
	Multi              bool   // more than one page: Prev/Next are shown
	Rows               string // "Rows 101–150 of 1,234"
	PrevHref, NextHref string // empty on the first / last page
	PrevHx, NextHx     string // section pagers only
	Sizes              []pageSize
	Target             string // section pagers only: the id of the container htmx swaps

	total           int
	pageKey, perKey string
	path, hxPath    string
	fragment        string
	query           url.Values
}

type pageSize struct {
	Per      int
	Href, Hx string
	Current  bool
}

// paginate parses u's page and per leniently (a bad per is the default, a bad
// or small page is 1, a page past the end is the last) for a list of total rows.
func paginate(u *url.URL, total int) pager {
	return newPager(u, total, pager{pageKey: "page", perKey: "per", path: u.Path})
}

// paginateSection is paginate for the Report section with HTML id id, on the
// Report at u.Path: it reads <id>_page and <id>_per and leaves every other
// parameter, including other sections', to the links as it found them.
func paginateSection(u *url.URL, id string, total int) pager {
	return newPager(u, total, pager{
		pageKey: id + "_page", perKey: id + "_per", path: u.Path,
		hxPath: u.Path + "/sections/" + id, fragment: id, Target: id + "-section",
	})
}

func newPager(u *url.URL, total int, p pager) pager {
	q := u.Query()
	p.query, p.total, p.Per = q, total, perPageChoices[0]
	if n, err := strconv.Atoi(q.Get(p.perKey)); err == nil {
		for _, c := range perPageChoices {
			if n == c {
				p.Per = n
			}
		}
	}
	p.Last = max(1, (total+p.Per-1)/p.Per)
	p.Page = 1
	if n, err := strconv.Atoi(q.Get(p.pageKey)); err == nil && n > 1 {
		p.Page = min(n, p.Last)
	}
	p.Multi = p.Last > 1
	if total == 0 {
		p.Rows = "No rows"
	} else {
		p.Rows = fmt.Sprintf("Rows %s–%s of %s", thousands(p.Offset()+1), thousands(p.End(total)), thousands(total))
	}
	if p.Page > 1 {
		p.PrevHref, p.PrevHx = p.links(p.pageKey, strconv.Itoa(p.Page-1))
	}
	if p.Page < p.Last {
		p.NextHref, p.NextHx = p.links(p.pageKey, strconv.Itoa(p.Page+1))
	}
	for _, c := range perPageChoices {
		href, hx := p.links(p.perKey, strconv.Itoa(c))
		p.Sizes = append(p.Sizes, pageSize{Per: c, Href: href, Hx: hx, Current: c == p.Per})
	}
	return p
}

// Offset is the number of rows before the current page.
func (p pager) Offset() int { return (p.Page - 1) * p.Per }

// End is the number of rows up to and including the current page of a list
// of total rows: the current page is rows Offset()+1 to End(total).
func (p pager) End(total int) int { return min(p.Offset()+p.Per, total) }

// Shown is whether a Report section shows its pager: only when it has more
// rows than the smallest page, so a large per can always be switched back.
func (p pager) Shown() bool { return p.total > perPageChoices[0] }

// With links to the list with the parameter key set to value (removed when
// value is empty), keeping every other parameter and per; unless key is the
// page parameter, it lands on page 1.
func (p pager) With(key, value string) string {
	href, _ := p.links(key, value)
	return href
}

// links returns With's link and, for a section pager, the same query on the
// section's own URL.
func (p pager) links(key, value string) (href, hx string) {
	q := url.Values{}
	for k, v := range p.query {
		q[k] = append([]string(nil), v...)
	}
	q.Del(p.pageKey)
	q.Set(p.perKey, strconv.Itoa(p.Per))
	if value == "" {
		q.Del(key)
	} else {
		q.Set(key, value)
	}
	query := "?" + q.Encode()
	href = p.path + query
	if p.fragment != "" {
		href += "#" + p.fragment
	}
	if p.hxPath != "" {
		hx = p.hxPath + query
	}
	return href, hx
}

// thousands formats n with comma separators: 1234 is "1,234".
func thousands(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
