# Engine architecture

The Engine coordinates durable mutations, layered reads, recovery, and shutdown.
Use this page to understand how the [WAL](wal.md) and [memtables](memtable.md)
work together. The implementation is in [core/engine.go](../core/engine.go).

## Owned state and locking

The Engine owns one active mutable memtable, a list of frozen memtables, a WAL,
and its lifecycle state. A table factory creates fresh active memtables after
rotation. A reusable buffer holds encoded mutation arguments.

SET, DELETE, Recover, and Close hold the Engine write lock. GET holds the read
lock. This serializes mutations and prevents a read from observing an operation
halfway through memory application. Memtables also protect their own state.

## Write ordering

SET and DELETE share the internal apply path:

1. Check that the Engine accepts mutations.
2. Check capacity. Reject a record that cannot fit an empty table. If only the
   active table lacks room, freeze it, retain it for reads, and create a new one.
3. Encode SET as key/value arguments or DELETE as a key argument.
4. Append the matching operation to WAL and wait for its sync and assigned log
   sequence number (LSN).
5. Apply the record to the active memtable using that LSN, then acknowledge.

Capacity rotation happens before WAL append. If append fails, the extra frozen
memtable remains, but the failed mutation has not changed the logical read view.
Rotation creates a memory table; it does not create a disk table.

DELETE always writes a fresh tombstone, including for absent or already-deleted
keys. Its returned bool reports successful tombstone application, not prior key
existence. Repeated deletes consume space and can trigger rotation.

## Read ordering and tombstones

GET checks the active memtable, then frozen tables from newest to oldest. The
first matching record determines the result. A tombstone returns the missing-key
error and stops lookup, so an older value cannot reappear. Returned values are
copies owned by the caller.

## States and uncertain writes

| State | GET | SET / DELETE |
| --- | --- | --- |
| Ready | Read the current memory view | Accept mutations |
| Unhealthy | Read the last successfully applied memory view | Return the unhealthy error |
| Closed | Return the closed error | Return the closed error |

If WAL accepts a mutation but memtable Apply fails, the Engine becomes Unhealthy.
Memtable rejection leaves its contents unchanged, so reads still observe a
coherent prefix of the write history. Further writes are rejected before WAL
append. Close remains available.

The failed mutation has an uncertain outcome. It may appear after WAL replay even
though the caller received an error and GET previously returned the older value.
An error is not a rollback guarantee. WAL I/O failures also have uncertain outcomes;
the WAL separately rejects further writes when it becomes unhealthy.

## Startup recovery

Open a new WAL instance over the existing directory and create a new Engine.
Call Recover before admitting clients. Recover clears the memory view, checks
Clear's error, and replays records through the same capacity/application path.
Replay preserves stored LSNs and does not append new WAL records. A history larger
than one memtable creates multiple frozen tables.

Recovery rebuilds memory in place. A replay error can leave a readable prefix;
that prefix is not a successful database open. The server checks the returned
error and aborts before opening its listener. The replay count is meaningful on
success; DurableWal can return zero on error after callbacks have applied records.

A durable WAL allows one successful replay per instance. Recover is a startup
operation, not a live reset or a way to reopen a closed WAL. Normal restart uses
new WAL and Engine instances.

## Shutdown and storage boundaries

Close holds the Engine write lock, closes WAL, and marks the Engine Closed after
successful cleanup. WAL close synchronizes the active segment and releases the
directory lock. Repeated successful Close calls are supported.

The current read path uses memory only. WAL history and frozen memtables remain
until disk-table flushing and reclamation are introduced. The proposed
[SST format](sstable.md) defines immutable files; manifest publication will decide
when those files become authoritative.
