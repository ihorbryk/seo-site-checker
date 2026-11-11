# A Check runs as lanes of chained River jobs

Each River job that executes a Check is a **lane**. A lane claims one pending Fetch, performs it, and then, in the same transaction that stores the result and completes the job (`JobCompleteTx`), claims the next pending Fetch and enqueues itself again. When nothing is pending, the lane ends. A Check runs at most `max parallel requests` lanes (1–10), tracked by a `lanes` counter on the Check row and changed under a row lock. URL discovery starts new lanes up to that limit. The lane that brings the counter to 0 enqueues the unique `FinalizeCheck` job. This is how the per-Project concurrency limit is enforced, because River OSS has no per-key limits (ADR 0002). It also keeps each Project's footprint in the queue at most 10 jobs.

## Considered Options

- **One long-running job per Check with an in-process goroutine pool**: rejected. River's stuck-job rescuer would start a duplicate of any job running longer than its timeout plus the rescue delay. Disabling the timeout (`-1`) means a crashed Check is never recovered. There's no heartbeat in OSS.
- **One job per Fetch, all enqueued up front, limited by snoozing**: rejected. A large Project floods the queue with jobs that keep waking and snoozing again, which delays other Projects. It also needs a crash-safe slot or lease table.
- **Dynamic per-Project River queues with `MaxWorkers`**: rejected. The limit holds per worker process, not across processes, and every queue runs its own producer.

## Consequences

- Every lane step must be safe to repeat. A retried lane re-runs the Fetch whose claim carries its job id.
- River's stuck-job delay is set to about 5 min, above the lane timeout (the Check's fetch timeout + 30s), so a crashed Check resumes within minutes of a worker returning.
- A lane that ends and a Fetch that adds work race on the Check row, so the counter must only change under that row lock.
- A delay between requests to the same host, if fetch policy wants one, is added by scheduling the lane's next job later.
