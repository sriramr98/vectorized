# WAL architecture and format

The write-ahead log (WAL) persists ordered mutations so the Engine can rebuild
memory after restart. Use this page to understand durability, record encoding,
segment rotation, and replay. The implementation starts in
[db/wal/wal.go](../db/wal/wal.go).

## Append and durability

The Engine appends a mutation before applying it to memory. Each successful
WAL Write assigns an increasing positive log sequence number (LSN), appends one
complete record, and synchronizes the active file before returning.

A short write, append failure, sync failure, or segment-rotation failure marks the
WAL unhealthy. Further writes and replay are rejected until a new WAL instance
is opened. A failed write may still be present after restart; callers cannot
interpret an error as proof that the record was not persisted.

The WAL provides ordered durable records. The [Engine](engine.md) owns memory
application, read visibility, and client acknowledgment.

## Directory and segment files

A WAL directory contains segment files named `wal_segment__<id>` and a
`wal.lock` file. The lock is exclusive and non-blocking, so a second owner of the
same directory fails promptly. The lock remains held throughout the WAL lifetime.

Segments are replayed in numeric ID order. A new directory starts with segment 1.
Only the active segment stays open for append; historical segments are tracked
by path. Directories use owner-only permissions (0700); segment files use 0600.

Before a record would exceed the segment-size threshold, the WAL closes the
active segment and creates the next numbered segment. It syncs the directory
when creating a new segment. Records are never split across segment files. The
rotation threshold is separate from the maximum permitted record size.

## V1 record format

All multi-byte integers use big-endian encoding. Each record has an 18-byte
header followed by its encoded arguments.

| Offset | Field | Bytes | Meaning |
| --- | --- | --- | --- |
| 0 | Checksum | 4 | CRC32 IEEE over bytes from version through the end of data |
| 4 | Version | 1 | 1 |
| 5 | LSN | 8 | Increasing mutation sequence number |
| 13 | Operation | 1 | 1 for SET, 2 for DELETE |
| 14 | Data length | 4 | Argument payload length in bytes |
| 18 | Data | Variable | Encoded operation arguments |

The payload contains a uint64 argument count, followed by a uint64 length and
bytes for each argument. SET has key/value arguments; DELETE has one key argument.
Payload integers are also big-endian. Length framing permits arbitrary binary
keys and values without delimiter escaping.

The encoder reserves the checksum field, encodes the remaining record, computes
the checksum, and fills the reserved field. The decoder bounds the declared data
length before allocation and verifies the checksum before emitting a record.

See [record encoding](../db/wal/wal_entry.go) and
[argument encoding](../utils/encoding.go) for the persisted representation.

## Open and replay

1. Resolve the directory, create it if needed, and acquire its lock.
2. Discover segment files and sort them by ID.
3. Remain in recovery-required state. Writes are rejected.
4. Replay records through the caller's callback, validating their encoding,
   checksums, operation types, and increasing LSNs.
5. After successful replay, establish the next LSN and open the active segment
   for writes. Reuse the latest segment if it has room; otherwise create a new one.

Replay may succeed once per open WAL. Replay on a ready or closed WAL is rejected.
A callback error leaves WAL recovery-required; retry begins from the start and
requires resetting the destination first. DurableWal returns zero on replay
error even if earlier callbacks applied records. The Engine must check the error
rather than treating that count as proof of an empty recovered view.

## Torn tails and corruption

Only an incomplete final record in the latest segment is a recoverable torn tail.
Replay truncates that segment to its last complete record and syncs the repair.
An incomplete record in an older segment, invalid checksum, unsupported version,
illegal operation, excessive declared length, or non-increasing LSN is an error.

The [server startup path](../commands/server.go) aborts on a recovery error before
accepting connections. A partial Engine replay is not a successful database open.

## Close

Close synchronizes and closes the active segment, releases remaining resources,
and releases the directory lock. It is idempotent after successful cleanup. Close
is terminal for that WAL instance; restart opens a new WAL over the same files.
