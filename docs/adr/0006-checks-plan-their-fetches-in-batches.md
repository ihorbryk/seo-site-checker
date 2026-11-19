# A Check plans its Fetches in batches

Before its lanes run (ADR 0003), a Check creates one pending Fetch per active, non-excluded Page. StartCheck used to do that in one River job and one transaction. URL discovery has no limit except exclude patterns, so a Page pool can reach millions of Pages. At 2,042,781 Pages the insert took about 30 s on a laptop, where 19 s of it were the two foreign-key checks per Fetch row. On production hardware it ran past River's default 1-minute job timeout on every attempt, so the Check never started.

Now StartCheck plans one batch of Pages per job (`worker.DefaultPlanBatchSize`, 20,000 by id). The first job finds the Check `queued`. It freezes the Request settings and Noise rules onto the Check, stores robots.txt and marks the Check `planning`. Every job then lists the next batch of active Pages after the Check's `plan_cursor`, plans Fetches for the ones no exclude pattern matches, and advances the cursor. In the same transaction it enqueues the job for the next batch, carrying the cursor as `after_page_id`. The batch that reaches the end of the pool starts the lanes and marks the Check `running`. Each job costs the same however large the pool grows.

## Considered Options

- **Keep one job and give it a longer timeout**: rejected. The job must stay below River's stuck-job delay (5 min), or the rescuer starts a duplicate. Planning would still grow with the pool, and the ceiling would only move.
- **Create Fetches lazily, with lanes claiming the next unfetched Page directly**: rejected. A Check would no longer hold one Fetch per Page from the start. The progress counts, the skipped Fetches of a cancelled Check and the Report counts would all have to be derived another way, which is a much larger change to ADR 0003.
- **Mark the Check `running` while it is still planning, and let lanes start on the first batch**: rejected. The lane that brings `lanes` to 0 would then need to know whether planning has ended before it enqueues FinalizeCheck. That couples the two job kinds through another flag on the Check row.

## Consequences

- `planning` is a new active Check state. It counts for the one-active-Check index, the scheduler and the UI. Cancelling a planning Check makes it `cancelling`. Its next StartCheck job sees that and enqueues FinalizeCheck, since no lane will end to do it. FinalizeCheck then skips the Fetches planned so far.
- A StartCheck job acts only if its `after_page_id` is still the Check's `plan_cursor`. A retried or rescued duplicate therefore does nothing, the way a lane re-runs only the Fetch its job id claimed.
- Planning is no longer one snapshot of the pool. A Page added or ignored during planning is planned if its id is beyond the cursor and it is active when its batch is read.
- StartCheck has its own timeout (3 min: a robots.txt fetch of up to 120 s plus one batch), kept below `rescueStuckJobsAfter`.
- `fetches_pending_check_id_idx (check_id, id) WHERE state = 'pending'` replaces the index on `check_id` alone. A lane's claim of the lowest pending Fetch then reads one index entry instead of walking the Check's done Fetches: 213 ms before and 1 ms after, halfway through a 2M-Fetch Check. `pages_project_state_id_idx` serves the batch reads.
- FinalizeCheck still runs as one job and one transaction, and so do several web pages' aggregates. They are the next limits for a pool of this size, and they are tracked separately.
