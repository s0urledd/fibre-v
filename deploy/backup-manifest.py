#!/usr/bin/env python3
"""
backup-manifest: a consistent cut of the observer's record, and the proof
that a copy of it came back whole.

The record is append-only JSONL written by several processes. A backup
taken by copying files one after another is not one moment: publications
copied at 03:00:00 and measurements at 03:00:04 disagree about what
existed. This tool takes the cut first (the byte length of every file, in
dependency order: what refers to a record is cut before the record it
refers to), hashes exactly those bytes, counts the records in them, notes
the scanner's checkpoint, and writes manifest.json. The copy may then be
taken at leisure and may be longer than the cut — the files only grow — and
`verify` trims each restored file back to the manifest's length and checks
the hash and the record count of what is left. Anything missing, shorter,
or different fails.

state.json is not append-only (the scanner rewrites it every checkpoint),
so it is recorded by its checkpoint, and a restored state.json must be at
or past it.

The sampling master key must never be in a copy; `verify` fails if it is.

  write    <data-dir> <manifest.json>      cut + manifest, files untouched
  snapshot <data-dir> <dest-dir>           cut + manifest + trimmed copies
  verify   <restored-dir> [manifest.json]  trim, hash, count; exit 1 on any fault
  show     <manifest.json>                 one line per file
"""
import hashlib
import json
import os
import shutil
import sys
import time

# Dependents first: a line in measurements.jsonl names a promise that
# publications.jsonl must already hold once both cuts are taken.
RECORD_FILES = [
    "measurements.jsonl",
    "reachability.jsonl",
    "amendments.jsonl",
    "corrections.jsonl",
    "sampling-secrets.jsonl",
    "registry.jsonl",
    "runs.jsonl",
    "host_history.jsonl",
    "param_uncertainty.jsonl",
    "payments.jsonl",
    "publications.jsonl",
]
STATE = "state.json"
FORBIDDEN = ["sampling-master.key"]
MANIFEST = "manifest.json"
CHUNK = 1 << 20


def sha_count(path, length):
    """sha256 and newline count of the first `length` bytes."""
    h = hashlib.sha256()
    n = 0
    left = length
    with open(path, "rb") as f:
        while left > 0:
            b = f.read(min(CHUNK, left))
            if not b:
                break
            h.update(b)
            n += b.count(b"\n")
            left -= len(b)
    if left > 0:
        raise IOError(f"{path}: wanted {length} bytes, file is {length - left}")
    return h.hexdigest(), n


def checkpoint(path):
    try:
        s = json.load(open(path))
    except Exception:
        return None
    return {
        "last_scanned_height": int(s.get("last_scanned_height") or 0),
        "last_scanned_time": s.get("last_scanned_time"),
        "gaps": len(s.get("gaps") or []),
    }


def cut(data_dir):
    """The consistent cut: every file's length, taken in order, then hashed."""
    lengths = []
    for name in RECORD_FILES:
        p = os.path.join(data_dir, name)
        if os.path.exists(p):
            lengths.append((name, os.path.getsize(p)))
    files = {}
    for name, length in lengths:
        digest, records = sha_count(os.path.join(data_dir, name), length)
        files[name] = {"bytes": length, "sha256": digest, "records": records}
    m = {
        "version": 1,
        "taken_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
        "data_dir": os.path.abspath(data_dir),
        "files": files,
        "checkpoint": checkpoint(os.path.join(data_dir, STATE)),
    }
    m["id"] = hashlib.sha256(json.dumps({"files": files, "checkpoint": m["checkpoint"]}, sort_keys=True).encode()).hexdigest()[:16]
    return m


def write(data_dir, out):
    m = cut(data_dir)
    tmp = out + ".tmp"
    with open(tmp, "w") as f:
        json.dump(m, f, indent=1, sort_keys=True)
        f.write("\n")
    os.replace(tmp, out)
    return m


