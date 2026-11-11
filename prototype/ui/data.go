// PROTOTYPE — throwaway. In-memory fake data for the UI screens prototype.
// Nothing here is the real data model; it only feeds the mockups.
package main

import (
	"crypto/sha256"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

var now = time.Now()

type KV struct{ K, V string }

type SEO struct {
	Title, Desc, Canonical, Robots, H1, OGTitle, Lang string
}

type Project struct {
	ID           int
	Domain       string
	Schedule     string
	UA           string
	Headers      []KV
	Timeout      int
	MaxPar       int
	Excludes     []string
	Robots       bool
	NoiseHeaders []string
	NoiseMasks   []string
	Pages        []*Page
	Checks       []*Check // ascending
}

type Page struct {
	ID        int
	Project   *Project
	Path      string
	State     string // active, orphaned, retired, ignored, excluded
	Orphans   int
	FirstSeen *Check
	Snapshots []*Snapshot
	Failures  []*Failure
}

type Check struct {
	ID         int
	Project    *Project
	Trigger    string
	Started    time.Time
	Finished   time.Time
	Status     string // running, finished, cancelled
	Total      int
	SeedFailed bool
	Dropped    int
	Snapshots  []*Snapshot
	Vanished   []*Page
	Retired    []*Page
	Revived    []*Page
	Failures   []*Failure
}

type Snapshot struct {
	ID       int
	Page     *Page
	Check    *Check
	Prev     *Snapshot
	Fetched  time.Time
	Status   int
	Headers  []KV
	Body     string
	TimingMs int
	Redirect string
	SEO      SEO
	Extra    []string
	NoBody   bool
}

type Failure struct {
	Page     *Page
	Check    *Check
	Kind     string
	Detail   string
	AlsoPrev bool
}

// ---------- accessors used by templates ----------

func (p *Page) URL() string { return "https://" + p.Project.Domain + p.Path }
func (p *Page) Last() *Snapshot {
	if len(p.Snapshots) == 0 {
		return nil
	}
	return p.Snapshots[len(p.Snapshots)-1]
}
func (p *Page) LastChanged() *Snapshot {
	for i := len(p.Snapshots) - 1; i >= 0; i-- {
		if c := Classify(p.Snapshots[i]); c != nil {
			return p.Snapshots[i]
		}
	}
	return nil
}

func (p *Project) Latest() *Check {
	if len(p.Checks) == 0 {
		return nil
	}
	return p.Checks[len(p.Checks)-1]
}
func (p *Project) LatestFinished() *Check {
	for i := len(p.Checks) - 1; i >= 0; i-- {
		if p.Checks[i].Status != "running" {
			return p.Checks[i]
		}
	}
	return nil
}
func (p *Project) Running() *Check {
	if l := p.Latest(); l != nil && l.Status == "running" {
		return l
	}
	return nil
}
func (p *Project) ChecksDesc() []*Check {
	out := make([]*Check, len(p.Checks))
	for i, c := range p.Checks {
		out[len(p.Checks)-1-i] = c
	}
	return out
}
func (p *Project) CountState(s string) int {
	n := 0
	for _, pg := range p.Pages {
		if pg.State == s {
			n++
		}
	}
	return n
}
func (p *Project) PagesIn(state string) []*Page {
	var out []*Page
	for _, pg := range p.Pages {
		if state == "" || pg.State == state {
			out = append(out, pg)
		}
	}
	return out
}

func (s *Snapshot) Header(name string) string {
	var vs []string
	for _, h := range s.Headers {
		if strings.EqualFold(h.K, name) {
			vs = append(vs, h.V)
		}
	}
	return strings.Join(vs, ", ")
}
func (s *Snapshot) Size() string {
	if s.Body == "" {
		return "no body"
	}
	return fmt.Sprintf("%.1f KB", float64(len(s.Body))/1024)
}

// SEOFields returns the ordered SEO fields of a Snapshot.
func (s *Snapshot) SEOFields() []KV {
	h := s.SEO
	if s.NoBody {
		h = SEO{}
	}
	red := s.Redirect
	return []KV{
		{"status", fmt.Sprint(s.Status)},
		{"content-type", s.Header("content-type")},
		{"x-robots-tag", s.Header("x-robots-tag")},
		{"redirect target", red},
		{"title", h.Title},
		{"meta description", h.Desc},
		{"canonical", h.Canonical},
		{"meta robots", h.Robots},
		{"h1", h.H1},
		{"html lang", h.Lang},
		{"og:title", h.OGTitle},
	}
}

// ---------- Diff summary + Report ----------

type FieldChange struct {
	Name, Before, After string
	Changed             bool
}

type Changed struct {
	Snap     *Snapshot
	Tier     int
	TierName string
	Badges   []string
	Fields   []FieldChange // changed only
}

var critical = map[string]bool{"x-robots-tag": true, "meta robots": true, "canonical": true, "redirect target": true}

func fieldChanges(prev, cur *Snapshot) []FieldChange {
	a, b := prev.SEOFields(), cur.SEOFields()
	out := make([]FieldChange, len(a))
	for i := range a {
		out[i] = FieldChange{a[i].K, a[i].V, b[i].V, a[i].V != b[i].V}
	}
	return out
}

// Classify returns the Diff summary for a Snapshot, or nil for empty Diff / first Snapshot.
func Classify(s *Snapshot) *Changed {
	if s.Prev == nil {
		return nil
	}
	c := &Changed{Snap: s, Tier: 9}
	statusChanged := s.Prev.Status != s.Status
	critHit := s.Prev.Status/100 != s.Status/100
	seoHit := false
	for _, f := range fieldChanges(s.Prev, s) {
		if !f.Changed {
			continue
		}
		c.Fields = append(c.Fields, f)
		if f.Name == "status" {
			continue
		}
		seoHit = true
		if critical[f.Name] {
			critHit = true
		}
	}
	hdr := false
	for _, h := range HeaderDiff(s.Prev, s) {
		if !h.Noise && h.Kind != "same" {
			hdr = true
		}
	}
	body := MaskBody(s.Prev.Body, s.Page.Project) != MaskBody(s.Body, s.Page.Project)
	if statusChanged {
		c.Badges = append(c.Badges, "status")
	}
	if seoHit {
		c.Badges = append(c.Badges, "SEO")
	}
	if hdr {
		c.Badges = append(c.Badges, "headers")
	}
	if body {
		c.Badges = append(c.Badges, "body")
	}
	if len(c.Badges) == 0 {
		return nil
	}
	switch {
	case critHit:
		c.Tier, c.TierName = 0, "critical"
	case seoHit || statusChanged:
		c.Tier, c.TierName = 1, "SEO"
	case hdr:
		c.Tier, c.TierName = 2, "headers only"
	default:
		c.Tier, c.TierName = 3, "body only"
	}
	return c
}

type Report struct {
	Check     *Check
	Changed   []*Changed
	New       []*Snapshot
	Revived   []*Page
	Unchanged int
	Fetched   int
	Critical  int
	SEO       int
}

func (c *Check) Report() *Report {
	r := &Report{Check: c, Revived: c.Revived}
	for _, s := range c.Snapshots {
		r.Fetched++
		if s.Prev == nil {
			r.New = append(r.New, s)
			continue
		}
		if ch := Classify(s); ch != nil {
			r.Changed = append(r.Changed, ch)
			if ch.Tier == 0 {
				r.Critical++
			} else if ch.Tier == 1 {
				r.SEO++
			}
		} else {
			r.Unchanged++
		}
	}
	sort.SliceStable(r.Changed, func(i, j int) bool {
		if r.Changed[i].Tier != r.Changed[j].Tier {
			return r.Changed[i].Tier < r.Changed[j].Tier
		}
		return r.Changed[i].Snap.Page.Path < r.Changed[j].Snap.Page.Path
	})
	return r
}

func (c *Check) Duration() string {
	if c.Status == "running" {
		return "—"
	}
	d := c.Finished.Sub(c.Started).Round(time.Second)
	return d.String()
}

type Progress struct {
	Check                    *Check
	Done, Total, Failed, New int
	Pct                      int
	Lanes                    []string
	Finalizing               bool
}

func (c *Check) Progress() Progress {
	el := int(time.Since(now).Seconds())
	cycle := c.Total + 8
	d := (el + 11) % cycle
	p := Progress{Check: c, Total: c.Total}
	if d >= c.Total {
		p.Done, p.Finalizing = c.Total, true
	} else {
		p.Done = d
		for i := 0; i < c.Project.MaxPar; i++ {
			p.Lanes = append(p.Lanes, fmt.Sprintf("/products/item-%d", d*c.Project.MaxPar+i))
		}
	}
	p.Failed = p.Done / 17
	p.New = p.Done / 9
	p.Pct = p.Done * 100 / c.Total
	return p
}

// ---------- header diff, masking, line diff ----------

var builtinNoiseHeaders = []string{"date", "age", "expires", "set-cookie", "etag", "last-modified", "cf-ray", "x-request-id", "server-timing", "report-to", "nel"}

type HeaderRow struct {
	Name, Before, After, Kind string
	Noise                     bool
}

func isNoiseHeader(p *Project, n string) bool {
	for _, x := range builtinNoiseHeaders {
		if x == n {
			return true
		}
	}
	for _, x := range p.NoiseHeaders {
		if strings.EqualFold(x, n) {
			return true
		}
	}
	return false
}

func HeaderDiff(a, b *Snapshot) []HeaderRow {
	collect := func(s *Snapshot) (map[string][]string, []string) {
		m := map[string][]string{}
		var order []string
		for _, h := range s.Headers {
			n := strings.ToLower(h.K)
			if _, ok := m[n]; !ok {
				order = append(order, n)
			}
			m[n] = append(m[n], h.V)
		}
		return m, order
	}
	ma, oa := collect(a)
	mb, ob := collect(b)
	seen := map[string]bool{}
	var rows []HeaderRow
	for _, n := range append(oa, ob...) {
		if seen[n] {
			continue
		}
		seen[n] = true
		va, okA := ma[n]
		vb, okB := mb[n]
		r := HeaderRow{Name: n, Before: strings.Join(va, " | "), After: strings.Join(vb, " | "), Noise: isNoiseHeader(a.Page.Project, n)}
		switch {
		case !okA:
			r.Kind = "added"
		case !okB:
			r.Kind = "removed"
		case r.Before != r.After:
			r.Kind = "changed"
		default:
			r.Kind = "same"
		}
		rows = append(rows, r)
	}
	return rows
}

var builtinMasks = []*regexp.Regexp{
	regexp.MustCompile(`(name="csrf-token" content=")[^"]*`),
	regexp.MustCompile(`(nonce=")[^"]*`),
}

func MaskBody(body string, p *Project) string {
	for _, re := range builtinMasks {
		body = re.ReplaceAllString(body, "${1}⟨masked⟩")
	}
	for _, m := range p.NoiseMasks {
		if re, err := regexp.Compile(m); err == nil {
			body = re.ReplaceAllString(body, "⟨masked⟩")
		}
	}
	return body
}

type DLine struct {
	Op   string // " ", "+", "-", "gap"
	A, B int
	Text string
}

func lineDiff(a, b []string) []DLine {
	n, m := len(a), len(b)
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else {
				dp[i][j] = max(dp[i+1][j], dp[i][j+1])
			}
		}
	}
	var out []DLine
	i, j := 0, 0
	for i < n || j < m {
		switch {
		case i < n && j < m && a[i] == b[j]:
			out = append(out, DLine{" ", i + 1, j + 1, a[i]})
			i++
			j++
		case i < n && (j == m || dp[i+1][j] >= dp[i][j+1]):
			out = append(out, DLine{"-", i + 1, 0, a[i]})
			i++
		default:
			out = append(out, DLine{"+", 0, j + 1, b[j]})
			j++
		}
	}
	return collapse(out, 3)
}

