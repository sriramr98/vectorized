# Architecture

Use this guide to understand how Vectorized stores writes, serves reads, and
recovers after restart.

Vectorized currently stores records in memory and persists mutations in a
write-ahead log (WAL). Sorted string tables (SSTs) are the next storage component;
the SST page specifies a proposed file format.

| Component | Responsibility | Read next |
| --- | --- | --- |
| Engine | Order mutations, resolve reads, coordinate recovery and lifecycle | [Engine architecture](engine.md) |
| Memtable | Hold versioned records within a logical byte budget | [Memtable architecture](memtable.md) |
| WAL | Persist ordered mutations and replay them after restart | [WAL architecture and format](wal.md) |
| SST | Store immutable sorted records with a sparse block index | [Proposed SST V1 format](sstable.md) |

The server handles a small RESP command set. SET and DELETE enter the Engine,
which appends and syncs the WAL before applying the mutation to memory. GET
searches active memory and then frozen tables. At startup, the server reconstructs
the memory view from WAL before accepting connections.

The WAL is authoritative for the current storage design. The Engine acknowledges
a mutation only after both WAL persistence and memory application succeed.
SST publication will add a manifest that selects the authoritative table set;
writing an SST file alone will not make it part of the database.
