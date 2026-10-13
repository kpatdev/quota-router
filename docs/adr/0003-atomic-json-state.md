# 3. Atomic JSON state file instead of SQLite

Date: 2026-10-13. Status: accepted.

State is bounded (bindings capped by `affinity_max_sessions`, history downsampled)
and always written whole. A temp-file + fsync + rename + directory fsync write is
crash-safe for that pattern. SQLite would need cgo cross-compilation of the
amalgamation or a multi-megabyte pure-Go port inside an in-process plugin, with
no integrity benefit. Writes are debounced (≤ 1/s) so a crash loses at most about
a second of binding updates; a lost binding only means one re-selection.
