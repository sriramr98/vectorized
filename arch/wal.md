## WAL Architecture

A write-ahead log (WAL) protects acknowledged writes from being lost if the database crashes before its in-memory state is flushed to the LSM tree.

Writes are appended to the WAL before they are applied to memory or confirmed to the client. On restart, the WAL can be replayed to restore those writes.

## WAL Layout

The WAL is a directory containing ordered segment files. Each segment is named `wal_segment__<id>`, where `id` is a non-negative integer. Segment IDs define the order of the segments.

The directory also contains `wal.lock`, which is used to prevent multiple database processes from operating on the same WAL directory.

## WAL Entry Format

Each segment contains a sequence of entries encoded as follows:

![WAL structure](./assets/wal_structure.png)

1. **CRC** — 4 bytes. A checksum of the LSN, operation type, length, and data.
2. **LSN** — 8 bytes. A monotonically increasing log sequence number.
3. **Operation type** — 1 byte. Identifies the operation, such as `SET` or
   `DELETE`.
4. **Data length** — 8 bytes. The size of the data field.
5. **Data** — Variable length. The operation's arguments.

The fixed entry overhead is 21 bytes, so the total entry size is:

```text
21 + data length
```

## WAL Startup

When a WAL is opened:

1. The path is converted to an absolute path.
2. The directory is created if needed with owner-only permissions (`0700`).
3. An exclusive, non-blocking advisory lock is acquired on `wal.lock`.
4. Existing segment files are discovered and sorted by ID. Directories and files with invalid names are ignored.
5. A new active segment is created with the next ID, even if the previous segment was not full.

Segment files use owner-only read/write permissions (`0600`) and are opened in append mode. Historical segments are tracked by path but are not kept open; only the active segment is opened for writing.

An empty WAL starts with `wal_segment__1`. The next active segment is always `max(existing segment ID) + 1`.

## WAL Shutdown

Closing the WAL synchronizes and closes the active segment, closes any other open segment handles, and releases the directory lock. The lock must be held for the entire lifetime of the WAL so that only one process can write to the directory at a time.

## Recovery and Segment Rotation

Recovery replays valid WAL entries in segment and entry order. The CRC allows recovery to detect incomplete or corrupted entries after a crash.

When the active segment reaches its configured size limit, the WAL creates a new segment with the next ID and continues appending there.
