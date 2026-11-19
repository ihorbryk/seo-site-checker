# A Check is finalized in batches

Once a Check's last lane ends, FinalizeCheck records the page events, updates the orphan streaks and caches the Report counts (ADR 0003). It used to do all of that in one River job and one transaction. On a 2,042,781-Page Check that cannot fit River's 1-minute default timeout. Measured on a laptop:
- `ResetLinkedOrphanStreaks` took 28.9 s.
- `CountOrphans` took 8.6 s.
- `InsertNewPageEvents` took 39.5 s on a Project's first Check, which records 2M `new` events.

When it times out, the Check stays `running` and holds its Project's one active slot for good.

Now each FinalizeCheck job finalizes one batch (`worker.DefaultFinalizeBatchSize`, 10,000), the way StartCheck plans (ADR 0006). A batch is the Project's Pages with ids in `(after, upto]`. `upto` is the page_id of the Check's next 10,000th Fetch, read from the `fetches (check_id, page_id)` key, and the last batch runs to the end of the id range. Every Page belongs to exactly one batch, whether it was fetched in the Check or not, and no batch holds more than 10,000 of the Check's Fetches.

For its Pages a batch does the following:
- It resets or raises orphan streaks and records the `vanished` and `retired` events, for a running Check.
- It skips the pending Fetches, for a cancelling Check.
- It records the `new` and `reactivated` events.
- It adds its Fetch outcomes and events to the Report counts.

It then advances the Check's `finalize_cursor` and, in the same transaction, enqueues the next batch's job carrying the cursor as `after_page_id`. The last batch marks the Check finished or cancelled. At 2,042,781 Pages that takes 205 jobs, the longest 0.76 s.

Whether some other Page's Snapshot links to a Page is answered per Page through `links (target_page_id, snapshot_id)`:
- It first looks at the Check's own Snapshots, which have ids from `checks.first_fetch_id` on.
- Only if none of them links to the Page does it look at the latest earlier Snapshots of Pages skipped in the Check.

A `CASE` and `OFFSET 0` keep the planner from turning these lookups into a hash join over all the Check's links. That join took 4.7 s per batch instead of 38 ms.

## Considered Options

- **One job with a longer timeout**: rejected for the same reason as in ADR 0006. A job must stay below River's 5-minute stuck-job delay, and the work still grows with the Check.
- **Batches by Page id over the Project's whole id range**: rejected. A small Project whose Pages are spread among a large one's ids would take many near-empty jobs. Bounding the batches by the Check's own Fetches keeps the number of jobs proportional to the Check.
- **A visible `finalizing` Check state**: rejected. Every Fetch is done by then, and the only thing finalizing changes for the user is that Cancel no longer applies. A `finalize_cursor` that is no longer NULL says that without another state for the web pages, the scheduler and the migrations to handle.

## Consequences

- Once its first batch has begun, a Check can't be cancelled: `RequestCancel` skips it and the progress view hides Cancel. Before that, a Check whose lanes have ended can still be cancelled. It is then finalized as cancelled, with nothing pending to skip.
- A FinalizeCheck job acts only if its `after_page_id` is still the Check's `finalize_cursor`, so a retried or rescued duplicate does nothing. The first job, with no `after_page_id`, stays unique per Check.
- The Report counts are summed batch by batch instead of counted at the end.
- FinalizeCheck has its own 3-minute timeout, below `rescueStuckJobsAfter`.
- `links_target_snapshot_idx (target_page_id, snapshot_id)` replaces the index on `target_page_id` alone.
- Issue #70 proposed two more indexes, and neither is needed.
  - `pages (project_id) WHERE orphan_streak > 0`: the streak updates now read a batch's Pages through `pages_project_state_id_idx (project_id, state, id)` and are scoped to the Project.
  - `pages (reactivated_check_id)`: the `reactivated` events now start from the batch's Fetches.
- A job that exhausts its attempts still leaves its Check active. That gap predates batching and is outside #70's fix.