def snapshot(data_dir, dest):
    os.makedirs(dest, exist_ok=True)
    m = cut(data_dir)
    for name, info in m["files"].items():
        src = os.path.join(data_dir, name)
        dst = os.path.join(dest, name)
        with open(src, "rb") as i, open(dst, "wb") as o:
            left = info["bytes"]
            while left > 0:
                b = i.read(min(CHUNK, left))
                if not b:
                    break
                o.write(b)
                left -= len(b)
    st = os.path.join(data_dir, STATE)
    if os.path.exists(st):
        shutil.copyfile(st, os.path.join(dest, STATE))
    with open(os.path.join(dest, MANIFEST), "w") as f:
        json.dump(m, f, indent=1, sort_keys=True)
        f.write("\n")
    return m


def verify(restored, manifest_path=None):
    manifest_path = manifest_path or os.path.join(restored, MANIFEST)
    problems = []
    try:
        m = json.load(open(manifest_path))
    except Exception as e:
        return [f"manifest {manifest_path}: {e}"], None
    for bad in FORBIDDEN:
        if os.path.exists(os.path.join(restored, bad)):
            problems.append(f"{bad} is in the copy: it must never leave the host")
    trimmed = 0
    for name, info in sorted(m["files"].items()):
        p = os.path.join(restored, name)
        if not os.path.exists(p):
            problems.append(f"{name}: missing (manifest has {info['bytes']} bytes, {info['records']} records)")
            continue
        size = os.path.getsize(p)
        if size < info["bytes"]:
            problems.append(f"{name}: truncated, {size} bytes of {info['bytes']}")
            continue
        if size > info["bytes"]:
            # the copy was taken after the cut; keep exactly the cut
            with open(p, "r+b") as f:
                f.truncate(info["bytes"])
            trimmed += size - info["bytes"]
        digest, records = sha_count(p, info["bytes"])
        if digest != info["sha256"]:
            problems.append(f"{name}: sha256 differs over the first {info['bytes']} bytes (content changed or not this backup)")
        elif records != info["records"]:
            problems.append(f"{name}: {records} records, manifest says {info['records']}")
    cp = m.get("checkpoint")
    if cp:
        rc = checkpoint(os.path.join(restored, STATE))
        if rc is None:
            problems.append(f"{STATE}: missing or unreadable (manifest checkpoint at height {cp['last_scanned_height']})")
        elif rc["last_scanned_height"] < cp["last_scanned_height"]:
            problems.append(f"{STATE}: checkpoint {rc['last_scanned_height']} is before the manifest's {cp['last_scanned_height']}")
    m["_trimmed_bytes"] = trimmed
    return problems, m


def show(m):
    print(f"manifest {m['id']} taken {m['taken_at']} from {m['data_dir']}")
    if m.get("checkpoint"):
        print(f"  checkpoint: height {m['checkpoint']['last_scanned_height']} ({m['checkpoint'].get('last_scanned_time')}), {m['checkpoint']['gaps']} gap(s)")
    for name, info in sorted(m["files"].items()):
        print(f"  {name:<24} {info['bytes']:>12} bytes {info['records']:>9} records {info['sha256'][:16]}")


def main(argv):
    if len(argv) < 3:
        print(__doc__.strip(), file=sys.stderr)
        return 2
    cmd = argv[1]
    if cmd == "write":
        m = write(argv[2], argv[3])
        show(m)
    elif cmd == "snapshot":
        m = snapshot(argv[2], argv[3])
        show(m)
    elif cmd == "verify":
        problems, m = verify(argv[2], argv[3] if len(argv) > 3 else None)
        if m:
            show(m)
            if m.get("_trimmed_bytes"):
                print(f"  trimmed {m['_trimmed_bytes']} bytes appended after the cut")
        for p in problems:
            print(f"  FAIL {p}")
        if problems:
            return 1
        print("verify: every file matches the manifest")
    elif cmd == "show":
        show(json.load(open(argv[2])))
    else:
        print(__doc__.strip(), file=sys.stderr)
        return 2
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
