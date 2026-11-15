# Memtable architecture

A memtable holds versioned key/value records in memory. The Engine writes to one
active memtable and retains frozen tables for reads. Use this page to understand
record ownership, capacity, and the records available for an SST flush.

The implementation is in [db/memory.go](../db/memory.go); its interfaces are in
[db/store.go](../db/store.go).

## Records and ordering

Each record contains a byte-string key, value bytes, a positive log sequence
number (LSN), and a tombstone flag. BasicMemTable stores one record per key in a
map. An accepted Apply replaces the previous record, including a tombstone.

An incoming LSN must exceed the table's last accepted LSN, even for a different
key. The Engine supplies ordered WAL records. A rejected Apply changes neither
records, logical size, nor the last accepted LSN.

## Capacity accounting

Logical size per stored record is:

```text
8 bytes for LSN + 1 byte for tombstone flag + key bytes + value bytes
```

Replacement subtracts the old record's size before adding the new one. Exact
capacity fits. Engine tombstones carry no value bytes. This budget measures
logical records, not Go map, pointer, or allocator overhead.

| Capacity preflight result | Meaning | Engine action |
| --- | --- | --- |
| nil | Replacement/insertion fits the active table | Continue |
| ErrRecordDoesntFit | Fits an empty table, but not this table | Rotate |
| ErrRecordTooLarge | Exceeds the table's total configured budget | Reject before rotation or WAL append |

CanAccomodate checks capacity without reserving space. Apply independently checks
freeze state, LSN ordering, and capacity under the memtable write lock.

## Freeze, reads, and export

Freeze is idempotent. A frozen table supports Get and GetAllSorted but rejects
Apply and Clear. The Engine retains it until a replacement storage layer can
serve its records.

Get distinguishes a found tombstone from a missing key. GetAllSorted sorts keys
in bytewise order and exports records, including tombstones and LSNs. The SST
writer can consume that ordered export without reading WAL again.

Apply copies its input data. Get and GetAllSorted return independent copies.
Callers cannot change stored values by modifying their input or returned slices.
Clear on a mutable table resets records, logical size, and LSN ordering.