func collapse(ls []DLine, ctx int) []DLine {
	var out []DLine
	i := 0
	for i < len(ls) {
		if ls[i].Op != " " {
			out = append(out, ls[i])
			i++
			continue
		}
		j := i
		for j < len(ls) && ls[j].Op == " " {
			j++
		}
		run := ls[i:j]
		head, tail := ctx, ctx
		if i == 0 {
			head = 0
		}
		if j == len(ls) {
			tail = 0
		}
		if len(run) > head+tail+1 {
			out = append(out, run[:head]...)
			out = append(out, DLine{Op: "gap", Text: fmt.Sprintf("%d unchanged lines", len(run)-head-tail)})
			out = append(out, run[len(run)-tail:]...)
		} else {
			out = append(out, run...)
		}
		i = j
	}
	return out
}

type SRow struct {
	L, R *DLine
	Gap  string
}

func splitRows(ls []DLine) []SRow {
	var rows []SRow
	var dels, adds []DLine
	flush := func() {
		for k := 0; k < max(len(dels), len(adds)); k++ {
			var r SRow
			if k < len(dels) {
				d := dels[k]
				r.L = &d
			}
			if k < len(adds) {
				a := adds[k]
				r.R = &a
			}
			rows = append(rows, r)
		}
		dels, adds = nil, nil
	}
	for _, l := range ls {
		switch l.Op {
		case "-":
			dels = append(dels, l)
		case "+":
			adds = append(adds, l)
		case "gap":
			flush()
			rows = append(rows, SRow{Gap: l.Text})
		default:
			flush()
			x := l
			rows = append(rows, SRow{L: &x, R: &x})
		}
	}
	flush()
	return rows
}

