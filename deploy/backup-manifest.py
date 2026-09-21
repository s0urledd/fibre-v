#!/usr/bin/env python3
"""
backup-manifest: a consistent cut of the observer's record, and the proof
that a copy of it came back whole.

The record is append-only JSONL written by several processes, plus
state.json, which the scanner rewrites at every checkpoint. A backup taken
by copying files one after another is not one moment: publications copied
at 03:00:00 and measurements at 03:00:04 disagree about what existed, and a
state.json copied last points past the records copied first. This tool
takes one cut, in this order:

  1. state.json, read whole, first. The scanner fsyncs its record files
     before it replaces state.json, so a record file read after the state
     holds everything the state's checkpoint covers.
  2. the byte length of every record file, dependents before what they
     refer to, each cut back to the end of its last complete line — a
     writer may be in the middle of a line at that instant.
  3. the SHA-256 of exactly those bytes, with every line in them parsed as
     a JSON record and counted. A line that is not one fails the cut.

The manifest carries the hashes, the counts, the checkpoint, and the bytes
of state.json as they were. The copy may then be taken at leisure and may
be longer than the cut — the files only grow — and `verify` trims each
restored file back to the manifest's length, checks the hash, parses and
counts the records, and puts the cut's own state.json in place of the
copy's. The copy's state.json was taken later and points past the records
the cut holds; a scanner resuming from it would skip the blocks in between
for good, and pulling only its height back would leave every other field
(gaps, param history, host history, reconcile cursors) from a later
moment. Anything missing, shorter, different or unparseable fails.

The sampling master key must never be in a copy; `verify` fails if it is.

  write    <data-dir> <manifest.json>      cut + manifest, files untouched
  snapshot <data-dir> <dest-dir>           cut + manifest + trimmed copies
  verify   <restored-dir> [manifest.json]  trim, hash, parse, count, state;
                                           exit 1 on any fault
  show     <manifest.json>                 one line per file
"""
import hashlib
import json
import os
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
VERSION = 2
CHUNK = 1 << 20


class RecordError(Exception):
    """A record file that is not a sequence of complete JSON lines."""


def complete_length(path):
    """The length of the file up to and including its last newline: the
    bytes that hold only complete lines. A writer can be mid-line at the
    instant the cut is taken; those bytes belong to the next cut."""
    size = os.path.getsize(path)
    with open(path, "rb") as f:
        end = size
        while end > 0:
            start = max(0, end - CHUNK)
            f.seek(start)
            b = f.read(end - start)
            i = b.rfind(b"\n")
            if i >= 0:
                return start + i + 1
            end = start
    return 0


def sha_records(name, path, length):
    """SHA-256 of the first `length` bytes, and the number of JSON records
    in them. Every line must parse as a JSON object and the cut must end
    on a line boundary; anything else raises RecordError."""
    h = hashlib.sha256()
    n = 0
    left = length
    rest = b""
    with open(path, "rb") as f:
        while left > 0:
            b = f.read(min(CHUNK, left))
            if not b:
                break
            h.update(b)
            left -= len(b)
            lines = (rest + b).split(b"\n")
            rest = lines.pop()
            for line in lines:
                n += 1
                if not line.strip():
                    raise RecordError(f"{name}: line {n} is empty, not a record")
                try:
                    rec = json.loads(line)
                except ValueError:
                    raise RecordError(f"{name}: line {n} is not a complete JSON record")
                if not isinstance(rec, dict):
                    raise RecordError(f"{name}: line {n} is not a JSON object")
    if left > 0:
        raise RecordError(f"{name}: wanted {length} bytes, file is {length - left}")
    if rest:
        raise RecordError(f"{name}: the cut ends inside record {n + 1}, not on a line boundary")
    return h.hexdigest(), n


def read_state(path):
    try:
        with open(path, "rb") as f:
            return f.read()
    except OSError:
        return None


def checkpoint_of(raw):
    if raw is None:
        return None
    try:
        s = json.loads(raw)
    except ValueError:
        return None
    return {
        "last_scanned_height": int(s.get("last_scanned_height") or 0),
        "last_scanned_time": s.get("last_scanned_time"),
        "gaps": len(s.get("gaps") or []),
    }


