# Coding standards

Judgement calls that no linter catches; read during review. Commands and test
mechanics are in `docs/testing.md`.

- **Domain enums are typed constants in their own package** (`internal/page`
  for Page state, origin and event kinds). Other code uses the constants; the
  `lint:literals` step enforces it for Pages.
- **Scenario tests go through `internal/systemtest`.** Wait with
  `System.WaitFor`, compare sets order-free, and extend `System` with a method
  when a test needs a new capability instead of inlining a helper.
- **The lane keeps its one transaction** (ADR 0003) but is split into named
  helpers, each doing one step of the Fetch.
- **A deliberate departure from the spec carries a comment naming the spec
  rule it departs from.**
