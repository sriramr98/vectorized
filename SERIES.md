# Building Vectorized: A Vector Database in Go

This repository backs a YouTube series in which we build an embedded vector
database in Go. The videos are code walkthroughs, not live-coding sessions:
each episode begins from a known commit, explains the design decision, walks
through the relevant code, and proves the behavior with tests or a small demo.

The end product is a single-node, persistent vector database with:

- durable writes and crash recovery;
- exact and approximate nearest-neighbor search;
- metadata filtering;
- updates, deletes, and compaction;
- a small, inspectable Go codebase.

Distributed replication, a SQL layer, authentication, and a production network
service are deliberately out of scope. The series is about storage-engine and
vector-index fundamentals.

## How Each Video Works

Every episode should answer four questions:

1. What constraint does the previous implementation fail to meet?
2. What invariant does this episode introduce?
3. Which files express that invariant?
4. Which test, benchmark, or failure demo proves it?

Record each episode against a tagged commit. Keep the walkthrough focused on
the public API, the write/read path, the on-disk format where applicable, and
the tests. Avoid reading every line in order; viewers should leave with a model
of the system, not a transcription of the repository.

## Phase One: A Durable KV Engine

The vector database will initially sit on top of a small LSM-style key-value
engine. That is intentional: vectors need durable records, versioning, deletes,
and compaction before they need an ANN index.

| # | Episode | Viewer question | Implementation milestone | Proof |
| --- | --- | --- | --- | --- |
| 1 | We're Building a Vector DB | Can a client talk to our database already? | Define the API and guarantees, then build an in-memory KV store behind a TCP server. Parse a small RESP command subset: `PING`, `GET`, `SET`, and `DEL`. Establish byte-slice ownership and missing-key behavior. | Integration test over TCP plus unit tests for overwrite, delete, and key/value isolation. |
| 2 | WAL and Recovery | How does an acknowledged write survive a crash? | Append mutations to a write-ahead log before applying them to the memtable. Replay valid log records on open. | Kill/reopen demo plus tests for replay and truncated final records. |
| 3 | SSTables: Write and Read | How do records outlive memory? | Flush an immutable memtable into a sorted table on disk, then read from both memory and tables. Define a simple table format and file naming convention. | Reopen tests and read-precedence tests across memory and disk. |
| 4 | Compaction | What prevents disk reads from getting slower forever? | Merge sorted tables, retain the newest value per key, and remove obsolete entries according to the current delete policy. | Tests for overlapping tables, overwrites, and deletes; a before/after read-count benchmark. |

### Guarantees After Episode 4

At this point, `vectorized` should be able to truthfully say:

- A successful write is present after a clean restart and after WAL replay.
- `Get` returns the most recently written value for a key.
- Flushing to an SSTable preserves the same logical contents.
- Compaction preserves visible values while reducing redundant table data.

It should not yet claim fully crash-safe compaction. That needs a manifest or
an atomic version-switching scheme and should be its own episode; otherwise a
crash between creating a compacted table and deleting old files can lose or
resurrect data.

### Wire Protocol: Use a Small RESP2 Subset

Use RESP2 for the initial server. It makes the first episode immediately
demonstrable with `redis-cli`, is binary-safe, and gives the parser a real
framing problem without turning the series into a protocol-design project.

Only support the commands the storage engine needs at first:

```text
PING
SET key value
GET key
DEL key
```

Accept RESP arrays and bulk strings, and return the appropriate RESP simple
strings, bulk strings, integers, and errors. Explicitly reject unsupported
RESP types and commands. Do not claim Redis compatibility: this is a small
RESP-speaking database, not a Redis clone.

Keep the protocol adapter thin. It should parse a request into a command,
call the database interface, and encode the response. The database package
must not know about TCP connections or RESP. That separation lets later
episodes replace the in-memory implementation with WAL/SSTable-backed storage
without changing client behavior.

## Recommended Repository Shape

Start with boundaries that make the walkthroughs easy to follow. Names can
change as the code teaches us better ones, but keep storage concerns separate
from the eventual vector index.

```text
.
├── cmd/               # TCP server, small demos, and inspection tools
├── db/                # Public database API and orchestration
├── protocol/          # RESP parsing and response encoding
├── storage/
│   ├── memtable/      # Mutable in-memory state
│   ├── wal/           # Log encoding and replay
│   ├── sstable/       # Sorted table encoding and lookup
│   └── manifest/      # Added when table-set changes become crash safe
├── vector/            # Added when vector records and search begin
├── index/             # Added for HNSW and other ANN structures
└── internal/          # Shared encoding, file, and test helpers
```

Do not create all of these packages in episode 1. Add a boundary when the
episode needs it. Premature package structure is harder to explain than a
small refactor with a clear reason.

## Series Conventions

- Store keys and values as bytes in the KV layer. Higher layers own encoding.
- Treat every disk format as a contract: specify record fields, checksums, and
  corruption behavior beside the implementation.
- Give every persisted format a version from its first appearance.
- Prefer deterministic tests over timing-based tests.
- Keep a benchmark next to every performance claim, especially before moving
  from exact vector search to HNSW.
- Make failure handling visible. A database series earns trust by showing what
  happens when a write is partial, a file is corrupt, or a process stops at the
  wrong moment.

## Next Phases

Once the initial KV engine is working, the natural path is:

1. Make compaction crash safe with a manifest and recovery rules.
2. Add versions, snapshots, and atomic write batches.
3. Define vector records, collections, IDs, and metadata.
4. Implement exact k-nearest-neighbor search as the correctness baseline.
5. Add filtering, profiling, and benchmarks.
6. Build, evaluate, persist, and maintain an HNSW index.
7. Move to immutable vector segments with deletes and vector-aware compaction.

The exact episode titles beyond phase one should follow the code. The important
dependency is fixed: persistence and correctness first; approximate search and
performance work afterwards.

## Episode Checklist

Before publishing an episode:

- Tag the exact commit used in the recording.
- Ensure `go test ./...` passes from that commit.
- Include one focused test or demo that demonstrates the episode's new
  guarantee.
- State one limitation that the next episode will address.
- Avoid presenting benchmarks from a developer laptop as universal numbers;
  explain the workload, dimensions, dataset size, and machine instead.