def cut(data_dir):
    """The consistent cut: state first, then every file's length in order,
    then hashed and parsed. Raises RecordError on a record that is not one."""
    state_raw = read_state(os.path.join(data_dir, STATE))
    lengths = []
    for name in RECORD_FILES:
        p = os.path.join(data_dir, name)
        if os.path.exists(p):
            lengths.append((name, complete_length(p)))
    files = {}
    for name, length in lengths:
        digest, records = sha_records(name, os.path.join(data_dir, name), length)
        files[name] = {"bytes": length, "sha256": digest, "records": records}
    state = None
    if state_raw is not None:
        state = {"bytes": len(state_raw), "sha256": hashlib.sha256(state_raw).hexdigest()}
    m = {
        "version": VERSION,
        "taken_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
        "data_dir": os.path.abspath(data_dir),
        "files": files,
        "state": state,
        "checkpoint": checkpoint_of(state_raw),
    }
    m["id"] = hashlib.sha256(json.dumps({"files": files, "state": state, "checkpoint": m["checkpoint"]}, sort_keys=True).encode()).hexdigest()[:16]
    if state_raw is not None:
        # the bytes themselves, so a restore can put this state back beside
        # the records it was cut with
        m["state_raw"] = state_raw.decode("utf-8")
    return m


def dump(m, path):
    tmp = path + ".tmp"
    with open(tmp, "w") as f:
        json.dump(m, f, indent=1, sort_keys=True)
        f.write("\n")
    os.replace(tmp, path)


def write(data_dir, out):
    m = cut(data_dir)
    dump(m, out)
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
    if m.get("state_raw") is not None:
        # the state as it was at the cut, not as it is now
        with open(os.path.join(dest, STATE), "w") as f:
            f.write(m["state_raw"])
    dump(m, os.path.join(dest, MANIFEST))
    return m


def verify(restored, manifest_path=None):
    manifest_path = manifest_path or os.path.join(restored, MANIFEST)
    problems = []
    notes = []
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
        try:
            digest, records = sha_records(name, p, info["bytes"])
        except RecordError as e:
            problems.append(str(e))
            continue
        if digest != info["sha256"]:
            problems.append(f"{name}: sha256 differs over the first {info['bytes']} bytes (content changed or not this backup)")
        elif records != info["records"]:
            problems.append(f"{name}: {records} records, manifest says {info['records']}")
    cp = m.get("checkpoint")
    sp = os.path.join(restored, STATE)
    if m.get("state_raw") is not None:
        # The cut's own state.json goes beside the cut's records, whatever
        # the copy carried: the copy's was read later and points past
        # them, and only the whole file is consistent with the cut.
        raw = m["state_raw"].encode("utf-8")
        st = m.get("state") or {}
        if hashlib.sha256(raw).hexdigest() != st.get("sha256") or checkpoint_of(raw) != cp:
            problems.append(f"{STATE}: the manifest's copy does not match its own hash or checkpoint (manifest altered)")
        else:
            have = read_state(sp)
            if have is None:
                with open(sp, "wb") as f:
                    f.write(raw)
                notes.append(f"{STATE}: not in the copy; written from the cut (height {cp['last_scanned_height'] if cp else '?'})")
            elif have != raw:
                hc = checkpoint_of(have)
                with open(sp, "wb") as f:
                    f.write(raw)
                notes.append(f"{STATE}: the copy's (height {hc['last_scanned_height'] if hc else '?'}) is not the cut's; replaced with the cut's (height {cp['last_scanned_height'] if cp else '?'})")
    elif cp:
        # a manifest from before the state was carried: it can check the
        # copy's state.json but not repair it, and a state past the cut
        # would resume the scanner past records this cut does not hold
        rc = checkpoint_of(read_state(sp))
        if rc is None:
            problems.append(f"{STATE}: missing or unreadable (manifest checkpoint at height {cp['last_scanned_height']})")
        elif rc["last_scanned_height"] != cp["last_scanned_height"]:
            problems.append(f"{STATE}: checkpoint {rc['last_scanned_height']} is not the manifest's {cp['last_scanned_height']}; this manifest carries no state.json to restore, take the backup again with the current tool")
    m["_trimmed_bytes"] = trimmed
    m["_notes"] = notes
    return problems, m


def show(m):
    print(f"manifest {m['id']} taken {m['taken_at']} from {m['data_dir']} (v{m.get('version', 1)})")
    if m.get("checkpoint"):
        st = m.get("state") or {}
        carried = f", {st['bytes']} bytes carried" if st else ""
        print(f"  checkpoint: height {m['checkpoint']['last_scanned_height']} ({m['checkpoint'].get('last_scanned_time')}), {m['checkpoint']['gaps']} gap(s){carried}")
    for name, info in sorted(m["files"].items()):
        print(f"  {name:<24} {info['bytes']:>12} bytes {info['records']:>9} records {info['sha256'][:16]}")


def main(argv):
    if len(argv) < 3:
        print(__doc__.strip(), file=sys.stderr)
        return 2
    cmd = argv[1]
    try:
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
                for n in m.get("_notes", []):
                    print(f"  {n}")
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
    except RecordError as e:
        print(f"  FAIL {e}")
        print(f"{cmd}: the record is not a sequence of complete JSON lines; nothing written")
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
