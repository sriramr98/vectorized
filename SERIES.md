# Building Vectorized: implementation and teaching contract

This is the single source of truth for the project's implementation sequence,
observable behavior, acceptance tests, scope, and agent collaboration rules.
The owner implements the database; agents write tests and explain results.
Read this document before proposing changes or adding tests.

The goal is an understandable single-node database in Go built on an LSM storage
engine: first complete durable disk reads and writes, then teach exact-match,
ordered, and vector indexes over those records, then improve storage maintenance
and performance. Full-text and fuzzy search are extension chapters. Each stage
should make the previous stage's limitation visible and fix one explainable
problem. The teaching order and implementation order are the same.

This is a target specification, not a claim that the listed features already
work. Proposed defaults below are project decisions for future implementation.
Changing a contract requires an explicit owner decision and an update here.

## 1. Authority and collaboration

- The owner's latest explicit instructions take precedence. Otherwise this
  document governs intended behavior. Existing code and tests are evidence of
  implementation, not permission to redefine the contract around a bug.
- `AGENTS.md` points here. `arch/` documents implementation details;
  `REVIEWS.md` is historical review material. Neither is a competing roadmap.
- By default agents may edit only `*_test.go` and test fixtures needed for the
  requested slice. Benchmarks and fuzz targets belong in test files. Agents may
  inspect production code, run checks, review, and explain failures.
- Agents must not write production implementations, stubs, interfaces, generated
  implementation code, dependency changes, or production refactors to make tests
  compile. The owner writes these. Documentation edits require a documentation
  request; this roadmap was created under such a request.
- Do not provide an unsolicited full implementation in chat. Give behavioral
  explanations and graduated hints; provide solution code only if requested.
- Add tests for one agreed stage or substep at a time. Do not pre-create every
  future suite, skip failures to get green, weaken assertions to match bugs, or
  add an entire alternate database under the label of a test helper.
- Use the real `BasicMemTable` with configurable limits for Engine tests.
  Fakes are appropriate for WAL failures, filesystem boundaries, clocks, and
  deterministic scheduling, not duplicate memtable implementations.
- Preserve unrelated changes. Do not commit, tag, or modify dependencies unless
  asked. Keep each eventual commit about one concern.
- If an API is missing, propose its minimal shape in prose. If the owner has
  already supplied a shape, test it and report an expected compile failure.
  Otherwise finish tests against available APIs and identify the precise missing
  contract; do not invent a large interface independently.

Agent workflow for every requested slice:

1. Read this file, applicable instructions, current source, and relevant tests.
   Check the worktree and current branch.
2. State the stage ID, intended behavior, and files to be changed. Identify
   ambiguities that materially change expectations before encoding them.
3. Add focused behavior tests, including failures and boundaries. Prefer public
   observations; same-package tests are acceptable for genuine internal contracts.
4. Run the relevant package tests. Distinguish assertion failures, build failures,
   environmental failures, and tests not run.
5. Report what the owner needs to implement and why the failing case matters.
   Never silently repair production code.
6. Once implementation exists, rerun focused checks and the applicable integration
   and race checks. Update completion evidence only when asked to update docs.

A stage is complete only when its contract, tests, integration proof, and stated
limitations agree. A passing happy path alone is not completion.

### Finish a useful slice, then move on

Each stage has a finite behavior contract, a runnable proof, and an explicit
defer list. Once those pass, proceed to the next stage; do not keep extending the
current component with new data structures, tuning, or speculative edge features.
Correctness needed by the next slice stays mandatory: durability, latest-version
visibility, tombstone handling, bounded decoding, and coherent publication.
Performance targets, comprehensive cross-feature stress runs, and optimizations
belong to the later stages that name them. An acceptance test involving a future
feature becomes active when that feature arrives; it cannot block an earlier
stage or silently require implementing the future stage early.

Keep all already-started work in the series, including TCP/RESP, WAL, memtables,
and Engine tests. Existing coverage is reused, not rewritten to restart the
series. Remaining correctness failures are owner implementation work; do not
weaken their assertions to accelerate the roadmap.

## 2. Starting point and destination

### Inspected baseline

Historical inspection on 2026-09-30 at `46ffcc9` on `feat/lsm`. That inspection
was a source review. Section 8 records the current 2026-10-16 working-tree test
run; the table below describes the starting foundation rather than current
completion evidence.

| Area | Existing code | What this establishes |
| --- | --- | --- |
| TCP and RESP | `transport/tcpserver`, `protocol/resp`, `protocol/command`, `handlers` | A small KV adapter exists; not general Redis compatibility. |
| WAL | `db/wal`, `arch/wal.md` | Versioned CRC records, per-write sync, segment rotation, one-time startup replay, torn-tail handling, unhealthy state. |
| Memtable | `db/memory.go`, `db/store.go` | Map-backed records containing key, bytes, LSN, tombstone; size accounting, copies, freeze, sorted export. |
| Engine | `core/engine.go` | WAL-before-apply, SET rotation, active/frozen reads, recovery into a replacement table. |
| Tests | `db/memory_test.go`, `core/engine_test.go`, WAL suites | Useful existing coverage; inspect and extend rather than rewrite wholesale. |
| SST, manifest, vector search | Not present in the inspected source set | Future stages below, not existing guarantees. |

At that historical baseline, DELETE did not share SET's rotation path and
returned true even for missing keys; recovery built only one capacity-limited
table and did not replace the whole frozen-table set; post-WAL apply failure
did not put the Engine into an explicit unhealthy state. These were starting
points for expected-behavior tests, not authorization for agent fixes. They are
not the current gap list; see the 2026-10-23 review in section 8 and
[Engine architecture](arch/engine.md) for current behavior and limitations.

### Scope and stop lines

The main path delivers durable record/vector CRUD, exact-match and ordered
metadata indexes, exact and approximate filtered vector search, rebuildable HNSW,
and then storage reclamation, background flush, and published evidence. Query
indexes have teaching value and do not require a measured bottleneck to enter
the series. Physical tuning still requires measurements.

Start with serialized writes and ordinary locks. Keep the existing map-backed
memtable work as the teaching baseline, then replace its storage with a simple
skip list in M02-S1 before the small SST format and synchronous flush.
Add one flush worker and simple full compaction
only after the core indexes work. Keep existing package names.
Add `db/sstable`, `db/manifest`, `vector`, or `index/hnsw` when the
corresponding concept arrives; do not reorganize the repository in advance.

Out of scope for the main path: replication, consensus, sharding across machines,
SQL, authentication, multi-statement transactions, public historical snapshots,
lock-free structures, custom allocators, SIMD/assembly, GPU execution, product
quantization, DiskANN, and cloud deployment. Embedding generation remains outside
the database; clients provide vectors.

Until the core indexes work, accept blocking flushes, sequential SST lookup,
full-sort exact top-k, retained WAL segments, accumulating SSTs, and rebuilding
derived indexes on open. Use a documented small dataset and finite demo run;
this intermediate database does not claim bounded disk growth or fast startup.
Memory budgets and safe input bounds still apply. Synchronous flushing prevents
unbounded frozen-table accumulation once M04 exists.

Product checkpoints, each demonstrated before proceeding:

- After M04: SET/GET/DELETE through memtables and SSTs, including restart;
  the first complete durable disk read/write cycle.
- After M12: persistent vector CRUD and exact filtered search over the wire.
- After I01/I02 and M15: exact-match, range, and integrated approximate vector
  indexes, with correct updates/deletes and rebuild on restart. Show this working
  database before starting compaction or throughput work.
- After M17: the maintained database, with WAL reclamation, compaction,
  background flush, and reproducible correctness/performance evidence.

Full-text search and its fuzzy-matching extension are optional follow-on chapters;
geospatial indexing is another optional direction. They do not delay the core
checkpoints. Section 7 records their order and concrete first-version limits.

## 3. Global behavior contracts

These apply when the relevant feature is introduced.

### Writes, errors, and versions

- Serialize mutations initially. Successful writes become visible before the
  response. Concurrent calls may serialize in either order; completed
  non-overlapping calls respect real-time order.
- Validate deterministic failures before WAL append: input format, size limits,
  collection existence/dimension, finite vectors, and option bounds.
- WAL append assigns a globally increasing positive LSN and syncs before success.
  Gaps are allowed; strict increase, not consecutive numbering, is the contract.
  Never reuse an LSN after restart or checkpoint.
- Validate capacity -> rotate memory if needed -> append and sync -> apply ->
  acknowledge. Rotation before append is accepted, including when encoding or
  append later fails, provided no rejected mutation becomes logically visible.
  One successful mutation appends once and applies its assigned LSN. Recovery
  uses the same capacity path with the recovered LSN and no new WAL append.
- A WAL I/O/sync error can have an uncertain outcome: the record might survive
  restart. A post-WAL apply failure also has an uncertain outcome. Current M02
  returns the error but has no Engine unhealthy latch; the caller must stop
  normal service and reopen for reconciliation. WAL unhealthy/closed states
  still reject writes themselves. M04/M08 introduce automatic rejection for
  ambiguous storage publication. Do not promise rollback or exactly-once retries.
- A validation rejection has no logical mutation effect and appends no WAL
  record. Oversized records must fail before rotation; a later encoding or WAL
  rejection may leave an extra frozen table under the accepted rotation policy.
- Visibility uses the highest LSN for each key. Equal-LSN copies introduced by
  flush/recovery must agree in contents; conflicting copies are corruption.