type DiffView struct {
	Snap, Prev *Snapshot
	Fields     []FieldChange
	AnySEO     bool
	Headers    []HeaderRow
	NoiseHit   []HeaderRow
	BodyNote   string
	Unified    []DLine
	Split      []SRow
	Summary    *Changed
}

func BuildDiff(s *Snapshot) *DiffView {
	d := &DiffView{Snap: s, Prev: s.Prev, Summary: Classify(s)}
	if s.Prev == nil {
		return d
	}
	d.Fields = fieldChanges(s.Prev, s)
	for _, f := range d.Fields {
		if f.Changed {
			d.AnySEO = true
		}
	}
	for _, h := range HeaderDiff(s.Prev, s) {
		if h.Kind == "same" {
			continue
		}
		if h.Noise {
			d.NoiseHit = append(d.NoiseHit, h)
		} else {
			d.Headers = append(d.Headers, h)
		}
	}
	switch {
	case s.Prev.Body == "" && s.Body != "":
		d.BodyNote = "body added"
	case s.Prev.Body != "" && s.Body == "":
		d.BodyNote = "body removed"
	default:
		a := strings.Split(MaskBody(s.Prev.Body, s.Page.Project), "\n")
		b := strings.Split(MaskBody(s.Body, s.Page.Project), "\n")
		d.Unified = lineDiff(a, b)
		changed := false
		for _, l := range d.Unified {
			if l.Op == "+" || l.Op == "-" {
				changed = true
			}
		}
		if !changed {
			d.Unified = nil
		}
		d.Split = splitRows(d.Unified)
	}
	return d
}

