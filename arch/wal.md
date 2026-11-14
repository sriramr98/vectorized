## WAL Architecture

This describes the WAL component. [SERIES.md](../SERIES.md) is authoritative for
the implementation roadmap and cross-component behavior, including future
manifest checkpoints and WAL reclamation.

A write-ahead log (WAL) protects acknowledged writes from being lost if the database crashes before its in-memory state is flushed to the LSM tree.

Writes are appended to the WAL before they are applied to memory or confirmed to the client. On restart, the WAL can be replayed to restore those writes.

Each successful write is synchronized to disk before `Write` returns. If segment rotation, append, or synchronization fails, the WAL becomes unhealthy and rejects further writes and replay. It must be closed and reopened before use so startup recovery can validate and, when appropriate, repair the log.

Before checkpointing, the WAL is the durable source of truth and memory is a
materialized view. Once manifest-backed checkpoints and reclamation exist, the
committed SST set plus the retained WAL suffix jointly represent authoritative
state. An entry must not be removed to undo an application failure. The Engine
must not acknowledge until its durability and application requirements hold.
Current Engine M02 uses one shared apply path with WAL bypass during recovery,
clears memory, and replays in place; failure can leave a readable prefix. The
server refuses startup if Recover returns an error. Engine does not currently
latch unhealthy after an apply failure; that limitation is documented in
[Engine architecture](engine.md). M04 will skip checkpoint-covered entries and
keep public admission closed until validation succeeds. Memtables still reject
duplicate or out-of-order LSNs; replay must reset its destination before retry.

## WAL Layout

The WAL is a directory containing ordered segment files. Each segment is named `wal_segment__<id>`, where `id` is a non-negative integer. Segment IDs define the order of the segments.

The directory also contains `wal.lock`, which is used to prevent multiple database processes from operating on the same WAL directory.

## WAL Entry Format

Each segment contains a sequence of entries encoded as follows:

![WAL structure](./assets/wal_structure.png)

1. **CRC** — 4 bytes. A checksum of the version, LSN, operation type, length,
   and data.
2. **Version** — 1 byte. Identifies the encoding version of the WAL entry.
3. **LSN** — 8 bytes. A monotonically increasing log sequence number.
4. **Operation type** — 1 byte. Identifies the operation, such as `SET` or
   `DELETE`.
5. **Data length** — 4 bytes. The size of the data field.
6. **Data** — Variable length. The operation's arguments.

The fixed entry overhead is 18 bytes, so the total entry size is:

```text
18 + data length
```

## WAL Startup

When a WAL is opened:

1. The path is converted to an absolute path.
2. The directory is created if needed with owner-only permissions (`0700`).
3. An exclusive, non-blocking advisory lock is acquired on `wal.lock`.
4. Existing segment files are discovered and sorted by ID. Directories and files with invalid names are ignored.
5. The WAL remains in `recovery required` state. Writes are rejected until startup recovery succeeds.
6. Startup recovery scans every discovered segment once, applies each entry through the caller's callback, and derives the latest LSN in the same pass.
7. If the latest segment is below the configured size limit, it is reopened in append mode and reused. Otherwise, a new active segment is created with the next ID and the WAL directory is synchronized so the new directory entry is durable.

Segment files use owner-only read/write permissions (`0600`) and are opened in append mode. Historical segments are tracked by path but are not kept open; only the active segment is opened for writing.

An empty WAL starts with `wal_segment__1`. A new segment uses `max(existing segment ID) + 1`; a reusable latest segment retains its existing ID and byte-size accounting.

## WAL Shutdown

Closing the WAL synchronizes and closes the active segment, closes any other open segment handles, and releases the directory lock. The lock must be held for the entire lifetime of the WAL so that only one process can write to the directory at a time.

## Recovery and Segment Rotation

Recovery replays segments and entries present at startup in order and may succeed only once for an open WAL. The callback is responsible for rebuilding the store while replay simultaneously derives the latest LSN. Only after the complete scan and callback succeed does the WAL open an active segment and accept writes. A failed callback leaves the WAL in `recovery required` state so recovery can be retried from the beginning.

Engine's in-place replay policy does not change this WAL lifecycle. A failed
callback can leave Engine prefix records even though DurableWal returns a zero
count and does not enable writes. Replay on a ready WAL is rejected, and Close
is terminal for that WAL instance. Engine.Recover does not reopen it; ordinary
restart constructs a new WAL and Engine. Do not use the permissive test fixture
as evidence that Recover-after-Close works with DurableWal.

A torn final record in the latest recovered segment is truncated back to the last complete record. An incomplete record in an older segment, a complete record with an invalid CRC, an invalid operation, or non-increasing LSN order is treated as corruption.

Before a record would make the active segment exceed its configured size limit, the WAL closes that segment, creates a new segment with the next ID, synchronizes the WAL directory, and appends the record there.


## Interesting Decisions

1. Why is CRC calculated at the end but written to the start of the record?

We need to first convert all the data we want into bytes before we calculate its checksum. That's why we calculate it at the end.

When we replay a WAL record, we need to know if an entry is valid before we parse or skip it. Hence, it is easy to read the first 4 bytes, look at the checksum, parse the remaining bytes, and compare the checksum.


2. Why do we append data length to the record instead of using a de-limiter to identify separate records?

What if the de-limiter was part of the data we're encoding? Since the data comes from various places, we cannot reliably find a delimiter that won't come up inside the data. Hence it's simple to just track the length of bytes and read till then to decode the original WalEntry.
