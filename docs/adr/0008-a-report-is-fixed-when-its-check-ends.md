# A Report is fixed when its Check ends

Story 78 of the MVP spec (#19) left ignored and excluded Pages out of the Report, "so that the Report only shows what the site did, not what I did". The code applied that to the Page pool as it is now: a Report left out every Page that is ignored today or that today's exclude patterns match. A Report's Changed and Unchanged counts therefore kept changing after its Check ended and could not be stored. The Report, each row of the Checks tab and each row of the home screen recounted them from the Check's Diffs on every request, which at 2M Pages is millions of rows per page view (#71). The screens also disagreed, because the Checks tab left out ignored Pages but not excluded ones.

A Report now records what its Check found. FinalizeCheck stores the number of Diffs in each tier and whether the seed Page failed, next to the Report counts it already stores (ADR 0007), and nothing changes them afterwards. Ignoring or excluding a Page affects later Checks only. Story 78 still holds for a Page ignored or excluded before a Check, because that Page gets no Fetch in it.

## Considered Options

- **Keep the counts live and adjust stored counters when a Page is ignored or restored**: rejected. Saving an exclude pattern would have to recount every ended Check of the Project against the new patterns, which is the pool-sized work this removes, moved to another request.
- **Store the counts but keep hiding now-ignored Pages from the lists**: rejected. A section's total and its rows would disagree ("Rows 1–50 of 340" over 337 rows).

## Consequences

- A Page ignored or excluded after a Check stays in that Check's Report as an ordinary row, with no marker. An old Report can't be cleaned up by ignoring a Page.
- Every Diff of a Check counts, including one of a Page ignored while the Check ran.
- The Checks that ended before this change are backfilled by counting every Diff, so their Changed counts may change once.
- The New, Vanished and Retired sections stop hiding Pages ignored now. Their totals then equal the header counts, which always included those Pages.
- Implemented by #76. #78 then makes the Report's lists cheap to page through.
