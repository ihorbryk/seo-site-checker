# One container image per process, plus an Operator image; migrations run as a one-shot compose service

Production runs from container images on GHCR, built by a GitHub Actions workflow when a GitHub Release is published, and run with Docker Compose. There is one image per long-running process (`ghcr.io/ihorbryk/seo-site-checker/web`, `…/scheduler`, `…/worker`) and one `…/operator` image holding the Operator's commands, `migrate` and `user`. All four come from one Dockerfile (a shared builder stage, one final target each) and always carry the same release version, pinned in compose by a single `VERSION` variable, because the processes coordinate through one shared schema (ADR 0001) and must never run mismatched. Compose runs `operator migrate` as a one-shot service that `web`, `scheduler` and `worker` wait on (`service_completed_successfully`), which keeps ADR 0001's rule that no long-running process migrates on boot.

## Considered Options

- **One image with every binary, selected by `command`**: simpler to build and pull, but each process ships binaries it never runs, and an image name says nothing about what it runs.
- **Each process migrates on startup**: no extra service, but three processes race to migrate, and a failed migration surfaces as a crash-looping web server instead of one failed step.
- **The `goose` CLI in the image**: works, but adds a second tool alongside migrations that are already embedded in the binaries (`db.Migrations()`).

## Consequences

- `seed` is not in any image; it stays a `mise` dev task.
- The Operator creates Users with `docker compose run --rm -it operator user create <email>`. The `operator` service sits behind a compose profile so `up` never starts it.
- Adding a new process means a new Dockerfile target, a new matrix entry in the release workflow, and a new compose service.
- `BOOTSTRAP_EMAIL` (read by migration 00039) must be passed to the `migrate` service.