- Missing GET returns the existing missing-key error. DELETE unconditionally
  writes a fresh tombstone, including for missing/already-deleted keys. Its bool
  means the tombstone was successfully recorded/applied, not that a live key
  existed. Repeated deletes consume WAL/memory and may rotate. The current RESP
  adapter consequently returns `:1` for a successful DEL even on a missing key;
  this is not Redis deletion-count compatibility.
- Caller-owned and returned slices/maps must not alias mutable database state.
  Empty byte keys and non-nil empty values are valid at the KV API; nil KV input
  slices are invalid. Vector IDs have their own nonempty requirement.

### Durability, recovery, and resource bounds

- Before checkpoints, WAL holds the durable history. Afterwards, the committed
  manifest-selected SST set plus the retained WAL suffix hold authoritative
  state. ANN files are always derived data.
- Reject corrupt complete records, unsupported versions, invalid lengths and
  out-of-order LSNs. Only an incomplete final WAL record in the latest segment
  is an allowed torn tail. CRC errors are not silently treated as torn tails.
- M02 recovery clears the current memory view and replays directly into it;
  failed replay can leave a partial prefix visible to direct Engine callers.
  This is an explicit simplification, not rollback or atomic replacement.
  The server must refuse admission on recovery error, as `runServer` already
  does. M04's manifest/SST recovery retains the stronger contract of no public
  service until successful validation; partial memory is never a successful open.
- The supported crash model is a single process on a local filesystem with
  functioning file/directory sync and same-filesystem atomic rename. State the
  tested OS/filesystem. A process-kill test does not prove survival of power loss
  or broken hardware.
- Bound all disk/network lengths before allocation and check arithmetic overflow.
  Publish configured record, dimension, queue, and query limits. Memtable
  accounting is a logical byte budget, not an exact Go heap measurement.
- Only one database owner holds the directory lock. Close stops admission,
  waits for active work, releases resources, and is idempotent at the Engine API.
  Operations after close return a defined closed error. Current M02 supports
  tested sequential close behavior; concurrent Engine close coordination is a
  known implementation gap (section 8), not established by the current race run.
- Start with blocking maintenance under a lock where necessary. Do not introduce
  concurrency without tests for publication, cancellation, and resource lifetime.

### Reads and search

- A tombstone is a found record that stops lookup. Never turn it into “absent in
  this layer, try older layers.”
- Point reads, scans, and search must agree on live IDs and latest versions.
- Each scan/search observes one coherent state. Initially hold a read lock
  through the operation; writers may wait. This does not require MVCC.
- Exact search returns the true top-k of eligible live records. Approximate
  search may miss better neighbors, but never returns deleted, stale, duplicate,
  wrong-collection, or filter-ineligible records.
- For all vector metrics expose a lower-is-better `distance`. Sort by distance,
  then ID in bytewise ascending order for ties. Approximation affects candidate
  discovery, not the metric used to score returned records.

## 4. Roadmap and video structure

### Active application path (2026-11-13)

The owner now prioritizes a demonstrable storage engine and implemented vector
index for database-role applications. Follow [TASKS.md](TASKS.md) sequentially:
bounded M02 lifecycle work -> M03 -> M04 -> a full-scan M07 slice -> single-
collection squared-L2 vector storage (M09/M10 slices) -> M11 -> M13/M14 -> M15 ->
application evidence (M17 slice). Teaching and implementation order remain equal.

Defer M02-S1's skip list, M12 metadata filtering, I01/I02 secondary indexes, and
maintenance until after that checkpoint. Keep the map-backed memtable and rebuild
HNSW on startup. Existing work remains included. Partial-stage slices do not
claim the full collection/metric/filter or maintained-release contracts are done.
The broader table and chapters below retain the longer-term curriculum; this
active path supersedes their ordering where they conflict. Preserve durability,
version/tombstone correctness, bounded decoding, and the existing ANN release
target; no performance tuning is required to advance through the first versions.

The table and chapter order below describe the broader curriculum; the active
application path above takes priority for current implementation and teaching.
Existing M IDs and test IDs remain stable for references; their numeric order is
no longer the schedule. I IDs add query-index chapters without renaming existing
work. Each row may span several focused commits/videos. Optional work is skipped
unless selected, with its limitation documented.

| Order | Stage | New question / stopping point | Prerequisites | Status |
| --- | --- | --- | --- | --- |
| 1 | M00 | What do the existing TCP/RESP and WAL layers guarantee? | Existing code | Included; audit needed |
| 2 | M01 | Can a simple memtable safely apply, freeze, and export records? | M00 | Included; current tests pass, completion evidence pending |
| 3 | M02 | Do rotation, deletes, and recovery preserve one logical view? Finish with the M02-S1 skip-list replacement. | M01 | Existing suites pass (2026-10-23); review limits remain; skip list planned |
| 4 | M03 | Can we write and read a simple sorted file? | M02 | Planned |
| 5 | M04 | Can we flush, read from disk, and restart safely? | M03 | Planned; first disk cycle |
| 6 | M07 | Can we scan current records across unmerged storage layers? | M04 | Planned |
| 7 | M09 | Can we persist collections, vectors, and typed metadata? | M07 | Planned |
| 8 | M10 | What do our vector distances mean? | M09 | Planned |
| 9 | M11 | Can we return correct exact top-k with a simple baseline? | M10 | Planned |
| 10 | M12 | Can clients query with metadata filters? | M11 | Planned; exact search demo |
| 11 | I01 | How does a field value lead directly to matching record IDs? | M12 | Planned; exact-match index |
| 12 | I02 | How does ordered encoding enable range and prefix queries? | I01 | Planned; ordered index |
| 13 | M13 | How does graph traversal reduce vector comparisons? | I02; M11 metrics/oracle | Planned |
| 14 | M14 | How does HNSW build and search a layered graph? | M13 | Planned |
| 15 | M15 | Do ANN queries stay valid after updates, deletes, and restart? | M14 | Planned; indexed database demo |
| 16 | M05 | Can we reclaim WAL history now that SSTs are authoritative? | M04; after M15 in series | Planned |
| 17 | M06 | Can we merge redundant SSTs without changing query results? | M05; M07 | Planned |
| 18 | M08 | Can bounded background flush reduce write stalls? | M06 | Planned |
| 19 | M16 | Does startup cost justify saving derived graph snapshots? | M15; M08 in series | Optional; measure first |
| 20 | M17 | What can the completed database honestly claim? | M08; M16 decision | Planned |

This changes the old M06 -> M07 dependency: logical scans merge versions without
rewriting files. M05/M06/M08 are later responses to visible storage limitations,
not prerequisites for record schemas or indexes. Stage-local tests introduce
flush, pruning, compaction, and worker interactions only when those features exist.

For each video: show a limitation using the previous commit, introduce one
invariant, draw the read/write path, walk the relevant committed code, run one
small deterministic test/demo, and state the remaining limitation. Tag recording
commits only after tests pass. Do not present red teaching tests as a working
release. Avoid reading every source line or promising features before they exist.

## M00 — Lock down the existing foundation

**Why now:** WAL is already implemented. Confirm its boundary rather than rebuild
it or add another WAL policy before memtables work end to end.

**Behavior and implementation scope**

Document WAL startup as open/locked -> recovery required -> ready -> unhealthy or
closed. Replay succeeds once per WAL instance; retrying a failed callback starts
from the beginning. The callback owns resetting its destination; current Engine
recovery clears and replays in place rather than publishing staging state.
Keep per-write sync as the only
durability mode in the main path. Retain the existing 18-byte V1 WAL header and
the distinct argument framing in `utils/encoding.go`; do not conflate them.

Preserve the thin RESP adapter: binary-safe arrays/bulk strings, PING/GET/SET/DEL,
bounded lengths, deterministic command/arity errors. Unknown commands must not
silently execute or look successful. No Redis compatibility claim.

**Acceptance tests**

- M00-T1: append SET/DELETE, close/reopen, replay ordered contents, append again,
  reopen again; the new LSN exceeds every recovered LSN.
- M00-T2: writes before recovery and after unhealthy/closed states fail; a second
  successful Replay is rejected; callback failure permits a clean retry.
- M00-T3: partial final header/body is truncated; the same truncation in an older
  segment fails; complete bad CRC/version/op/LSN order fails.
- M00-T4: short write and sync failure are reported and disable further writes;
  exact size boundary and rotation preserve all prior records.
- M00-T5: excessive disk/network lengths fail without excessive allocation;
  existing directory ownership fails promptly; close releases the lock.
- M00-T6: fragmented and pipelined RESP commands retain framing; invalid command,
  wrong arity, empty and binary payloads, and EOF behavior have defined results.
  Use `net.Pipe` where a real listener adds no value.

**Video proof:** kill after an acknowledged write, reopen, and inspect it.
Explain why a failed response may still correspond to a persisted write.

**Done/defer:** existing tests cover the matrix or the uncovered cases are added
and passing. Defer group commit, asynchronous acknowledgments, and replication.

## M01 — Finish the memtable contract

**Behavior and implementation scope**

The original baseline uses a map plus sorting on export; preserve that work in
the series. M02-S1 replaces the representation while retaining these contracts.
Apply replaces the latest record for a key,
including tombstones. Input LSN must exceed the table's last accepted LSN, even
for a different key. A rejected Apply changes neither data, size, nor last LSN.
This is an ordered-replay contract, not arbitrary out-of-order ingestion.

Logical size is `8 + 1 + len(key) + len(value)` per live stored record; tombstones
carry no value bytes. Replacement subtracts the old size before adding the new.
A nonzero maximum is required; exact capacity fits. Oversized records are
rejected, including in an empty table. Engine prevalidation comes in M02.

