#!/usr/bin/env python3
"""Write the databases sqlite_test.go reads, with SQLite itself.

    python3 internal/machine/sqlite/testdata/make.py

Run from anywhere; the files land next to this script. The tests check what
the files hold, not their bytes, so another SQLite version writes files that
pass just the same.
"""
import gzip
import os
import shutil
import sqlite3

HERE = os.path.dirname(os.path.abspath(__file__))

# Every file is kept gzipped: pages are mostly zeros, and the tests unpack them
# into a temporary directory, where nothing else opens them.
def pack(path):
    for suffix in ("", "-wal"):
        if os.path.exists(path + suffix):
            with open(path + suffix, "rb") as src, gzip.GzipFile(path + suffix + ".gz", "wb", mtime=0) as dst:
                dst.write(src.read())
            os.remove(path + suffix)
    if os.path.exists(path + "-shm"):
        os.remove(path + "-shm")

# zellij's session index, as zellij-utils/src/session_index.rs creates it.
SESSIONS = """CREATE TABLE sessions (
                name TEXT PRIMARY KEY,
                folder TEXT,
                repo TEXT,
                worktree TEXT,
                branch TEXT,
                facts TEXT NOT NULL DEFAULT '{}',
                created_at INTEGER NOT NULL,
                last_used_at INTEGER NOT NULL,
                id TEXT
            )"""


def fresh(name):
    """A new database file, with no leftovers from an earlier run."""
    path = os.path.join(HERE, name)
    for suffix in ("", "-wal", "-shm", "-journal", ".gz", "-wal.gz"):
        if os.path.exists(path + suffix):
            os.remove(path + suffix)
    return path


def add(conn, name, session_id, when=1700000000, facts="{}"):
    conn.execute(
        "INSERT INTO sessions (name, facts, created_at, last_used_at, id) VALUES (?, ?, ?, ?, ?)",
        (name, facts, when, when, session_id),
    )


# plain.db: rows with ids, one from before ids (no id), and a table whose
# INTEGER PRIMARY KEY is the rowid.
path = fresh("plain.db")
conn = sqlite3.connect(path)
conn.execute(SESSIONS)
add(conn, "brave-peach", "3f9a0c1d2b4e")
add(conn, "api-server", "a1b2c3d4e5f6")
add(conn, "notes", None)
conn.execute("CREATE TABLE numbered (n INTEGER PRIMARY KEY, label TEXT, score REAL, raw BLOB)")
conn.execute("INSERT INTO numbered VALUES (7, 'seven', 7.5, x'00ff'), (-3, NULL, -0.25, NULL)")
conn.commit()
conn.close()
pack(path)

# altered.db: the id column added after rows were written, as zellij adds it to
# an index from before ids. Those rows carry no value for it.
path = fresh("altered.db")
conn = sqlite3.connect(path)
conn.execute(SESSIONS.replace(",\n                id TEXT", ""))
conn.execute("INSERT INTO sessions (name, created_at, last_used_at) VALUES ('old-one', 1, 1)")
conn.execute("ALTER TABLE sessions ADD COLUMN id TEXT")
add(conn, "new-one", "0123456789ab")
conn.commit()
conn.close()
pack(path)

# wal.db: the first rows checkpointed into the database, then two transactions
# only in the write-ahead log: a new session, then a rename. The files are
# copied while the connection is open, so the log keeps them.
path = fresh("wal.db")
conn = sqlite3.connect(path, isolation_level=None)
conn.execute("PRAGMA journal_mode=WAL")
conn.execute("PRAGMA wal_autocheckpoint=0")
conn.execute(SESSIONS)
add(conn, "first", "aaaaaaaaaaaa")
conn.execute("PRAGMA wal_checkpoint(TRUNCATE)")
conn.execute("BEGIN")
add(conn, "second", "bbbbbbbbbbbb")
conn.execute("COMMIT")
conn.execute("BEGIN")
conn.execute("UPDATE sessions SET name = 'first-renamed' WHERE id = 'aaaaaaaaaaaa'")
conn.execute("COMMIT")
for suffix in ("", "-wal"):
    shutil.copyfile(path + suffix, path + ".copy" + suffix)
conn.close()
for suffix in ("", "-wal"):
    os.replace(path + ".copy" + suffix, path + suffix)
pack(path)

# big.db: small pages, many rows and long values, so the table has interior
# pages and records that run onto overflow pages.
path = fresh("big.db")
conn = sqlite3.connect(path)
conn.execute("PRAGMA page_size=1024")
conn.execute(SESSIONS)
for i in range(300):
    add(conn, "session-%04d" % i, "%012x" % i, facts='{"note":"%s"}' % ("x" * (i % 5) * 200))
add(conn, "long-facts", "ffffffffffff", facts='{"note":"%s"}' % ("y" * 5000))
conn.commit()
conn.close()
pack(path)

# large-pages.db: 65536-byte pages, which the header writes as 1.
path = fresh("large-pages.db")
conn = sqlite3.connect(path)
conn.execute("PRAGMA page_size=65536")
conn.execute(SESSIONS)
add(conn, "roomy", "cccccccccccc")
conn.commit()
conn.close()
pack(path)

# utf16.db: text stored as UTF-16, which the reader refuses.
path = fresh("utf16.db")
conn = sqlite3.connect(path)
conn.execute("PRAGMA encoding='UTF-16le'")
conn.execute(SESSIONS)
add(conn, "wide", "dddddddddddd")
conn.commit()
conn.close()
pack(path)