// History entry of one Page in one Check.
type HistEntry struct {
	Check   *Check
	Snap    *Snapshot
	Fail    *Failure
	Changed *Changed
}

func (p *Page) History() []HistEntry {
	var out []HistEntry
	for _, c := range p.Project.ChecksDesc() {
		e := HistEntry{Check: c}
		for _, s := range p.Snapshots {
			if s.Check == c {
				e.Snap = s
				e.Changed = Classify(s)
			}
		}
		for _, f := range p.Failures {
			if f.Check == c {
				e.Fail = f
			}
		}
		if e.Snap != nil || e.Fail != nil {
			out = append(out, e)
		}
	}
	return out
}

// ---------- fake data ----------

type DB struct {
	Projects  []*Project
	Pages     map[int]*Page
	Checks    map[int]*Check
	Snapshots map[int]*Snapshot
	nextID    int
}

func (db *DB) id() int { db.nextID++; return db.nextID }

func (db *DB) project(domain, schedule string) *Project {
	p := &Project{ID: len(db.Projects) + 1, Domain: domain, Schedule: schedule, UA: "SEOSiteChecker/0.1", Timeout: 30, MaxPar: 2}
	db.Projects = append(db.Projects, p)
	return p
}

func (db *DB) page(p *Project, path string, first *Check) *Page {
	pg := &Page{ID: db.id(), Project: p, Path: path, State: "active", FirstSeen: first}
	p.Pages = append(p.Pages, pg)
	db.Pages[pg.ID] = pg
	return pg
}

