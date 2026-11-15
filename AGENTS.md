# Working on Vectorized

Read [SERIES.md](.project-notes/SERIES.md) before starting work. It is the single source of
truth for implementation stages, behavior contracts, acceptance tests, scope,
and collaboration rules. Follow its section 1 agent workflow.

The owner writes production code. Agents write tests by default: only
`*_test.go` and necessary test fixtures may be edited for an implementation task.
Do not add production fixes, stubs, interfaces, dependency changes, or refactors
to make tests pass or compile. Inspect code, run tests, and explain failures as
hints. Documentation may be edited when the owner requests documentation work.
An explicit owner request can override this default for that request.

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