Current capacity preflight is `CanAccomodate(Record) error`: nil means it fits,
`ErrRecordDoesntFit` means rotate, and `ErrRecordTooLarge` means reject because
even an empty table cannot fit it. Apply independently rejects total-capacity
overflow with `ErrMemTableMaxSize` and oversized records with `ErrRecordTooLarge`.

Freeze is idempotent and leaves reads/export available. Apply and Clear on a
frozen table fail. Clear on a mutable table resets data, size, and LSN.
Get/export return independent records, sorted export is bytewise by key, and
tombstones remain present for flushing.

**Acceptance tests**

- M01-T1: insert/overwrite/delete marker; stale and equal LSN rejected; a greater
  LSN works after a rejected attempt.
- M01-T2: exact capacity succeeds, one byte over fails without mutation; larger
  and smaller replacements update capacity correctly; tombstones consume space.
- M01-T3: oversized single record and invalid zero capacity have defined errors;
  size arithmetic does not wrap.
- M01-T4: mutate input and returned slices, including sorted export; later reads
  and size behavior remain unchanged.
- M01-T5: freeze twice, read/export, reject Apply/Clear; mutable Clear permits a
  fresh ordered sequence.
- M01-T6: empty table, empty key/value, binary keys, sorted tombstones, and
  concurrent reads/Apply/Freeze obey the locking contract under the race detector.

**Video proof:** fill a tiny table, show a rejected write leaves the old value,
then freeze it and show that its sorted contents cannot change.

**Done/defer:** all invariants above are proven. The bounded skip-list replacement
is scheduled in M02-S1 for ordered traversal and teaching coverage. Defer arenas,
lock-free access, and further memtable tuning.

## M02 — Rotation, layered reads, and whole-engine recovery

**Behavior and implementation scope**

Use one mutation path for SET and unconditional DELETE. Before rotation/append,
ensure the record can fit an empty table. `CanAccomodate` distinguishes oversized
rejection from a full current table. If the current table lacks room, freeze it
and create a fresh table before appending; retain the frozen table for reads even
if append fails. Encode SET as key/value and DELETE as key-only with the matching
WAL op, append once, then apply its assigned LSN. WAL also enforces its payload
limit; a rejected encoding/append must not publish the requested logical mutation.

Read active then frozen newest-to-oldest. Across future SSTs, explicitly compare
versions where layer ordering alone is insufficient. DELETE does not check
existence; it appends/applies a new tombstone and returns true on success. Reads
respect it immediately. Compaction later reconciles redundant versions.

Owner-approved simplification: Recover discards frozen references, clears the
current mutable table, and replays into the Engine through `apply(..., skipWal=true)`.
It uses the same capacity transition without writing new WAL records, preserving
recovered LSNs. On error, the old view is not restored and replayed prefix records
may remain readable. The current implementation sets `closed=false` on return,
including on error; this does not imply recovery succeeded or WAL permits writes.
For this intermediate stage recovery may retain multiple frozen tables in RAM;
M04/M08 remove that unbounded-history limitation. Normal durable startup still
requires a newly opened WAL, successful Recover, then client admission. A caller
must inspect the error. `runServer` aborts before listening on recovery failure.
DurableWal permits one successful Replay per instance and rejects replay after
Close. Recover alone cannot restart a closed durable Engine or safely refresh an
already-recovered one: it clears memory before Replay rejects the call. Restart
uses a newly opened WAL and a new Engine. See `arch/engine.md` for this distinction
and remaining lifecycle limits.

If apply fails after WAL success, return an uncertain-outcome error; reopen can
replay the durable record. The removed unhealthy-latch test is no longer an M02
acceptance requirement; no automatic Engine fail-stop is currently implemented.
Recovering with a smaller record limit returns an error, potentially after
building a readable prefix. Do not describe that prefix as a successful database
open. M04 keeps public admission closed until its recovery succeeds.

**Acceptance tests**

- M02-T1: SET causing rotation remains visible; older frozen keys are readable;
  newest overwrite wins across at least three tables.
- M02-T2: DELETE of a frozen key, DELETE that itself triggers rotation, repeated
  DELETE, absent DELETE, and reinsert-after-delete have correct results. Every
  successful DELETE writes a fresh tombstone, including absent/repeated deletes.
- M02-T3: a frozen tombstone masks an older frozen value; later SST integration
  repeats this case against disk.
- M02-T4: capacity rotation makes exactly one WAL append and preserves its LSN;
  oversized input appends nothing and creates no cascade of empty tables.
- M02-T5: append failure preserves logical contents and publishes no requested
  record, but may leave an extra frozen table. Successful append followed by apply
  failure reports an error; a new durable Engine recovers that record on reopen.
  Automatic rejection of subsequent operations is not claimed at M02.
- M02-T6: replay larger than one memtable, including overwrites and deletes,
  reconstructs the same view without extra WAL writes. Failed replay returns an
  error and may retain a readable prefix; the server must not start from it.
- M02-T7: successful rebuild removes obsolete frozen state; sequential repeated
  close succeeds and post-close SET/GET/DELETE fail. Concurrent close safety and
  closed-instance restart are not established by the current tests.

**Video proof:** use a tiny budget to show three generations and one tombstone.
Restart with history bigger than the budget and obtain identical reads.

**Done/defer:** both mutation kinds and replay share the same visibility rules.
Complete the bounded M02-S1 representation change below, then move to M03.
No background worker yet.

### M02-S1 — Keep the memtable ordered with a skip list

Owner decision on 2026-10-18: replace map-plus-export-sort with a skip list before
SST work. Finish the existing M02 Engine correctness slices first so storage
representation changes have a working integration baseline. This is one bounded
teaching substep; it does not start an open-ended memtable optimization chapter.

Keep `BasicMemTable`, its public API, the existing `RWMutex`, bytewise key order,
one latest record per key, and all M01 capacity/LSN/freeze/copy contracts. Replace
the internal map with an ordered skip list. Apply finds and replaces an existing
key or inserts a new node; a tombstone remains a record in that node. Get searches
the list. `GetAllSorted` walks the bottom level and returns independent records
without a sorting pass. Preserve the existing logical record-byte accounting;
node/link allocation is additional heap overhead, not a changed capacity contract.

Use a documented bounded maximum height and geometric random height selection,
with deterministic seeds for structural tests. Keep random choices and mutations
under the existing lock. Expected lookup/update/insert cost is O(log N) within
the intended workload; sorted export is O(N) node visits plus copying. The API
still allocates the exported slice; streaming iteration is a separate future
decision. No concurrent skip-list algorithm, arena, or secondary lookup map.

**Acceptance and stopping point**

- Reuse all existing M01 and M02 behavior tests with the real `BasicMemTable`.
  The storage replacement must not change capacity rejection, LSN advancement,
  tombstone visibility, freeze, aliasing, or Engine recovery behavior.
- Add focused tests for increasing/decreasing/random insertion order, head/tail
  and missing-key lookup, replacement without duplicate nodes, empty/binary keys,
  and Clear/reuse. Compare seeded operations with a simple map-and-sort oracle.
- Check sorted links, height bounds, and reachability of every bottom-level node
  across fixed seeds. Rejected Apply must leave the logical state unchanged.
- Run focused tests and relevant race checks; demonstrate out-of-order writes
  exporting in order through the skip list. No speed target gates completion.

**Video proof:** contrast the already-built map/export-sort with an ordered list
and its shortcut levels, then export the same records in order. Once the existing
contracts and structural checks pass, stop and implement the minimal SST.

## M03 — A minimal SST file writer and reader

**Behavior and implementation scope**

Build the file component independently before installing it into Engine. An SST
contains unique bytewise-sorted keys with latest record LSN/value/tombstone.
Reject unsorted or duplicate input; merging belongs elsewhere.

V1 design: magic/version header, bounded length-prefixed sorted records with
checksums, and a validated completion footer with record count and checksum
coverage sufficient to detect truncated or changed contents. Use explicit
fixed-width integers and a documented endianness; never serialize Go structs
by memory layout. Before encoding, the owner records exact widths, checksum
coverage, and offsets in `arch/` and links it here; agents then add independent
golden-byte fixtures. The reader validates complete files before serving them.

Point lookup may scan sequentially and stop when it passes the requested key.
Iteration walks records in order, reports corruption, and closes resources.
A missing key and a found tombstone are distinct results. Writers produce
temporary output and never publish partial files. Blocks and a sparse index
are a later measured extension with explicit format compatibility; they are
not necessary to complete the first disk read/write cycle.

**Acceptance tests**

- M03-T1: round-trip empty/single/multiple-record files, binary keys, empty values,
  tombstones, and LSN extremes within the supported domain.
- M03-T2: first/last/interior keys and misses below/above/between keys; a record
  at the permitted size limit is readable.
- M03-T3: reject unsorted/duplicate input and illegal tombstone payloads.
- M03-T4: truncate header/record/footer, flip bytes, corrupt record counts,
  overflow lengths, unsupported versions; return errors without panics/OOM.
- M03-T5: short writes and close/sync errors abort output; no complete-looking
  published file appears.
- M03-T6: golden bytes independently validate the layout; lookup returns owned
  bytes; iteration ordering and iterator error/close behavior are deterministic.

**Video proof:** inspect a small file with a hex dump, read one value and one
tombstone, and show that an incomplete file is rejected.

**Done/defer:** standalone format and sequential reader/writer proven. Defer
sparse indexes, compression, Bloom filters, mmap, and block caches. Engine file
publication is the next stage, not another standalone file optimization.

