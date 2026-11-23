# A Report section is read a page at a time, by walking the Pages or from its own index

Once a Report's counts were stored on its Check (ADR 0008), its lists were still built the expensive way. The Changed section read every Diff of the Check into Go, sorted by tier and URL, and sliced a page out. The other sections found their rows through the Check's Fetches or page events, sorted every matching row by URL and only then applied `LIMIT`/`OFFSET`, and each recounted a total the Check already stores (#71, #78). On a 2M-Page Check one page view was a 2M-row sort.

Every section is ordered by URL (Changed Pages by tier first), and the URL lives on `pages`, so no index of the Check's rows can be in URL order. A page is therefore read one of two ways, chosen in Go from the section's stored count:

- **Dense**: walk the Project's Pages in URL order through the unique index on `(project_id, url)` and probe each Page for a row of the Check, through `fetches (check_id, page_id)`, `page_events` or, for a Diff, the Fetch with the Snapshot's id. It stops at the page's limit. It costs about `Fetches / count` probes per row.
- **Sparse**: read the section's rows from an index that holds only them, then look up each row's Page and sort. It costs the section's rows, at most `Fetches / 100`.

A section of `n` rows is dense when `n * 100 >= Fetches`, `Fetches` being the Check's stored Snapshot, failure and skipped counts. Either way the first page costs about 10,000 index probes or fewer at 2M Pages, and the web app never reads or sorts a whole section. The `LATERAL ... OFFSET 0` in each query keeps the planner from reordering the dense walk or joining the sparse rows in bulk on a stale estimate. Measured timings are in the PR for #78.

Changed Pages are read per tier (critical, SEO, headers, body) from the four stored tier counts: a page covers a range of positions in the section, and each tier it overlaps is read at its own offset, so a page can span two tiers. Only the Snapshot ids are read this way; the costly columns (SEO changes, redirect target) come from `ListCheckDiffRows` for the ids on the page, so the formatting and redirect-chain lookups still run only for them.

Section totals are the stored counts, so `CountFetchFailures`, `CountSkippedFetches`, `CountPageEvents` and `ListCheckDiffStates` are gone.

## Indexes

Migration 00048 adds, concurrently and safe to rerun:

- `diffs.check_id`, backfilled from the Snapshot in committed batches of 10,000 Diffs, filled by a BEFORE INSERT trigger when an insert omits it (the old worker still runs while the migration does; the trigger is kept, one skipped branch for new code), and `diffs_check_tier_idx (check_id, tier, snapshot_id) WHERE tier <> 'empty'`. A Diff had no way to find its Check except through its Snapshot, and a Check's Diffs of one tier could only be found by reading all its Snapshots. It has no foreign key: a Diff goes with its Snapshot, which goes with the Check.
- `fetches_check_outcome_idx (check_id, outcome, page_id) WHERE outcome IN ('failure', 'skipped')`.
- `page_events_check_kind_idx (check_id, kind, page_id)`.

## Considered Options

- **Keyset pagination**: rejected as out of scope; the pager and its links stay as #41 and #42 defined them, so a page is still reached by its number.
- **Copy the URL onto fetches, diffs and page events, and index it with the Check**: rejected. One index would serve every section with no choice to make, but it stores every URL up to four more times on tables of millions of rows.
- **Always walk the Pages**: rejected. A section with 10 rows in a 2M-Page Project would probe every Page to find them.
- **Always read the section's own index and sort**: rejected. It sorts 2M rows for a section that holds 2M (2.8 s measured for Skipped when every Page was skipped).

## Consequences

- A very deep page of a dense section is slow, because the walk has to pass the rows before it (3.9 s at page 20,000 of 2M Changed Pages). The cost grows with how deep the page is, not with the size of the Check.
- The pool used to choose is the Check's own Fetches, not the Project's Pages. A Project with many ignored or excluded Pages walks past them without finding a row, so a dense read costs somewhat more than estimated there; both reads stay correct.
- Rows deleted from a Check's Diffs outside FinalizeCheck would leave the stored counts and the lists disagreeing, as for every stored count (ADR 0008).
