## WAL Architecture

A write-ahead log (WAL) protects acknowledged writes from being lost if the database crashes before its in-memory state is flushed to the LSM tree.

Writes are appended to the WAL before they are applied to memory or confirmed to the client. On restart, the WAL can be replayed to restore those writes.

Each successful write is synchronized to disk before `Write` returns. If segment rotation, append, or synchronization fails, the WAL becomes unhealthy and rejects further writes and replay. It must be closed and reopened before use so startup recovery can validate and, when appropriate, repair the log.

The WAL is the source of truth; memory, disk, and replicas are materialized views. If applying a committed WAL entry to any of them fails, the entry must not be removed or rolled back from the WAL. The system should retry application or replay it during recovery, and should not acknowledge the client until the required durability and application policy is satisfied. WAL operations should therefore be safe to replay and, ideally, idempotent.

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
5. If the latest segment is below the configured size limit, it is reopened in append mode and reused. Otherwise, a new active segment is created with the next ID and the WAL directory is synchronized so the new directory entry is durable.

Segment files use owner-only read/write permissions (`0600`) and are opened in append mode. Historical segments are tracked by path but are not kept open; only the active segment is opened for writing.

An empty WAL starts with `wal_segment__1`. A new segment uses `max(existing segment ID) + 1`; a reusable latest segment retains its existing ID and byte-size accounting.

## WAL Shutdown

Closing the WAL synchronizes and closes the active segment, closes any other open segment handles, and releases the directory lock. The lock must be held for the entire lifetime of the WAL so that only one process can write to the directory at a time.

## Recovery and Segment Rotation

Recovery replays segments and entries present at startup in order. If the latest segment is reused, replay exposes only its recovered prefix while it remains active; records appended after startup become replayable once rotation closes the segment. A newly created active segment is excluded. A torn final record in the latest recovered segment is truncated back to the last complete record. An incomplete record in an older segment, a complete record with an invalid CRC, an invalid operation, or non-increasing LSN order is treated as corruption.

Before a record would make the active segment exceed its configured size limit, the WAL closes that segment, creates a new segment with the next ID, synchronizes the WAL directory, and appends the record there.


## Interesting Decisions

1. Why is CRC calculated at the end but written to the start of the record?

We need to first convert all the data we want into bytes before we calculate its checksum. That's why we calculate it at the end.

When we replay a WAL record, we need to know if an entry is valid before we parse or skip it. Hence, it is easy to read the first 4 bytes, look at the checksum, parse the remaining bytes, and compare the checksum.


2. Why do we append data length to the record instead of using a de-limiter to identify separate records?

What if the de-limiter was part of the data we're encoding? Since the data comes from various places, we cannot reliably find a delimiter that won't come up inside the data. Hence it's simple to just track the length of bytes and read till then to decode the original WalEntry.
