# SEO Site Checker

Periodically fetches the pages of monitored sites, keeps every response, and reports what changed between consecutive fetches.

## Language

**Project**:
One monitored site, identified by its domain (a single exact host, e.g. `example.com`, distinct from `www.example.com`), owning its Request settings and Page pool.
_Avoid_: Site, website, account, home URL

**Request settings**:
A Project's editable configuration for how and how often its Pages are fetched (schedule, user agent, headers, timeout, parallelism, exclude patterns, robots.txt). The domain is not a Request setting.
_Avoid_: Config, options, crawl settings

**Page**:
One URL belonging to a Project's Page pool.
_Avoid_: URL, link, resource

**Page pool**:
The set of Pages a Project checks: the seed Page (the domain's root over https) plus every Page found by URL discovery.
_Avoid_: URL list, sitemap, queue

**URL discovery**:
Adding links on the Project's domain found in a Snapshot's HTML to the Project's Page pool, unbounded except by exclude patterns.
_Avoid_: Crawling, spidering, link harvesting

**Orphaned Page**:
A Page that no other Page's Snapshot in a Check linked to (a Page linking or redirecting to itself doesn't count). It is still fetched.
_Avoid_: Dead page, unlinked page

**Vanished Page**:
A Page in the first Check in which it became orphaned, as listed in that Check's Report.
_Avoid_: Removed page, deleted page, 404 page

**Retired Page**:
A Page orphaned for three consecutive Checks, no longer fetched until URL discovery finds it again. Its Snapshots are kept.
_Avoid_: Deleted page, inactive page

**Ignored Page**:
A Page the user removed from the Page pool. It is never fetched or brought back by URL discovery unless the user restores it. Its Snapshots are kept.
_Avoid_: Deleted page, excluded page (exclusion comes from exclude patterns, not removal)

**Check**:
One pass over a Project's Page pool, started by its schedule or manually. A Project has at most one active Check at a time. A Check ends finished, or cancelled by the user, which leaves it partial.
_Avoid_: Run, crawl, scan, job

**Fetch**:
One attempt by a Check to retrieve one Page. It ends in a Snapshot, a Fetch failure, or a skip. A skip sends no request: robots.txt disallows the Page (or could not be read), or the Check was cancelled first. A Page has at most one Fetch per Check.
_Avoid_: Job, task, request

**Snapshot**:
What one fetch of one Page during a Check stored when an HTTP response was received (any status): status, response headers, raw HTML (HTML responses only), timing. Snapshots are kept forever.
_Avoid_: Response, capture, version

**Fetch failure**:
A fetch of a Page during a Check that received no HTTP response (timeout, DNS, TLS, connection error, or an HTML body over the size cap). It is not a Snapshot, never serves as a Diff baseline, and is never retried within the Check.
_Avoid_: Error snapshot, failed Snapshot

**SEO fields**:
The values extracted from a Snapshot that are diffed as structured data: from the HTML, title, meta description, canonical, meta robots, h1, hreflang, page language, Open Graph tags and structured-data types; from the response, status, content type, X-Robots-Tag and redirect target. Every occurrence is kept, so duplicates are visible.
_Avoid_: Metadata, tags

**Diff**:
The difference between a Page's Snapshot and that Page's immediately previous Snapshot, after Noise rules are applied. A Diff is empty when nothing differs after noise is removed. A Page's first Snapshot has no Diff.
_Avoid_: Change, delta, comparison

**Noise rule**:
A rule that stops a known-volatile part of a response from counting as a change in a Diff: either a header name to ignore, or a pattern in the HTML to mask. Some are built in and fixed; the rest belong to a Project. Each Check uses the Noise rules as they were when it started. Noise rules never apply to SEO fields.
_Avoid_: Filter, ignore pattern, exclude pattern (exclusion is about Pages), diff setting

**Report**:
The view of one finished Check: its changed Pages (non-empty Diffs) ranked by significance, plus the Pages that were new, vanished, retired, or had a fetch failure in that Check. Unchanged and skipped Pages are only counted. Every finished or cancelled Check has one, however many Fetches failed; a cancelled Check's Report is marked partial.
_Avoid_: Summary, digest, result
