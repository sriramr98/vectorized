## Important behavioral issues

  [x] Missing DEL behaves incorrectly for a Redis[]like API. Engine.Delete (core/engine.go:65) returns ErrKeyNotFound, causing the handler to return an internal error. It should normally return (false, nil), allowing
    the handler to send :0.

  [x] Recovery accepts extra SET arguments because core/engine.go:101 checks len(res) < 2; it should require exactly two.
  [x] Recover clears and then incrementally modifies the live store. A replay failure leaves partial state. Building a fresh memtable and publishing it only after successful replay is safer.
  [x] DurableWal.Write (db/wal/wal.go:177) does not reject n < len(record) with a nil error.
  [x] LengthEncodeBytes (utils/encoding.go:21) ignores errors returned while writing the argument count and individual lengths.
  [x] Close is not idempotent: a second call reaches a nil dirLocker.
  [x] The supplied context.Context is unused, which makes the constructor contract misleading.
  [x] New WAL segment creation is not followed by directory synchronization. File Sync alone does not establish the strongest crash guarantee for a newly created directory entry.

  ## Test-suite gap

  go test ./... passes, and the race detector passed for the storage, WAL, handler, protocol, and utility packages. However, core currently has no tests, while most handler tests use a no[]op InMemWal (db/wal/
  inmem.go:3).

  The most important missing tests are:

  [x] updating an existing key when WAL append fails;
  [x] inserting a new key when WAL append fails;
  [x] verifying WAL happens before memory application;
  [x] deleting a missing key;
  [x] real close/reopen recovery through Engine;
  [x] replay → append → replay, with open segments excluded;
  [x] replay callback failure followed by another replay;
  [x] torn final header and torn final body;
  [x] corrupt complete record;
  [x] oversized declared record;
  [x] recovery failure without publishing partial state.

  ## Small action list

   1. Correct Engine ordering and add Engine failure tests.
   2. Remove/fix replay caching, bound allocations, and handle torn final records.
   3. Make WAL append return its assigned LSN.
   4. Introduce memtable entries containing LSN plus value/tombstone.
   5. Add mutable → immutable memtable swapping and layered reads.
   6. Implement a single sorted SSTable format, initially keeping all WAL files.
   7. Add a manifest/checkpoint before implementing WAL deletion or compaction.
