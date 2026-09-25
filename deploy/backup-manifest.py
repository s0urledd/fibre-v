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

A file observer-archive has rotated is its archive plus its live file:
archive/<file>/ holds gzip segments of the older lines and index.json,
which says where the live file starts in the record (its base) by the
SHA-256 of its first line. The cut carries, per such file, the base and
every segment up to it (name, logical range, lines, digests of the lines
and of the gzip file); `records` stays the live file's own count and
`archived_records` is the rest. The cut and the copy hold archive/.lock
shared, so no rotation happens under them; segments never change once
written. `verify` checks every segment the cut names, whole, and that the
restored index places the live file at the cut's base.

  write    <data-dir> <manifest.json>      cut + manifest, files untouched
  snapshot <data-dir> <dest-dir>           cut + manifest + trimmed copies
  verify   <restored-dir> [manifest.json]  trim, hash, parse, count, state;
                                           exit 1 on any fault
  show     <manifest.json>                 one line per file
  cat      <data-dir> <file>               the whole record of one file,
                                           archived lines first, to stdout
  end      <data-dir> <file>               its logical length (base + live)
"""
import fcntl
import gzip
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
    # a publication the prober's load policy sampled out, once (it names a
    # promise, as a measurement does)
    "sampling_decisions.jsonl",
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
VERSION = 3
CHUNK = 1 << 20
ARCHIVE = "archive"


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


def head_sha(path):
    """SHA-256 of the file's first line, newline included; "" when it has
    no complete line. It is how index.json names a live file."""
    h = hashlib.sha256()
    try:
        with open(path, "rb") as f:
            while True:
                b = f.read(CHUNK)
                if not b:
                    return ""
                i = b.find(b"\n")
                if i >= 0:
                    h.update(b[:i + 1])
                    return h.hexdigest()
                h.update(b)
    except FileNotFoundError:
        return ""


def archive_index(data_dir, name):
    try:
        with open(os.path.join(data_dir, ARCHIVE, name, "index.json")) as f:
            return json.load(f)
    except FileNotFoundError:
        return None


def live_base(data_dir, name, idx=None):
    """The logical offset the live file starts at: the base of the newest
    generation whose first line is the live file's, 0 for a file never
    archived. None when the index has generations and none is this file."""
    idx = idx if idx is not None else archive_index(data_dir, name)
    gens = (idx or {}).get("generations") or []
    if not gens:
        return 0
    head = head_sha(os.path.join(data_dir, name))
    for g in reversed(gens):
        if head and g.get("head_sha256") == head:
            return int(g["base"])
    return None


def archive_cut(data_dir, name):
    """The archived part of one file at the cut: the live file's base and
    every segment below it, in order, without a gap. None for a file never
    archived."""
    idx = archive_index(data_dir, name)
    if idx is None or not idx.get("generations"):
        return None
    base = live_base(data_dir, name, idx)
    if base is None:
        raise RecordError(f"{name}: the live file's first line matches no generation in {ARCHIVE}/{name}/index.json")
    segs, at = [], 0
    for s in sorted(idx.get("segments") or [], key=lambda s: s["from"]):
        if s["to"] > base:
            continue  # written by a run that never swapped; the next run drops it
        if s["from"] != at:
            raise RecordError(f"{name}: archive segments leave a gap at logical byte {at}")
        at = s["to"]
        segs.append({k: s[k] for k in ("name", "from", "to", "lines", "sha256", "gz_sha256", "gz_bytes")})
    if at != base:
        raise RecordError(f"{name}: archive segments end at {at}, the live file starts at {base}")
    return {"base": base, "segments": segs, "records": sum(s["lines"] for s in segs)}


class ArchiveLock:
    """archive/.lock held shared: observer-archive holds it exclusively for
    a run, so no rotation happens under a cut or a copy. The directory is
    made (owned like the data dir) when missing, so a first rotation cannot
    begin under a cut either."""

    def __init__(self, data_dir):
        self.data_dir, self.f = data_dir, None

    def __enter__(self):
        d = os.path.join(self.data_dir, ARCHIVE)
        if not os.path.isdir(d):
            try:
                os.makedirs(d, exist_ok=True)
                st = os.stat(self.data_dir)
                if os.geteuid() == 0:
                    os.chown(d, st.st_uid, st.st_gid)
            except OSError:
                return self  # a read-only copy: nothing rotates it
        p = os.path.join(d, ".lock")
        try:
            new = not os.path.exists(p)
            self.f = open(p, "a")
            if new and os.geteuid() == 0:
                st = os.stat(self.data_dir)
                os.chown(p, st.st_uid, st.st_gid)
            fcntl.flock(self.f, fcntl.LOCK_SH)
        except OSError:
            self.f = None
        return self

    def __exit__(self, *exc):
        if self.f:
            self.f.close()


def iter_record(data_dir, name, live_length=None):
    """The bytes of one file's whole record: its archived segments up to the
    live file's base, then the live file (its first live_length bytes when
    given)."""
    a = archive_cut(data_dir, name)
    for s in (a or {}).get("segments", []):
        with gzip.open(os.path.join(data_dir, ARCHIVE, name, s["name"]), "rb") as z:
            while True:
                b = z.read(CHUNK)
                if not b:
                    break
                yield b
    p = os.path.join(data_dir, name)
    if not os.path.exists(p):
        return
    left = live_length
    with open(p, "rb") as f:
        while left is None or left > 0:
            b = f.read(CHUNK if left is None else min(CHUNK, left))
            if not b:
                break
            if left is not None:
                left -= len(b)
            yield b


def verify_archive(restored, name, info):
    """Every segment the cut names is in the copy, whole: the gzip file's
    digest and size, and what it decompresses to (digest, length, lines);
    and the copy's index places the live file at the cut's base."""
    a = info.get("archive")
    if not a:
        return []
    problems = []
    for s in a["segments"]:
        p = os.path.join(restored, ARCHIVE, name, s["name"])
        if not os.path.exists(p):
            problems.append(f"{name}: archive segment {s['name']} missing")
            continue
        gh = hashlib.sha256()
        with open(p, "rb") as f:
            for b in iter(lambda: f.read(CHUNK), b""):
                gh.update(b)
        if gh.hexdigest() != s["gz_sha256"] or os.path.getsize(p) != s["gz_bytes"]:
            problems.append(f"{name}: archive segment {s['name']} differs from the cut (gzip digest or size)")
            continue
        h, n, lines = hashlib.sha256(), 0, 0
        try:
            with gzip.open(p, "rb") as z:
                for b in iter(lambda: z.read(CHUNK), b""):
                    h.update(b)
                    n += len(b)
                    lines += b.count(b"\n")
        except (OSError, EOFError) as e:
            problems.append(f"{name}: archive segment {s['name']}: {e}")
            continue
        if h.hexdigest() != s["sha256"] or n != s["to"] - s["from"] or lines != s["lines"]:
            problems.append(f"{name}: archive segment {s['name']} does not decompress to the lines the cut names")
    if problems:
        return problems
    try:
        base = live_base(restored, name)
    except (OSError, ValueError) as e:
        return [f"{name}: {ARCHIVE}/{name}/index.json: {e}"]
    if base != a["base"]:
        problems.append(f"{name}: the copy's index places the live file at {base}, the cut at {a['base']}")
    return problems


def cut(data_dir):
    """The consistent cut: state first, then every file's length in order,
    then hashed and parsed. Raises RecordError on a record that is not one."""
    with ArchiveLock(data_dir):
        return cut_locked(data_dir)


def cut_locked(data_dir):
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
        a = archive_cut(data_dir, name)
        if a:
            files[name]["archive"] = a
            files[name]["archived_records"] = a["records"]
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
    with ArchiveLock(data_dir):
        m = cut_locked(data_dir)
        copy_cut(data_dir, dest, m)
    dump(m, os.path.join(dest, MANIFEST))
    return m


def copy_cut(data_dir, dest, m):
    for name, info in m["files"].items():
        a = info.get("archive")
        if a:
            # segments never change once written: copied whole, with the
            # index that places the live file at the cut's base
            os.makedirs(os.path.join(dest, ARCHIVE, name), exist_ok=True)
            for s in a["segments"]:
                shutil.copyfile(os.path.join(data_dir, ARCHIVE, name, s["name"]), os.path.join(dest, ARCHIVE, name, s["name"]))
            shutil.copyfile(os.path.join(data_dir, ARCHIVE, name, "index.json"), os.path.join(dest, ARCHIVE, name, "index.json"))
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
        else:
            problems.extend(verify_archive(restored, name, info))
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
        archived = ""
        if info.get("archive"):
            a = info["archive"]
            archived = f" + {a['records']} archived in {len(a['segments'])} segment(s) (base {a['base']})"
        print(f"  {name:<24} {info['bytes']:>12} bytes {info['records']:>9} records {info['sha256'][:16]}{archived}")


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
        elif cmd == "cat" and len(argv) > 3:
            with ArchiveLock(argv[2]):
                p = os.path.join(argv[2], argv[3])
                length = complete_length(p) if os.path.exists(p) else None
                for b in iter_record(argv[2], argv[3], length):
                    sys.stdout.buffer.write(b)
        elif cmd == "end" and len(argv) > 3:
            p = os.path.join(argv[2], argv[3])
            base = live_base(argv[2], argv[3])
            print((base or 0) + (os.path.getsize(p) if os.path.exists(p) else 0))
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