func (db *DB) check(p *Project, trigger string, ago time.Duration, took time.Duration) *Check {
	c := &Check{ID: db.id(), Project: p, Trigger: trigger, Started: now.Add(-ago), Status: "finished"}
	c.Finished = c.Started.Add(took)
	p.Checks = append(p.Checks, c)
	db.Checks[c.ID] = c
	return c
}

func (db *DB) snap(pg *Page, c *Check, mod func(s *Snapshot)) *Snapshot {
	var s Snapshot
	if last := pg.Last(); last != nil {
		s = *last
		s.Headers = append([]KV(nil), last.Headers...)
		s.Extra = append([]string(nil), last.Extra...)
		s.Prev = last
	} else {
		title := strings.Trim(strings.ReplaceAll(pg.Path, "/", " "), " ")
		if title == "" {
			title = "Home"
		}
		s.Status = 200
		s.SEO = SEO{Title: strings.Title(title) + " | " + pg.Project.Domain, Desc: "All about " + title + ".", Canonical: pg.URL(), H1: strings.Title(title), OGTitle: strings.Title(title), Lang: "en"}
		s.Extra = []string{"<p>", "Welcome to " + title + ".", "</p>"}
		s.Headers = []KV{{"Content-Type", "text/html; charset=utf-8"}, {"Date", ""}, {"Cache-Control", "max-age=300"}, {"Server", "nginx"}, {"ETag", ""}, {"X-Request-Id", ""}, {"Set-Cookie", ""}}
		if pg.Project.Domain == "example.com" {
			s.Headers = append(s.Headers, KV{"X-Varnish", ""})
		}
	}
	s.ID = db.id()
	s.Page, s.Check = pg, c
	s.Fetched = c.Started.Add(time.Duration(len(c.Snapshots)*2) * time.Second)
	s.TimingMs = 80 + (s.ID*37)%300
	if mod != nil {
		mod(&s)
	}
	if s.NoBody {
		s.Body = ""
	} else {
		s.Body = renderBody(&s)
	}
	h := fmt.Sprintf("%x", sha256.Sum256([]byte(s.Body)))[:12]
	for i := range s.Headers {
		switch strings.ToLower(s.Headers[i].K) {
		case "date":
			s.Headers[i].V = s.Fetched.UTC().Format(time.RFC1123)
		case "etag":
			s.Headers[i].V = `W/"` + h + `"`
		case "x-request-id":
			s.Headers[i].V = fmt.Sprintf("req-%08x", s.ID*2654435761)
		case "set-cookie":
			s.Headers[i].V = fmt.Sprintf("sid=%x; Path=/; HttpOnly", s.ID*40503)
		case "x-varnish":
			s.Headers[i].V = fmt.Sprintf("%d %d", s.ID*7919, s.ID*104729)
		}
	}
	pg.Snapshots = append(pg.Snapshots, &s)
	c.Snapshots = append(c.Snapshots, &s)
	db.Snapshots[s.ID] = &s
	return &s
}

