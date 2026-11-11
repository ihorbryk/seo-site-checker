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
type pager struct {
	Page, Per, Last    int
	Multi              bool   // more than one page: Prev/Next are shown
	Rows               string // "Rows 101–150 of 1,234"
	PrevHref, NextHref string // empty on the first / last page
	Sizes              []pageSize

	path  string
	query url.Values
}

type pageSize struct {
	Per     int
	Href    string
	Current bool
}

// paginate parses u's page and per leniently (a bad per is the default, a bad
// or small page is 1, a page past the end is the last) for a list of total rows.
func paginate(u *url.URL, total int) pager {
	q := u.Query()
	p := pager{Per: perPageChoices[0], path: u.Path, query: q}
	if n, err := strconv.Atoi(q.Get("per")); err == nil {
		for _, c := range perPageChoices {
			if n == c {
				p.Per = n
			}
		}
	}
	p.Last = max(1, (total+p.Per-1)/p.Per)
	p.Page = 1
	if n, err := strconv.Atoi(q.Get("page")); err == nil && n > 1 {
		p.Page = min(n, p.Last)
	}
	p.Multi = p.Last > 1
	if total == 0 {
		p.Rows = "No rows"
	} else {
		p.Rows = fmt.Sprintf("Rows %s–%s of %s", thousands(p.Offset()+1), thousands(p.End(total)), thousands(total))
	}
	if p.Page > 1 {
		p.PrevHref = p.With("page", strconv.Itoa(p.Page-1))
	}
	if p.Page < p.Last {
		p.NextHref = p.With("page", strconv.Itoa(p.Page+1))
	}
	for _, c := range perPageChoices {
		p.Sizes = append(p.Sizes, pageSize{Per: c, Href: p.With("per", strconv.Itoa(c)), Current: c == p.Per})
	}
	return p
}

// Offset is the number of rows before the current page.
func (p pager) Offset() int { return (p.Page - 1) * p.Per }

// End is the number of rows up to and including the current page of a list
// of total rows: the current page is rows Offset()+1 to End(total).
func (p pager) End(total int) int { return min(p.Offset()+p.Per, total) }

// With links to the list with the parameter key set to value (removed when
// value is empty), keeping every other parameter and per; unless key is page,
// it lands on page 1.
func (p pager) With(key, value string) string {
	q := url.Values{}
	for k, v := range p.query {
		q[k] = append([]string(nil), v...)
	}
	q.Del("page")
	q.Set("per", strconv.Itoa(p.Per))
	if value == "" {
		q.Del(key)
	} else {
		q.Set(key, value)
	}
	return p.path + "?" + q.Encode()
}

// thousands formats n with comma separators: 1234 is "1,234".
func thousands(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
