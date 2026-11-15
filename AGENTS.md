# Working on Vectorized

Read [SERIES.md](.project-notes/SERIES.md) before starting work. It is the single source of
truth for implementation stages, behavior contracts, acceptance tests, scope,
and collaboration rules. Follow its section 1 agent workflow.

## Implementation ownership

The owner writes implementation code. Agents write tests only: edit
`*_test.go` and necessary test fixtures for the requested behavior.

- Do not write or modify production implementations, fixes, stubs, interfaces,
  generated implementation code, dependencies, or production refactors.
- Do not make production changes to get tests to compile or pass. Report missing
  APIs, build failures, and failing assertions for the owner to implement.
- Do not supply finished implementation code in chat. Help the owner with
  graduated hints, invariants, design explanations, and code-review feedback.
- Agents may inspect source, run relevant checks, and explain results. Test
  helpers must not contain an alternate implementation of the database.
- Requests to fix or implement behavior remain test-and-guidance tasks. Do not
  treat them as permission to take over the owner's implementation.

Documentation edits are allowed when the owner requests documentation work.
These ownership rules take precedence over older workflow wording in the
private project notes.

Work on one requested stage/substep at a time. Test intended behavior rather
than preserving bugs. Use real configurable `BasicMemTable` instances in Engine
tests. Preserve unrelated work and report validation accurately.

`arch/` contains supporting implementation notes; `.project-notes/REVIEWS.md` is historical.
When they conflict with intended behavior, follow `.project-notes/SERIES.md` and flag the drift.

Personal roadmaps, task lists, reviews, and session notes belong in the
gitignored `.project-notes/` directory. Public `arch/` documents explain component
architecture and formats; keep dates, commit IDs, test logs, and agent workflow
out of them. If the private roadmap is absent, use the owner's instructions and
public architecture docs; do not recreate personal trackers in public docs.
