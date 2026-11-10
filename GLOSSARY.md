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
A Page that no Snapshot in a Check linked to. It is still fetched.
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
One pass over a Project's Page pool, started by its schedule or manually.
_Avoid_: Run, crawl, scan, job

**Snapshot**:
What one fetch of one Page during a Check stored when an HTTP response was received (any status): status, response headers, raw HTML (HTML responses only), timing. Snapshots are kept forever.
_Avoid_: Response, capture, version

**Fetch failure**:
A fetch of a Page during a Check that received no HTTP response (timeout, DNS, TLS, connection error, or an HTML body over the size cap). It is not a Snapshot and never serves as a Diff baseline.
_Avoid_: Error snapshot, failed Snapshot

**SEO fields**:
The values extracted from a Snapshot (title, meta description, canonical, robots, h1, hreflang, redirects) that are diffed as structured data.
_Avoid_: Metadata, tags

**Diff**:
The difference between a Page's Snapshot and that Page's immediately previous Snapshot.
_Avoid_: Change, delta, comparison

**Report**:
The per-Check summary of all Diffs, plus Pages that were new, vanished, or failed during that Check.
_Avoid_: Summary, digest, result