## M04 — Manifest-backed synchronous flush

**Behavior and implementation scope**

Introduce the catalog before trusting SSTs or deleting history. Choose a small,
checksummed, versioned manifest snapshot at a fixed path `MANIFEST`, replaced
atomically. It records the database identity, generation, live SST descriptors,
durable LSN watermark, and file-ID allocation state. Rewriting a small catalog
is enough; a manifest journal/CURRENT hierarchy is not required.

Flush frozen tables oldest-first so a watermark always represents a contiguous
prefix of applied history. Under the initial maintenance lock:

1. Write a uniquely named temporary SST; sync and close it.
2. Rename to its immutable final name; sync the containing directory.
3. Write/sync/close a complete replacement manifest to a temporary name.
4. Atomically replace MANIFEST and sync its directory.
5. Publish the new in-memory file set; only then release the frozen table.

Never overwrite an existing final SST. Choose new IDs above both catalog state
and recognized existing filenames to avoid orphan collisions after a crash.
Only manifest-listed tables are live; directory scanning does not resurrect
orphan outputs. Missing/corrupt listed tables cause open to fail. Missing manifest
is accepted only for an explicitly recognized pre-manifest WAL-only database
(or an empty new database); a populated SST database missing its catalog fails.

Initially retain every WAL segment. Open validates the catalog and tables,
replays only records beyond its durable watermark into staging state, and
initializes the next LSN above both catalog and WAL history. Raw WAL decoding may
still scan old retained records to validate them.

If recovery exceeds its RAM budget, synchronously install complete recovered
prefixes using the same protocol while keeping the public Engine unavailable.
A later replay error can leave durable valid-prefix progress, but exposes no
partial public state; a retry starts from the new committed checkpoint.
This prevents arbitrarily large history from requiring arbitrarily large RAM.

**Acceptance tests**

- M04-T1: active/frozen/SST precedence, delete masking, flush then reopen with
  WAL retained; no duplicate logical entries.
- M04-T2: interrupt each numbered publication boundary, reopen, and observe a
  recoverable committed file set with every acknowledged mutation preserved.
- M04-T3: orphan temp/final SST ignored; missing/corrupt live SST or manifest
  fails open; directory contents alone cannot select a new state.
- M04-T4: failures keep needed memory/WAL state; ambiguous manifest publication
  makes the Engine unhealthy until reopen.
- M04-T5: multiple flushes advance only contiguous durable coverage; empty flush
  is a no-op; table IDs are not reused after orphan-producing crashes.
- M04-T6: replay beyond the budget spills complete prefixes; retry after failure
  preserves the correct view; restart resumes LSN allocation above the watermark.

**Video proof:** SET, overwrite, DELETE, flush, read a key that exists only in an
SST, close/reopen, and read the same logical contents through the existing KV
adapter. Prove a flushed key is absent from active/frozen memory and covered WAL
is not reapplied. Also stop after the SST rename but before catalog publication:
the orphan exists, yet recovery correctly ignores it and uses WAL.

**Done/defer:** the first durable disk read/write cycle works and flushed state
survives catalog transitions. Continue to M07 scans, then records and indexes.
Retain all WAL and SST history until M05/M06; flush synchronously until M08.

## M07 — Ordered scans and a coherent read view

**Behavior and implementation scope**

Add an internal iterator or callback scan over a half-open key range
`[start, end)`, with an explicit unbounded endpoint representation. Merge active,
frozen, and SST records; resolve duplicates by LSN and suppress tombstones.
Support prefix scans using correct byte ordering, including all-0xff prefixes.

Start by holding an Engine read lock for the scan; avoid callbacks that reenter
mutating APIs while the lock is held. Cancellation, early stop, and errors must
release every iterator and lock. Search will build on this contract. Public
long-lived snapshots and transactions are not needed.

**Acceptance tests**

- M07-T1: range bounds, empty/unbounded ranges, binary prefixes, sorted output.
- M07-T2: overlapping layers return each live key once at its latest value;
  tombstones and reinserts agree with GET.
- M07-T3: cancellation, early stop, iterator corruption, and repeated close
  release resources; a subsequent writer can complete.
- M07-T4: use channels/barriers to start a write during a scan; results reflect
  one complete view, never a mixture, under race testing.
- M07-T5: compare randomized SET/DELETE/flush/reopen/scan sequences with a simple
  reference map. Add compact operations when M06 arrives later.

**Video proof:** enumerate a collection whose records span every storage layer.

**Done/defer:** one consistent source for future exact search, without needing
compaction. No MVCC or retained snapshot versions.

## M09 — Durable collections and vector records

**Behavior and implementation scope**

Add a Go vector API over the KV engine first: create/list/get collection,
upsert/get/delete record. Proposed collection fields are a nonempty name,
positive fixed dimension, and immutable metric. Support `l2_squared` and
`cosine`; default to cosine for the eventual embedding demo. Changing dimension
or metric requires a new collection. Collection deletion/rename is deferred.

Represent a record as nonempty string ID, float32 vector, and flat typed scalar
metadata. Store the entire record under one collision-free namespaced KV key so
vector and metadata update atomically with one existing SET. Collection
definitions live under a separate reserved prefix. Length-prefix key components
or otherwise prove encoding injectivity; never join arbitrary IDs with an
unescaped delimiter. Raw external KV commands must not modify this reserved
namespace.

Version the value codec; specify float32 IEEE-754 encoding, endianness, lengths,
and deterministic metadata-key ordering before golden tests. Bound dimension,
ID length, metadata size, and total encoded bytes to fit Engine/WAL limits.
Retain the caller's original vector in storage; normalized representations are
derived for cosine search. Reject NaN/Inf and cosine zero vectors.

Create with identical schema is idempotent; conflicting schema is an error.
Upsert replaces the full vector and metadata. Missing collection differs from
missing ID. Validate everything before append. The first vector API can serialize
at a service lock around validation and mutation; no write batch is needed.

**Acceptance tests**

- M09-T1: create, duplicate identical/conflicting create, list, restart, schema
  recovery; wrong dimension and invalid metric reject without WAL mutation.
- M09-T2: round-trip vectors and typed metadata, independent slices/maps, full
  replacement removing old metadata, delete missing/live and reinsert.
- M09-T3: invalid IDs, dimensions, NaN/Inf, cosine zero vector, oversized payload,
  and namespace collisions; raw KV cannot overwrite collection/vector keys.
- M09-T4: corrupt/truncated/unsupported codecs fail; golden bytes and fuzzing
  cover length arithmetic and metadata types.
- M09-T5: crash/reopen and flush preserve collection schema and the latest
  complete vector/metadata pair. Repeat after WAL pruning and compaction when
  M05/M06 introduce them.

**Video proof:** persist a few hand-written 2D vectors and labeled records.
Explain embeddings without adding an embedding API dependency.

**Done/defer:** durable vector CRUD exists. No metadata secondary index, graph,
bulk transaction, or per-record patch operation.

## M10 — Distance functions and numerical contracts

**Behavior and implementation scope**

Define `l2_squared(q,x) = sum((q_i-x_i)^2)`.
Define `cosine_distance(q,x) = 1 - dot(q,x)/(norm(q)*norm(x))`, clamping rounding
drift to [0,2]. Reject cosine zero queries as well as zero stored vectors.
For finite float32 inputs accumulate in float64 and return float64 distance.
Use consistent normalization/scoring for exact and graph paths.