func (db *DB) fail(pg *Page, c *Check, kind, detail string) {
	f := &Failure{Page: pg, Check: c, Kind: kind, Detail: detail}
	if n := len(pg.Failures); n > 0 && len(c.Project.Checks) > 1 && pg.Failures[n-1].Check == c.Project.Checks[len(c.Project.Checks)-2] {
		f.AlsoPrev = true
	}
	pg.Failures = append(pg.Failures, f)
	c.Failures = append(c.Failures, f)
}

func setHeader(s *Snapshot, k, v string) {
	for i := range s.Headers {
		if strings.EqualFold(s.Headers[i].K, k) {
			if v == "" {
				s.Headers = append(s.Headers[:i], s.Headers[i+1:]...)
			} else {
				s.Headers[i].V = v
			}
			return
		}
	}
	if v != "" {
		s.Headers = append(s.Headers, KV{k, v})
	}
}

func renderBody(s *Snapshot) string {
	h := s.SEO
	l := []string{"<!doctype html>", `<html lang="` + h.Lang + `">`, "<head>", `<meta charset="utf-8">`, "<title>", h.Title, "</title>",
		`<meta name="description" content="` + h.Desc + `">`, `<link rel="canonical" href="` + h.Canonical + `">`}
	if h.Robots != "" {
		l = append(l, `<meta name="robots" content="`+h.Robots+`">`)
	}
	l = append(l, `<meta property="og:title" content="`+h.OGTitle+`">`,
		fmt.Sprintf(`<meta name="csrf-token" content="%x">`, s.ID*2246822519),
		fmt.Sprintf(`<script nonce="%x">`, s.ID*3266489917), "window.dataLayer = [];", "</script>", "</head>", "<body>",
		"<nav>", `<a href="/">`, "Home", "</a>", `<a href="/pricing">`, "Pricing", "</a>", `<a href="/blog">`, "Blog", "</a>", "</nav>",
		"<main>", "<h1>", h.H1, "</h1>")
	l = append(l, s.Extra...)
	l = append(l, "</main>", "<footer>", "© 2026", "</footer>", fmt.Sprintf("<!-- Rendered in %dms -->", s.TimingMs/3), "</body>", "</html>")
	return strings.Join(l, "\n")
}

