# Vectorized

A database internals project in Go: building a durable key-value engine toward
an LSM-backed vector database.

The current implementation combines a TCP/RESP server, a segmented write-ahead
log, and versioned mutable/immutable memtables. The focus is on implementing and
explaining storage behavior: write ordering, recovery, deletion visibility,
capacity management, and failure handling.

## Implemented

- **Durable write-ahead log:** versioned binary records with CRC checksums,
  increasing log sequence numbers (LSNs), per-write sync, and segment rotation.
- **Startup recovery:** ordered WAL replay across segments, reconstruction across
  multiple memtables, incomplete final-record repair, and corruption detection.
  The server admits clients only after successful recovery.
- **Versioned memtables:** logical byte budgets, replacement accounting,
  oversized-record rejection, defensive copies, freezing, and sorted export.
- **Layered reads and tombstones:** active memory followed by frozen tables in
  reverse creation order, preserving overwrites and preventing deleted values
  from reappearing from older tables.
- **Explicit failure boundaries:** serialized mutations; a post-WAL application
  failure disables further mutations while keeping the previously applied
  in-memory view readable. Close coordinates with the Engine lock.
- **TCP command interface:** binary-safe RESP framing for `PING`, `SET`, `GET`,
  and `DEL`, including fragmented/pipelined requests and connection shutdown.

## How writes work

```text
SET / DEL
    → acquire Engine write lock
    → check capacity; rotate the memtable if needed
    → append and sync the WAL record
    → apply to memory using the assigned LSN
    → acknowledge
```

The WAL supplies durability; memtables supply the current read view. Recovery
reuses the memory-application path with recovered LSNs and no new WAL appends.
The database directory is protected by an exclusive WAL directory lock.

An error after WAL acceptance has an uncertain outcome: the mutation may appear
after restart even if the client received an error. DELETE always records a
tombstone; its success does not indicate whether the key previously existed.

## Run locally

Use the Go toolchain specified in [go.mod](go.mod), currently Go 1.27.1.

```sh
go run . server --port 6380
```

Using `redis-cli` as a client:

```sh
redis-cli -p 6380 PING
redis-cli -p 6380 SET hello database
redis-cli -p 6380 GET hello
```

Stop with Ctrl-C, restart the server, and GET `hello` again to exercise WAL
recovery. Then try:

```sh
redis-cli -p 6380 DEL hello
redis-cli -p 6380 GET hello
```

The default data directory is `~/.vectorized/data`; WAL files live in its `wal/`
subdirectory. To choose a directory, create `vectorized.yaml`:

```yaml
port: 6380
data_path: ./data
```

Configuration can also be selected with `--config_path path/to/config.yaml`.
Only the commands listed above are supported. This is a RESP subset, with its
own deletion semantics: successful `DEL` returns `1`, including for missing keys.

## Tests

```sh
go test ./...
go test -race ./core ./db ./db/wal
```

The suites cover capacity boundaries, LSN ordering, slice ownership, tombstone
visibility, rotation, real WAL close/reopen recovery, short writes, sync failures,
torn tails, and rejection of mutations after an uncertain application failure.

## Read the implementation

| Component | Entry point |
| --- | --- |
| Engine, mutation ordering, and recovery | [core/engine.go](core/engine.go) |
| Memtable records and capacity accounting | [db/memory.go](db/memory.go) |
| WAL lifecycle and persistence | [db/wal/wal.go](db/wal/wal.go) |
| WAL record encoding and checksums | [db/wal/wal_entry.go](db/wal/wal_entry.go) |
| WAL replay and tail repair | [db/wal/replay.go](db/wal/replay.go) |
| RESP command handling | [handlers/public_handler.go](handlers/public_handler.go) |
| TCP connection lifecycle | [transport/tcpserver/server.go](transport/tcpserver/server.go) |

## Next milestones

1. Immutable SST files, a sparse block index, and synchronous flush.
2. Manifest-backed file publication, disk reads, and restart recovery.
3. Durable vector records and exact nearest-neighbor search.
4. HNSW construction, mutation integration, and reproducible recall/latency
   comparisons against exact search.
5. Compaction, WAL reclamation, and measured storage improvements.

**Current boundary:** durability is WAL-backed; records are reconstructed into
memory on startup. SST storage and vector search are not implemented yet. Each
memtable has a logical byte budget, but frozen tables and WAL history accumulate
until flushing and reclamation are added. The current memtable uses a map and
sorts on export. Performance results will be published with reproducible workloads.

See the [architecture guide](arch/README.md) for component responsibilities,
read/write paths, and persisted formats.