The squared-L2 convention and normalization relationship are also described in
the [Faiss metric documentation](https://github.com/facebookresearch/faiss/wiki/MetricType-and-distances).
These formulas define this project's behavior; approximate search will not
change their meaning.

**Acceptance tests**

- M10-T1: L2 for [0,0] and [3,4] is 25; identical vectors have distance zero.
- M10-T2: cosine identical positive-direction vectors give 0, orthogonal give 1,
  opposite give 2; scaling by a positive constant preserves distance.
- M10-T3: dimension mismatch, nonfinite inputs, and cosine zero norms reject;
  zero vector remains legal for squared L2.
- M10-T4: large/small finite float32 components remain finite under the defined
  accumulator; roundoff comparisons use documented tolerances.
- M10-T5: symmetry and nonnegativity properties; do not assert the triangle
  inequality for squared L2 or cosine distance.

**Video proof:** plot the 2D examples and show that vector length changes L2
ranking but not cosine direction.

**Done/defer:** one shared metric contract. No inner-product metric or SIMD yet.

## M11 — Exact top-k search and its performance baseline

**Behavior and implementation scope**

Search the coherent collection scan. Reject negative k; k=0 returns empty after
request validation; k larger than the live count returns all live records.
Return ID and distance, with optional copied metadata. Empty collection returns
empty. Query vectors obey the collection schema.

First use a straightforward full-sort implementation ordered by (distance, ID),
with independent hand-computed cases and a simple reference in tests. For N live
vectors of dimension d, this baseline costs O(N*d + N*log(N)) work and O(N)
selection memory for positive k. State and bound the demo workload accordingly.
A bounded max-heap is a later measured improvement: O(N*d + N*log(k)) work and
O(k) selection memory, excluding the underlying scan/view. It is not a gate for
moving to filters or indexes. HNSW's own frontier heaps remain part of its algorithm.

Retain this exact path permanently as the correctness oracle and fallback.
Measure decoding and allocations before adding a derived flat vector cache.
If decoding dominates, add a packed float32 array plus ID/version mapping as a
separate optional substep; it must rebuild from authoritative records and publish
atomically with mutation visibility.

**Acceptance tests**

- M11-T1: hand-computed nearest IDs/distances; ties use ID order; k=0/1/N/>N,
  negative k, empty collection, bad query, and cancellation.
- M11-T2: latest updated vector changes rank; deleted ID disappears; reinsert
  appears once, across rotation/flush/reopen. Add compaction coverage in M06.
- M11-T3: randomized small datasets agree with independent full-sort reference
  for both metrics and tie-heavy data.
- M11-T4: mutate query/results after return; stored state remains unchanged;
  concurrent writes cannot create a mixed search view.
- M11-T5: benchmarks separate setup from search and report allocations and
  distance evaluations; no machine-dependent speed threshold in unit tests.

**Video proof:** search the same query before and after moving/deleting one point.
Show the scored records, deterministic sorting, and the correctness comparison.

**Done/defer:** exact search is correct and measured. Record baseline before ANN.

## M12 — Metadata filters and the first usable vector database

**Behavior and implementation scope**

Start with conjunctions of predicates on flat scalar metadata: equality for
string/bool/finite number, numeric comparisons, and explicit exists.
Missing keys fail comparisons; exists distinguishes absence. Types do not coerce
("1" differs from 1). Null, arrays, nested paths, OR, and NOT are deferred.
Reject invalid predicates before scanning.

Exact search applies the filter before top-k selection, so it returns the best
k among eligible records. Selecting global top-k then filtering is incorrect.
Metadata and vector are read from the same latest record version.

Expose a thin vector command adapter only after the Go API works. Prefer the
existing RESP transport with a documented versioned JSON request inside bulk
strings for vector operations, rather than a second server stack. Specify exact
command names, request fields, numeric limits, and error mapping before writing
adapter tests. Keep legacy KV commands and vector namespaces separated.

**Acceptance tests**

- M12-T1: each operator, type mismatch, missing field, exists, empty filter,
  multiple AND clauses, invalid syntax/types, and finite numeric bounds.
- M12-T2: nearest global records fail the filter but farther qualifying ones
  fill k; none qualify; fewer than k qualify.
- M12-T3: overwrite removes obsolete metadata; delete/reinsert and restart keep
  filters consistent with GET and exact search.
- M12-T4: vector adapter preserves distances/IDs/errors, rejects malformed and
  oversized requests, and handles fragmented/pipelined frames.
- M12-T5: full end-to-end create/upsert/query/filter/update/delete/reopen demo
  exercises a real Engine and temp directory.

**Video proof:** search the nearest points with a category constraint and
demonstrate why filtering after top-k loses valid answers.

**Done/defer:** the exact-search product checkpoint is reached. Metadata scans
provide the oracle for I01/I02, which now follow for teaching coverage. A cost-based
query planner is deferred; simple explicit access paths are enough.

## I01 — Exact-match secondary indexes

**Why now:** the scan-based filter gives an independent correctness baseline.
Teach indexing by adding a new access path over the same durable records before
optimizing storage. These are non-unique indexes; duplicate field values are valid.

**Behavior and implementation scope**

Choose a small explicit set of indexed metadata fields in service configuration.
Build a derived mapping from (collection, field, scalar type, value) to live
record IDs. Strings, bools, and finite numbers follow M12's equality semantics;
numeric positive/negative zero compare equal, types do not coerce, and missing
fields have no value posting. Index keys must not collide across collections,
fields, types, or values. Preserve deterministic ID order in query results.

Start with in-memory posting sets rebuilt from the coherent LSM record scan on
open. LSM records remain authoritative; losing an index loses no records. This
first version avoids requiring durable multi-key transactions before teaching
index lookup. Explain the distinction between the SST's physical lookup index
and a secondary index mapping metadata values to record IDs.

Use the service lock already introduced for vector records. Update authoritative
records first, then remove old postings and add new postings before exposing
success. An index failure invalidates that access path; exact scan fallback must
return current records. An authoritative failure must not publish index changes.
Publish rebuilds as a complete unit. Queries fetch current records and recheck
predicates; rechecking alone cannot repair missing postings, so never trust an
incomplete index. Raw KV commands cannot bypass protected record namespaces.

For an AND filter, intersect supported equality postings, then evaluate remaining
predicates on those candidates. An unsupported predicate with no usable index
falls back to a full scan. Exact vector search scores every eligible candidate
before top-k; no heuristic candidate cap may silently make exact results partial.
Bound index memory; exceeding its budget invalidates it and uses the scan path.

**Acceptance tests**

- I01-T1: duplicate values, collection/field isolation, type distinctions,
  missing values, empty strings, signed zero, and deterministic IDs.
- I01-T2: metadata replacement removes old postings; delete/reinsert, vector-only
  update, and repeated update agree with the scan oracle.
- I01-T3: single and intersected equality predicates plus residual predicates
  return exactly the scan result and preserve exact vector top-k.
- I01-T4: reopen rebuild, failed index update, memory limit, and rebuild failure
  preserve current query results through explicit fallback.
- I01-T5: concurrent mutation/query sees one coherent view; flush/reopen does
  not change matches. Add pruning/compaction coverage when M05/M06 arrive.

**Video proof:** find all records in one category, change a record's category,
delete another, restart, and compare indexed results with the scan. Report
candidate counts; no latency target is required to move on.

**Done/defer:** exact-match lookup works through mutation and restart. Defer
compressed postings, persistence, uniqueness constraints, online schema changes,
and a cost-based planner. Persisting postings inside the LSM is a later extension
with an explicit atomicity/rebuild policy, not a hidden dependency here.

## I02 — Ordered secondary indexes and range queries

**Behavior and implementation scope**

Extend the derived-index lifecycle with sorted (typed value, record ID) entries
for configured numeric and string fields. A sorted slice and binary search are
sufficient initially; O(N) insert/remove is an accepted limitation. Reuse I01's
lock, invalidation, memory budget, rebuild, and scan fallback rules.

Teach how a tuple such as (collection, field, type, value, ID) can become a
lexicographically ordered LSM key. Specify an injective, order-preserving byte
encoding and independently test its ordering. Ordinary length-prefixed strings
are not automatically bytewise order-preserving. Finite signed numeric values
must sort by numeric order, including negatives and canonical zero. String order
is bytewise UTF-8 order; locale-aware collation and Unicode normalization are
deferred. Tie-break equal values by ID. The initial sorted index holds these
keys in memory; writing them durably into the LSM requires the later posting
materialization extension in section 7.

Support explicit inclusive/exclusive numeric and string bounds, unbounded ends,
and string prefix lookup. Validate bound types; reversed or equal-exclusive
bounds yield an empty range. Range lookup returns matching current IDs in
(value, ID) order; vector search still sorts by (distance, ID). Intersect a range
candidate set with equality candidates when requested, then recheck all residual
predicates against the same coherent record view. Do not cap exact candidates.

**Acceptance tests**

- I02-T1: encoded ordering agrees with logical ordering for negative/positive
  numbers, zero, finite extremes, variable-length/binary strings, and duplicate
  values; tuple component boundaries cannot collide.
- I02-T2: open/closed/unbounded bounds, empty/reversed ranges, absent fields,
  invalid types, prefixes, and prefix endpoint edge cases have defined results.
- I02-T3: moving a value across a range boundary, removing the field, deletion,
  and reinsert remove obsolete matches and agree with the scan oracle.
- I02-T4: equality plus range candidates preserve exact filtered top-k; index
  invalidation, reopen, and failed rebuild use a correct scan fallback.
- I02-T5: seeded randomized mutations and queries match a reference scan;
  add compaction regressions in M06.

**Video proof:** query a price interval and a name prefix, update a value across
the interval boundary, then combine category + price + vector search. Inspect
encoded keys to show why an LSM range scan can support the same access pattern.

**Done/defer:** ordered lookup and composition work. Proceed to graph search.
Defer balanced-tree implementations, compound-index selection, covering indexes,
durable posting materialization, and a general query planner.

## M13 — A single-layer graph search laboratory

**Behavior and implementation scope**

Implement a standalone in-memory graph over immutable test vectors, before
integrating database writes. Give nodes dense internal IDs and bounded neighbor
lists. Teach greedy movement first, then a search frontier, a bounded best-candidate
heap, and a visited set. Candidate exploration uses an ef budget.
This stage is a learning scaffold, not a claim to have implemented HNSW.

Keep distance/ID ordering shared with exact search. Empty graph is valid.
Seed all random choices in tests; never depend on map iteration order.
For direct search, validate ef >= max(1,k) when k>0; k=0 does no graph work.

**Acceptance tests**

- M13-T1: known small graph where greedy search misses but a wider frontier finds
  the nearest point; cycles terminate and nodes are not repeatedly expanded.
- M13-T2: empty/single-node graph, duplicate vectors, ties, disconnected components,
  and invalid entry/neighbor IDs have defined behavior.
- M13-T3: heap ordering, visited accounting, and candidate bounds are tested
  independently; output IDs are unique and scored correctly.
- M13-T4: for the same graph/query/seed, behavior is reproducible. Do not require
  arbitrary disconnected graphs to discover unreachable nodes.

**Video proof:** animate the visited nodes and compare their count with N.
Use a failure example to motivate better connectivity and hierarchy.

**Done/defer:** graph traversal is understandable and bounded. No persistence,
online mutation integration, or recall claims yet.

## M14 — HNSW construction and evaluation

**Behavior and implementation scope**

Extend the graph with sampled levels, a top entry point, greedy descent in upper
layers, and bounded candidate search at the base layer. Add incremental insertion,
neighbor selection with diversity, reciprocal links, and pruning to per-layer
degree limits. Document whether base-layer capacity is 2M and upper layers M.
Use a capped maximum level to bound pathological random draws.

Start experiments with M=16 and efConstruction=100; they are tuning starting
points, not universal optima. Expose efSearch and validate parameter bounds
(including efConstruction >= M). Correctly handle replacement of the entry point
when a new node reaches a higher level. HNSW is an approximate candidate index;
the [original HNSW paper](https://arxiv.org/abs/1603.09320) describes the layered
graph and neighbor-selection approach being studied here.

**Acceptance tests**

- M14-T1: fixed-seed level assignment and insertion order, valid entry point,
  layer membership, no self/duplicate edges, valid references, degree caps.
- M14-T2: first node, duplicate vectors with distinct IDs, clustered points,
  high-dimensional inputs, and new highest-level node.
- M14-T3: pruned links do not corrupt adjacency; all inserted nodes remain
  addressable even if a search fails to visit them.
- M14-T4: deterministic held-out-query recall evaluation versus M11 exact truth.
  Repeated builds with identical inputs/seed are reproducible.
- M14-T5: parameter validation, k edges, result uniqueness, finite distances,
  and memory/allocation benchmarks.

**Video proof:** sweep efSearch and plot recall against latency and distance
evaluations; explain the loss in recall rather than hiding it.

**Done/defer:** graph invariants pass and a reproducible recall/work baseline is
recorded. Proceed to mutation integration rather than tuning this chapter until
optimal. Section 6's release quality target is assessed in M17. Do not assert
every query is exact or that increasing ef guarantees strictly better recall on
every finite example.

## M15 — Integrate ANN with updates, deletes, and filters

**Behavior and implementation scope**

Use HNSW as a derived cache over durable records. Nodes carry (ID, record LSN).
Maintain a latest-version/live-ID map. A vector update inserts a new node; an old
node may remain as a traversal bridge but is not a result. Deletion removes result
eligibility without requiring immediate physical graph surgery.

Initial open rebuilds from current live records in deterministic ID order.
Serialize vector mutations and searches at a service lock initially. Append/apply
the authoritative record first, then update the derived index before success.
If the index update fails, invalidate it and route searches to exact mode; the
durable mutation can still succeed because exact visibility is available.
Do not confuse an index failure with an authoritative Engine apply failure.

For approximate queries, allow graph traversal through stale or filter-ineligible
nodes. Validate candidate versions, apply filters, deduplicate IDs, recompute
current distances, and rank. Increase candidate budget up to a documented cap.
If fewer than k valid results remain, use an exact filtered scan to fill/correct
the result (and discover when fewer than k eligible records exist); expose whether
fallback ran. Filtering may
reduce ANN recall even when k valid results are found; exact mode remains
available for guaranteed filtered top-k.

Bound stale-node growth: initial policy rebuilds when stale nodes exceed 25% of
total nodes or a configured index-memory budget is reached. A synchronous rebuild
under the service lock is acceptable first. Publish the new graph as one unit.

**Acceptance tests**

- M15-T1: move nearest vector far away, update it repeatedly, delete/reinsert the
  same ID; no stale distances, deleted results, or duplicate IDs.
- M15-T2: delete an entry-point record and traverse stale bridge nodes without
  returning them; all-deleted collection returns empty.
- M15-T3: filters exclude most graph neighbors; traversal still works and exact
  fallback returns eligible results when candidate search underfills.
- M15-T4: index update/build failure switches to exact search with latest data;
  authoritative write failure never updates the index as if committed.
- M15-T5: reopen rebuild, rebuild after stale threshold, concurrent query/mutation,
  and rebuild publication all preserve one coherent state under race testing.
- M15-T6: compare approximate results with live-record membership and filters,
  independently of statistical recall measurement.

**Video proof:** update and delete nodes live, showing stale nodes in the graph
but no stale results. Demonstrate a selective filter forcing exact fallback.

**Done/defer:** demonstrate the indexed database checkpoint with exact-match,
range, and ANN queries, mutations, and reopen. Now move to M05 WAL reclamation
and M06 compaction. Graph repair, lock-free mutation, and incremental disk graph
updates are deferred.

## M05 — Checkpoint and WAL reclamation

**Behavior and implementation scope**

The durable watermark C means all mutations through C are represented by the
committed SST view, including overwritten/deleted effects. It is not simply the
maximum LSN found in any completed file.

Delete a sealed WAL segment only when its maximum LSN is at most C and no active
recovery/reader requires it. Never delete the active segment; do not slice a
segment containing both covered and uncovered records. Commit C before deletion
and sync the WAL directory after cleanup. Cleanup failure leaks disk space but
does not invalidate a committed checkpoint.

On restart, seed LSN allocation from max(C, retained WAL maximum), even if no
record remains in old WAL files. Missing numeric segment IDs can be legitimate
after pruning; malformed data within retained files is still an error.

**Acceptance tests**

- M05-T1: segments entirely before C are reclaimable; mixed segments and the
  active segment remain; boundary LSN equal to C is covered.
- M05-T2: crash before checkpoint, after checkpoint, during deletion, and after
  deletion; reopen preserves latest values and tombstones.
- M05-T3: all old logs removed, reopen and append; LSN never resets.
- M05-T4: deletion fails or repeats after restart; database remains readable and
  cleanup is retryable.
- M05-T5: missing uncheckpointed required history is detected where metadata can
  establish the gap; do not promise detection of arbitrary manual file removal.
- M05-T6: repeat record, equality/range index, exact search, and ANN restart
  scenarios after pruning; rebuilds use live records, never require pruned WAL.

**Video proof:** show disk usage and replay work drop after checkpointing.

**Done/defer:** no acknowledged mutation depends on a deleted segment. Keep
retention simple; no archival WAL or online backup protocol.

## M06 — Simple, safe compaction

**Behavior and implementation scope**

Start with explicit full compaction of all currently committed SSTs under a
maintenance lock, later triggered by a small file-count threshold. Stream a
k-way key merge; choose greatest LSN for each key. Split output at a target size
so future compactions do not force one enormous file. Memory should scale with
input cursors and their bounded read buffers, not the entire dataset.

Keep tombstones by default. An optional tombstone-dropping substep is allowed
only for a full disk merge with no older versions outside the input set, no
historical snapshots, and a recovery checkpoint that prevents old WAL records
from being replayed. Do not use “this tombstone is old” as sufficient proof.

Publish all outputs and remove all input descriptors in one manifest replacement.
Only after durable publication and release of readers may old files be removed.
A failed pre-publication merge leaves the previous catalog usable. A newer
active-memory write remains newer than compacted disk contents.

**Acceptance tests**

- M06-T1: overlapping/disjoint inputs, many versions of a key, different file
  creation order than record LSN order, and equal-LSN duplicate copies.
- M06-T2: delete/reinsert/delete histories never resurrect a value; conflicting
  equal-LSN contents fail; retained tombstones survive reopen.
- M06-T3: output splitting at boundaries preserves sorted unique keys.
- M06-T4: interrupted output, manifest replacement, and old-file cleanup recover
  to valid contents; repeat compaction changes no logical results.
- M06-T5: active/frozen versions win over compacted older values; empty inputs
  and all-tombstone output are handled explicitly.
- M06-T6: if dropping tombstones is implemented, prove no excluded older source
  or replayed WAL can revive them.
- M06-T7: scans, collection schemas, complete vector/metadata pairs, equality and
  range matches, and exact/ANN eligibility agree before/after compaction and
  reopen. Activate the deferred M07/M09/M11/I01/I02 compaction regressions here.

**Video proof:** many overwrites create redundant files; merge them and compare
all GETs and disk bytes before/after. Include a deleted key.

**Done/defer:** bounded file growth at modest scale. State the write amplification
of full compaction. Leveled/tiered selection is an optional measured extension.

## M08 — Bounded background flush and maintenance

**Behavior and implementation scope**

Move flush I/O to one worker. Freeze/swap under the write lock, enqueue immutable
tables in order, build files outside it, then publish briefly under coordination.
One ordered maintenance coordinator serializes manifest changes and checkpoint
advancement. Compaction may still block writes initially.

Set a finite pending-byte/table budget. Reserve room before WAL append; when
full, block with cancellation or return a defined busy error before append.
Pick and document one admission policy before tests; the default is cancellable
blocking. A failed flush before catalog publication retains its readable table,
records the error, and stops new writes until repaired/reopened. Ambiguous catalog
publication instead makes the Engine unhealthy and stops normal reads as well;
do not silently drop the queue or continue from uncertain authoritative state.

Close stops admission, waits for in-flight acknowledged writes/maintenance,
closes handles, and reports errors. A final memtable flush is not necessary for
durability because WAL is synced. No goroutine continues using closed resources.

**Acceptance tests**

- M08-T1: hold the worker at a deterministic barrier; reads see frozen data and
  writes continue only up to the configured budget.
- M08-T2: full-queue cancellation returns without a WAL append; release the worker
  and blocked writers resume; shutdown unblocks waiters.
- M08-T3: pre-publication worker failure retains readable committed state and
  rejects new mutations; ambiguous catalog publication rejects normal reads too;
  restart restores every acknowledged write.
- M08-T4: simultaneous requested flush/compaction cannot lose a catalog edit or
  advance C past unflushed history.
- M08-T5: repeated close, close during flush, and concurrent reads run without
  races, leaked goroutines, or closed-file access.

**Video proof:** deliberately slow disk output, then show bounded queue growth
and explain backpressure. Measure write latency before/after background flush.

**Done/defer:** bounded maintenance with observable queue depth and errors.
Do not add multiple workers or complex scheduling yet.

## M16 — Persist derived index snapshots for faster startup

**Behavior and implementation scope**

Measure full rebuild time first. Persist only if it materially affects the demo
workload; otherwise document rebuilding as the completed simple policy and skip
this optimization.

Snapshot a coherent collection index at watermark S using a brief exclusive
service phase initially. Store magic/version, database and collection identity,
dimension/metric, graph parameters, S, node ID/version mappings, vectors or stable
references, topology, lengths, and checksums. Publish temp -> file sync -> rename
-> directory sync. Keep index publication independent of authoritative manifest
durability: missing, stale, or corrupt graph files cause rebuild, not data loss.

Simplest safe loader: use a snapshot only when its identity/schema and S match
the current authoritative mutation watermark exactly. Any mismatch triggers full
rebuild. This deliberately avoids a second recovery system. Loading an older
snapshot and replaying index deltas is a separate optional stage and requires a
retained change stream; do not assume pruned WAL is available.

**Acceptance tests**

- M16-T1: clean save/reopen uses the snapshot and preserves search scores and
  eligibility; record IDs and versions match authoritative data.
- M16-T2: crash at each publication boundary leaves either a usable file or a
  rebuild path; truncated/bad-checksum/unknown-version snapshots rebuild.
- M16-T3: database identity, collection, schema, parameter, or watermark mismatch
  cannot load the wrong graph.
- M16-T4: oversized counts, invalid edges, invalid entry point, and invalid vector
  lengths are rejected before unsafe allocation/access.
- M16-T5: a mutation after snapshot publication forces rebuild on restart,
  including when old WAL has already been reclaimed.

**Video proof:** compare cold rebuild time with a valid snapshot load, then
remove/corrupt the index and show the durable collection still works.

**Done/defer:** optional startup optimization has no authority over user data.
No independent graph WAL or delta-replay protocol.

## M17 — End-to-end evidence and a finished teaching release

**Behavior and implementation scope**

Publish a reproducible demo with a small local vector dataset, generator or
provenance/license, checksums, fixed queries, and command transcript. Include one
real embedding dataset when available, with model/version/dimension recorded;
network embedding calls are not part of tests.

Expose enough diagnostics to explain performance: active/pending bytes, SST and
WAL bytes, durable watermark, flush errors, compaction bytes, graph nodes/stale
ratio, query distance evaluations, and exact fallback count. A small stats API
or structured logs suffice; no observability platform is required.

Document durability/error semantics, query modes, record limits, memory footprint,
startup behavior, supported platform, and omissions. Label approximate results
and fallback behavior accurately in the demo.

**Acceptance tests**

- M17-T1: one deterministic integration scenario crosses insert, overwrite,
  rotation, flush, checkpoint, compaction, equality/range lookup, exact/ANN
  filtered search, delete, index rebuild, shutdown, and reopen.
- M17-T2: randomized reference-model runs for storage contents and exact search;
  verify ANN result validity separately from recall.
- M17-T3: crash harness runs publication boundaries using real subprocesses and
  explicit barriers; every acknowledged mutation survives reopening.
- M17-T4: decoder fuzz smoke runs, relevant race suite, file/goroutine cleanup,
  malformed request limits, and documented close behavior.
- M17-T5: repeatable benchmark report includes exact and ANN baselines, filters,
  mutation churn, and restart costs. Explain remaining bottlenecks.

**Video proof:** a complete search application demo, then a crash and recovery,
then a candid recall/latency/memory comparison.

**Done/defer:** stop when claims are supported and the code remains teachable.
Do not turn the final episode into an unlimited production-hardening backlog.

## 5. Cross-cutting test strategy

### Failure injection and crash matrix

Prefer real temp directories and real storage. Use narrow, owner-provided seams
for short writes, sync failure, rename failure, and deterministic worker barriers.
Agents may request such a seam but may not add it to production by default.
A file wrapper in a test is fine if the existing API accepts it.

For each persistence transition test interruptions before and after: WAL append,
WAL sync, memory apply, SST sync/rename, manifest sync/replace, checkpoint, and
obsolete-file removal. Observe state after reopen, not just that a mock Sync was
called. Error injection tests alone do not prove physical persistence ordering;
pair them with source review and subprocess interruption tests.

The recovery oracle is:

- All acknowledged mutations survive in their valid serial order.
- A mutation rejected before append never appears.
- A mutation interrupted after append but before acknowledgment may be absent or
  present; if present it is a complete record, never partial bytes/state.
- Deletes remain deletes and records from uncommitted output files do not
  override catalog/WAL state.
- Corruption returns a clear failure instead of a plausible partial database.

Use a controller subprocess to record acknowledgments and kill the worker at
explicit barriers. Clean Close/reopen is a restart test, not a crash test.

### Unit, property, integration, and performance separation

Use table-driven cases for semantic boundaries; a small map plus a full-sort
vector oracle for randomized tests; byte fuzzing for codecs; and real
Engine/WAL/SST integration for publication/recovery. Print seeds on randomized
failure. Channels, barriers, or injected clocks replace sleeps as ordering tools.

Tests assert error identity/category and state changes, not incidental full error
strings, private field layout, exact goroutine counts, or universal latency.
Golden bytes are appropriate for intentional persisted-format contracts.

Typical verification, adapted to the actual installed Go toolchain:

```sh
GOCACHE=/private/tmp/vectorized-gocache go test ./db ./core
GOCACHE=/private/tmp/vectorized-gocache go test ./...
GOCACHE=/private/tmp/vectorized-gocache go test -race ./...
```

Run focused packages while iterating; full tests and relevant race checks at a
milestone. Run a named fuzz target and selected benchmarks when those exist.
Format changed test files and run `git diff --check`. Never claim sandbox-blocked
network tests, missing-toolchain tests, or intentional compile failures passed.
No tests were run merely to edit this roadmap.

## 6. Performance and “good enough” acceptance

Record CPU, RAM, OS, Go version, storage medium, dataset source/seed, N, dimension,
k, metric, HNSW parameters, filter selectivity, and warm/cold state. Separate
ingest/build time from query time. Report ns/op or latency distribution,
allocations, throughput, retained memory, disk bytes, recall, and distance work
where relevant. Single-call microbenchmarks do not establish p99 service latency.

Use a laptop-sized progression: 1k, 10k, then 100k vectors where memory permits;
dimensions 32 for algorithm experiments and 128/384/768 for representative
workloads. These are benchmark scenarios, not capacity guarantees. Dense float32
vectors alone cost roughly 4*N*d bytes before graph links, metadata, maps,
duplicate versions, and temporary build space.

Exact ground truth uses the same live dataset, metric, filter, k, and ID tie
ordering. For each query with eligible records, recall@k is
`|approx IDs intersect exact IDs| / min(k, eligible count)`; exclude zero-denominator
queries from the average and test their empty result separately. A recall number
must identify whether exact fallback participated.

ANN release gate, evaluated in M17 rather than blocking M14 integration: on a
committed seeded fixture with 10k vectors, dimension 32, k=10, and at least 100
held-out queries, achieve mean recall@10
>= 0.95 without exact fallback using a documented parameter configuration.
Choose query/data distributions before tuning and include clustered data.
This is a project target, not a guarantee for every dataset. Amend the target
explicitly with evidence if inappropriate; never silently soften tests.

Also publish curves on higher-dimensional data and, when feasible, real
embeddings. Show at least one useful reduction in query work/latency at a stated
recall relative to exact search. A graph slower than exact search on tiny
collections is acceptable; keep exact mode. Do not encode laptop timing targets
as CI assertions.

Optimization order: measure -> identify dominant cost -> change one thing ->
rerun correctness -> compare the same workload. Stop when the main-path feature
is useful and understandable. A benchmark regression deserves investigation,
not a rewrite of unrelated systems.

## 7. Extension chapters and measured improvements

These are not prerequisites for the main-path release. The owner chooses them;
agents must not start them simply because they appear here.

### Search extensions

Both full-text and fuzzy search are extension chapters, as agreed on 2026-10-16.
They follow the core indexed database; the default schedule is after M17.
Once selected, teach and implement E01 before E02. Their purpose is broader
query/index coverage, so selecting them does not require a performance bottleneck.
Use the existing LSM records, namespaces, scan oracle, and derived-index lifecycle.

**E01 — Full-text search with an inverted index**

Index explicitly configured string metadata fields. Start with a documented
analyzer: Unicode letter/digit token runs, lowercase mapping, no stemming, and
no accent folding; record the analyzer version. Store a term dictionary and
postings from terms to live IDs. Repeated terms in a record must not duplicate
results. Initially answer AND queries over analyzed terms with deterministic ID
ordering; an empty analyzed query returns no matches. Bound field/query lengths
and token counts before mutation or index work. Phrase queries and relevance
ranking are separate optional follow-ups, not first-version completion gates.

Start with rebuildable in-memory postings over durable records. Text replacement,
field removal, delete/reinsert, and restart must remove obsolete matches.
Failure invalidates the index and falls back to analyzing a coherent record scan.
Combine text candidates with metadata predicates, then validate current records.
Tests compare with an independent scan for case, punctuation, Unicode, repeated
terms, empty queries, collection isolation, updates/deletes, and failed rebuilds.
The demo finds a document by words, changes its text, deletes it, then restarts.
Stop when this lifecycle works; defer posting compression and durable materialization.

**E02 — Fuzzy term matching over the text index**

Extend E01 with bounded Levenshtein edit distance over analyzed Unicode code
points. Start with configurable maximum distance 0, 1, or 2; transposition counts
as two edits in this first version. Define fuzzy matching per token, not over the
whole document: expand each query term to dictionary terms within the distance,
union their postings, then intersect across query terms. Deduplicate record IDs,
apply remaining predicates, and order results deterministically by ID. Typo
tolerance is explicit and separate from vector semantic similarity.

Scan the vocabulary first; compare with a simple dynamic-programming oracle.
Set term-length, expansion, and work limits. If a complete expansion cannot fit
the configured budget, return a defined limit error rather than silently omit
matching terms. Cover insertion/deletion/substitution, distance-zero equivalence
to E01, Unicode code points, multiple matching dictionary terms, no matches,
mutations, last-document removal of a term, and restart. Show a typo finding the
same live document; stop there. Tries, automata, n-gram candidate indexes, BK-trees,
and spelling suggestions are later measured alternatives, not prerequisites.

**Other optional coverage**

- Geospatial index: sortable cell codes produce candidates for a radius/box
  query, then exact geometry removes false positives. Test cell boundaries,
  antimeridian behavior, updates, deletes, and restart against a scan oracle.
- Ranked text and hybrid search: introduce term frequency/document frequency
  and a documented ranking formula, then an explicit text/vector fusion policy.
  Keep exact term eligibility distinct from ranking and evaluate on fixed queries.

### Persisting secondary entries in the LSM

After I01/I02 (and E01 if selected), an optional chapter can materialize tuples
such as (field, value, ID) and (term, ID) as reserved LSM keys. These are derived
entries: authoritative records remain sufficient to rebuild them. Choose a
consistency policy before implementation. A straightforward follow-up is a
bounded atomic WAL batch for the record plus removal/addition of its index keys;
an alternative needs a fully specified generation/invalidation/rebuild protocol.
Several ordinary SET calls alone do not establish atomic record/index updates.

Prove create/update/delete, partially written batches, crash/reopen, rebuild,
flush, and compaction against the scan oracle before trusting persisted entries.
An index miss must not hide a committed record after an interrupted update.
This chapter teaches how ordered secondary keys and postings use LSM storage
directly, without making the first index chapters depend on another recovery system.

### Physical improvements, selected by measurement

These follow the working indexed database. Add one improvement at a time;
measure the same workload before/after and rerun its correctness oracle.

| Extension | When justified and behavior | Required tests/proof |
| --- | --- | --- |
| SST blocks and sparse index | Sequential point reads dominate; add bounded checksummed blocks and first-key/offset entries. Version the format and retain old-reader support or specify migration. | First/last/between-block keys, large records, corrupt offsets/checksums, truncated index/footer, old/new format agreement; measure avoided reads. |
| Heap-based exact top-k | Full-sort candidate memory or sorting dominates; keep a bounded max-heap and sort the final k by distance/ID. | Match the full-sort oracle for ties, k edges, filters, updates/deletes; measure selection memory and query work. |
| Bloom filters | Negative SST lookups dominate; filters may produce false positives, never false negatives. Include tombstone keys. | Every inserted key tests positive, corrupted filter is an error or safe bypass, enabled/disabled reads agree; measure avoided reads. |
| Bounded block cache | Repeated SST reads dominate; key entries by immutable file ID/block, enforce byte budget, return owned data. | Eviction, oversized blocks, concurrent access, obsolete-file identity, checksum behavior, cached/uncached equivalence; measure hit rate. |
| Group commit | Sync dominates ingestion; batch concurrent WAL appends under one sync and acknowledge only covered durable writes. | Ordered LSNs, batch sync failure/uncertain outcomes, no early acknowledgment, cancellation/close, crash/reopen; measure throughput and tail latency. |
| Leveled or size-tiered compaction | Full merges cause measured write amplification; specify overlap/input selection before coding. | Excluded older values cannot resurrect, concurrent flush publication, output splits and crash recovery; compare bytes rewritten/read. |
| Packed exact-search cache | Record decode/map overhead dominates exact queries; derived arrays remain coherent with durable records. | Update/delete ID mapping, rebuild/failure fallback, owned buffers, concurrent view; measure allocations and RAM. |
| Atomic batch API | A feature truly needs several KV changes atomically; one validated WAL batch with all-or-none visibility/replay. | Duplicate-key ordering, over-limit rejection, torn batch, post-WAL failure, restart and flush boundaries; no public transaction system implied. |
| Immutable vector segments | One in-memory graph exceeds the chosen workload budget; build a graph per immutable segment plus mutable delta. | Global versions/delete map suppress old segment results, merged top-k/dedup, local ANN overfetch, atomic catalog changes, vector-aware compaction/rebuild; measure memory/read tradeoffs. |

For any optional persisted format, apply the same versioning, bounded decode,
checksums, atomic publication, and recovery matrix as the main path.

## 8. Progress ledger and next request

The status table in section 4 is the roadmap ledger. When asked to update it,
record the stage/substep, accepted contract changes, implementation commit,
test command/result, demo artifact, and remaining limitation. “Partial” is useful;
do not mark stages done from source inspection or an old test result.

### Roadmap revision accepted 2026-10-16

Preserve all started work and stable stage/test IDs. The new order delivers the
M04 disk cycle, then scans/records/search and I01/I02/HNSW before M05/M06/M08
maintenance. M03 starts with sequential SST reads; M11 starts with full-sort
top-k. Their optimization contracts move to section 7. M14 records a recall
baseline; the existing numeric release target is assessed in M17. Full-text and
fuzzy matching are both extension chapters, with fuzzy matching following text
search. Implementation and video order are identical.

### Current chapter: M02, Engine integration

The 2026-10-16/18 failing-test counts are historical and are superseded by this
review on 2026-10-23: branch `feat/lsm`, base commit `2cf676e`, with owner changes
to Engine, capacity preflight, and tests. Evidence applies to that working tree.

**Accepted behavior changes and implementation**

- SET/DELETE share `apply`; normal calls append once, recovery skips append and
  preserves recovered LSNs. Capacity preflight now distinguishes oversized
  records from records that require rotation.
- DELETE always writes a tombstone. Rotation before append is accepted even if
  append fails; current tests allow the retained extra frozen table.
- Recover clears and replays in place; failed replay may retain a readable
  prefix. Old-state preservation and atomic replacement are no longer M02
  requirements. The server aborts startup on recovery error.
- Tests requiring recovery failure isolation, repeated-delete no-ops, and
  automatic post-WAL-failure Engine shutdown were removed. Their absence is
  recorded explicitly; no new guarantee follows from a green remaining suite.
- Sequential closed-state checks and new-instance durable restart pass. A
  closed-instance restart is not supported by the unchanged DurableWal lifecycle.

**Validation performed**

```sh
GOCACHE=/private/tmp/vectorized-gocache go test ./...
GOCACHE=/private/tmp/vectorized-gocache go test -race ./core ./db
go test -count=1 ./...
```

The first run passes all packages except `transport/tcpserver`, whose listener
cannot bind in the sandbox (`operation not permitted`). The focused race run
passes. The final uncached full-suite run outside the sandbox passes all packages,
including TCP. No full-repository race run, crash harness, or recording demo was
performed. Documentation whitespace and internal links are checked separately.

**Review limits, not silently completed work**

- Recover clears before Replay validates WAL lifecycle. Calling it twice after
  success, or after Close, can erase memory before WAL rejects replay. Its
  restart comment overstates what a closed DurableWal can do.
- Recover ignores Clear's error; a frozen active table need not reset successfully.
- Close writes `closed` without the Engine mutex. Concurrent close is not covered
  by the passing sequential close test or the current race workload.
- There is no Engine unhealthy latch after a post-WAL apply failure. The record
  can be missing in memory yet reappear on restart. Existing handler errors do
  not automatically stop service.
- New partial-prefix visibility and unconditional absent/repeated DELETE policy
  lack dedicated replacement coverage; successful replay tests are not evidence
  of recovery-error or closed-instance behavior.

See [Engine architecture](arch/engine.md) for the call path and precise limits.
Production code and tests were reviewed and run, not edited in this update.

### Added scope on 2026-10-18

M02-S1 is the owner's requested skip-list replacement before SST work. It remains
planned, with the existing map version preserved as covered history. No skip-list
implementation or tests were added during this documentation review.

**Next planned substep:** M02-S1 after disposition of the review limits above.
Do not reinstate discarded atomic-recovery or existence-check DELETE policies
as hidden prerequisites. Current test success does not establish lifecycle claims
that the real WAL rejects. The owner chooses the scope of the next code slice.

**Current chapter exit:** accepted M02 contracts and M02-S1 checks pass; run
the relevant integration/race checks and a tiny-table demo that writes across
three generations, overwrites/deletes/reinserts, closes, reopens, and obtains
the same live view from the real WAL. Fix correctness failures within this scope.
Record the evidence, then start M03's minimal SST format. Do not add an arena,
background worker, compaction, or throughput target to this exit gate.

## 9. Reference material

External references inform concepts; they do not silently replace this project's
chosen contracts or require matching another database's architecture.

- [Existing WAL design](arch/wal.md): current local format/lifecycle details.
- [LevelDB implementation notes](https://github.com/google/leveldb/blob/main/doc/impl.md):
  an example of sorted tables, a live-file manifest, and compaction. This project
  deliberately starts with a simpler full-merge policy and manifest snapshot.
- [Faiss metric conventions](https://github.com/facebookresearch/faiss/wiki/MetricType-and-distances):
  squared L2 and cosine normalization.
- [HNSW paper](https://arxiv.org/abs/1603.09320): hierarchy, graph search, and
  neighbor-selection foundations for M14.