func seed() *DB {
	db := &DB{Pages: map[int]*Page{}, Checks: map[int]*Check{}, Snapshots: map[int]*Snapshot{}}

	// ---- example.com: the rich one ----
	p := db.project("example.com", "daily")
	p.Excludes = []string{"/search*", "/tag/**"}
	p.NoiseHeaders = []string{"X-Varnish"}
	p.NoiseMasks = []string{`Rendered in \d+ms`}
	p.Headers = []KV{{"Authorization", "Basic c3RhZ2luZzpzZWNyZXQ="}}
	c1 := db.check(p, "manual", 9*24*time.Hour, 41*time.Second)
	paths := []string{"/", "/pricing", "/blog", "/blog/launch", "/about", "/careers", "/old-promo", "/team", "/legacy", "/contact", "/admin", "/search?q=shoes"}
	pg := map[string]*Page{}
	for _, path := range paths {
		pg[path] = db.page(p, path, c1)
		db.snap(pg[path], c1, nil)
	}
	pg["/legacy"].State = "orphaned"
	pg["/admin"].State = "ignored"
	pg["/search?q=shoes"].State = "excluded"

	cx := db.check(p, "schedule", 5*24*time.Hour, 12*time.Second)
	cx.Status = "cancelled"
	for _, path := range []string{"/", "/pricing", "/blog"} {
		db.snap(pg[path], cx, nil)
	}

	c2 := db.check(p, "schedule", 26*time.Hour, 38*time.Second)
	for _, path := range paths[:10] {
		path := path
		db.snap(pg[path], c2, func(s *Snapshot) {
			if path == "/pricing" {
				s.SEO.H1 = "Simple pricing"
			}
		})
	}

	c3 := db.check(p, "schedule", 2*time.Hour, 47*time.Second)
	c3.Dropped = 3
	for _, path := range []string{"/", "/pricing", "/blog", "/blog/launch", "/about", "/careers", "/old-promo", "/team"} {
		path := path
		db.snap(pg[path], c3, func(s *Snapshot) {
			switch path {
			case "/pricing":
				s.SEO.Title = "Pricing plans from $9/mo | example.com"
				s.SEO.Desc = "Compare plans. Start free."
				s.Extra = []string{"<p>", "Plans start at $9/month.", "</p>", "<table>", "<tr>", "<td>", "Pro", "</td>", "</tr>", "</table>"}
			case "/blog/launch":
				s.SEO.Robots = "noindex, follow"
			case "/about":
				setHeader(s, "Cache-Control", "no-store")
				setHeader(s, "Strict-Transport-Security", "max-age=63072000")
			case "/careers":
				s.Extra = append(s.Extra, "<p>", "We are hiring a Go engineer.", "</p>")
			case "/old-promo":
				s.Status = 301
				s.NoBody = true
				s.Redirect = "https://example.com/promo"
				setHeader(s, "Location", "/promo")
				setHeader(s, "Content-Type", "text/plain; charset=utf-8")
			}
		})
	}
	db.fail(pg["/contact"], c3, "timeout", "no response within 30s")
	pg["/team"].State, pg["/team"].Orphans = "orphaned", 1
	c3.Vanished = []*Page{pg["/team"]}
	pg["/legacy"].State = "retired"
	c3.Retired = []*Page{pg["/legacy"]}
	for _, path := range []string{"/docs/api", "/promo"} {
		pg[path] = db.page(p, path, c3)
		db.snap(pg[path], c3, nil)
	}

	// ---- shop.example.org: has a running Check ----
	q := db.project("shop.example.org", "hourly")
	q.MaxPar = 4
	q.Robots = true
	s1 := db.check(q, "manual", 3*time.Hour, 3*time.Minute)
	s1.Dropped = 4
	qp := map[string]*Page{}
	for _, path := range []string{"/", "/products", "/products/widget", "/cart", "/checkout"} {
		qp[path] = db.page(q, path, s1)
		db.snap(qp[path], s1, nil)
	}
	s2 := db.check(q, "schedule", 2*time.Hour, 2*time.Minute)
	for path, page := range qp {
		path := path
		db.snap(page, s2, func(s *Snapshot) {
			if path == "/cart" {
				setHeader(s, "Cache-Control", "private, no-cache")
			}
		})
	}
	run := db.check(q, "schedule", 4*time.Minute, 0)
	run.Status, run.Total = "running", 40

	// ---- blog.acme.dev: seed failure ----
	b := db.project("blog.acme.dev", "manual only")
	b1 := db.check(b, "manual", 4*24*time.Hour, 20*time.Second)
	bp := map[string]*Page{}
	for _, path := range []string{"/", "/posts/hello-world", "/posts/go-generics", "/about"} {
		bp[path] = db.page(b, path, b1)
		db.snap(bp[path], b1, nil)
	}
	b2 := db.check(b, "manual", 1*time.Hour, 34*time.Second)
	b2.SeedFailed = true
	db.snap(bp["/"], b2, func(s *Snapshot) {
		s.Status = 503
		s.NoBody = true
		setHeader(s, "Retry-After", "120")
	})
	db.snap(bp["/about"], b2, nil)
	db.fail(bp["/posts/hello-world"], b2, "connection", "connection reset by peer")
	db.fail(bp["/posts/go-generics"], b2, "TLS", "x509: certificate has expired")
	return db
}
