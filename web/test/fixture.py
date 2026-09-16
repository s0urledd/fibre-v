#!/usr/bin/env python3
"""Generate a design-evaluation fixture for the Fibre observer store.

Purpose: the devnet has four validators and two publications, which is far too
little to judge a dashboard on. Real questions the design must survive -- does a
moniker column hold a 34-character name, does the worst-first sort surface the
right rows, does an eleven-class legend stay readable when nine of the classes
are actually present -- only show up at scale.

This writes into a COPY of the schema the real store creates, never into a live
database, and every row is synthetic. It is a local design aid; nothing it
produces is ever published.
"""
import hashlib, json, os, random, shutil, sqlite3, sys
from datetime import datetime, timedelta, timezone

# The schema is created by the real collector rather than copied from a
# database lying around, so this can never drift from the shipped migrations:
# `observer-collector -once` applies the baseline and every migration, then
# exits. Point OBSERVER_COLLECTOR at the binary if it is not on PATH or in the
# repo's bin directory.
def make_schema(path):
    import subprocess
    here = os.path.dirname(os.path.abspath(__file__))
    candidates = [os.environ.get("OBSERVER_COLLECTOR"),
                  os.path.join(here, "..", "..", "fibre-sentinel", "bin", "observer-collector"),
                  shutil.which("observer-collector")]
    binary = next((c for c in candidates if c and os.path.exists(c)), None)
    if not binary:
        sys.exit("observer-collector not found; run `make build` or set OBSERVER_COLLECTOR")
    os.makedirs(os.path.dirname(path), exist_ok=True)
    # An unreachable RPC is deliberate: the collector logs the failure, applies
    # the schema and exits 0. Nothing here should touch a real chain.
    subprocess.run([binary, "-once", "-db", path, "-data-dir", os.path.dirname(path),
                    "-rpc", "http://127.0.0.1:1"], check=True,
                   stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=120)

OUT = sys.argv[1] if len(sys.argv) > 1 else "/tmp/fibre-fixture/observer.db"
rnd = random.Random(51)   # CIP-51. Deterministic: same fixture every run.

NOW = datetime(2026, 9, 16, 10, 30, tzinfo=timezone.utc)
def ts(dt): return dt.strftime("%Y-%m-%dT%H:%M:%S.%f")[:-3] + "Z"

# Moniker set chosen for SHAPE, not realism: the design has to survive a
# three-character name and a thirty-four-character one in the same column, plus
# non-ASCII, digits, punctuation and inconsistent casing, because real validator
# sets contain all of those.
MONIKERS = [
    "P2P", "Kiln", "Figment", "Chorus One", "Everstake", "Luganodes",
    "Stakin Institutional Operations", "Cosmostation", "Imperator.co",
    "polkachu", "NODEJUMPER", "Lavender.Five Nodes 🐝", "StakeLab",
    "Nodes.Guru", "Forbole", "Citadel.one", "A41", "DSRV", "B-Harvest",
    "Stakewolle | Community Node", "Enigma", "宇宙节点 Cosmos Node",
    "01node", "Blockscope", "Crosnest", "Golden Ratio Staking", "Huginn Tech",
    "KalpaTech", "Lunar Digital Assets", "Mandragora", "Nocturnal Labs",
    "Ontropy", "Orbital Command", "POSTHUMAN ꝏ DVS", "Qubelabs", "RHINO",
    "Simply Staking", "SmartStake", "Stake&Relax 🦥", "Staketab", "Synergy",
    "TheGrandSlam", "Umbrella ☔", "Validatus", "WhisperNode 🤐", "Witval",
    "ZKV", "brochain", "chainflow", "cryptech", "d-collective", "easy2stake",
    "freshSTAKING", "genznodes", "hashkey", "itrocket", "jabbey", "kjnodes",
    "moonli.me", "nodeist",
]
N = len(MONIKERS)

def cons_addr(i):
    return hashlib.sha256(f"fixture-cons-{i}".encode()).hexdigest()[:40]

def bech32ish(prefix, i):
    # Not a real bech32; the fixture never leaves this machine and the design
    # only cares about the string's shape and length.
    body = hashlib.sha256(f"{prefix}-{i}".encode()).hexdigest()[:38]
    return f"{prefix}1{body}"

# --- validator population -------------------------------------------------
# Voting power follows a steep power law, the way a real active set does: the
# top validator holds roughly two orders of magnitude more than the tail. That
# matters because assigned rows scale with power, so the table's "rows" column
# spans a wide range and the design has to keep it aligned.
vals = []
for i in range(N):
    power = int(9_000_000 * (0.90 ** i)) + rnd.randint(1000, 60_000)
    vals.append({
        "i": i, "moniker": MONIKERS[i], "cons": cons_addr(i),
        "operator": bech32ish("celestiavaloper", i),
        "consbech": bech32ish("celestiavalcons", i),
        "power": power,
        "jailed": i in (37, 52),
        "identity": hashlib.sha256(f"kb{i}".encode()).hexdigest()[:16].upper(),
        "host": f"fibre{i:02d}.example.net:7980",
    })
total_power = sum(v["power"] for v in vals)

# Behaviour classes. Deliberately skewed to healthy: an observer whose fixture
# is half-broken teaches the designer to optimise for a network that does not
# exist. The interesting cases are rare and must still be findable.
# hours before NOW at which each impairment began; None means "always".
OUTAGE_SINCE = {5: 30.0, 38: 9.0, 28: 40.0, 33: 18.0}

def impaired(i, at):
    """True if validator i's impairment was already in effect at time `at`."""
    if i not in OUTAGE_SINCE:
        return True
    since = OUTAGE_SINCE[i]
    if since is None:
        return True
    return at >= NOW - timedelta(hours=since)

BEHAVIOUR = {}
for v in vals:
    i = v["i"]
    if i == 23:                 BEHAVIOUR[i] = "faulty"        # real, repeated FAULT
    elif i in (14, 44):         BEHAVIOUR[i] = "flaky"         # occasional FAULT
    elif i in (5, 38):          BEHAVIOUR[i] = "unreachable"   # outage
    elif i == 47:               BEHAVIOUR[i] = "unregistered"  # no host on chain
    elif i == 28:               BEHAVIOUR[i] = "identity"      # certificate lapsed
    elif i == 41:               BEHAVIOUR[i] = "unattested"    # never signed
    elif i == 33:               BEHAVIOUR[i] = "prunes_early"  # drops late in window
    elif i in (37, 52):         BEHAVIOUR[i] = "jailed"
    else:                       BEHAVIOUR[i] = "healthy"

shutil.rmtree(os.path.dirname(OUT), ignore_errors=True)
make_schema(OUT)
db = sqlite3.connect(OUT)
db.execute("PRAGMA journal_mode=DELETE")
for t in ("publications","assignments","probes","reachability","endpoints",
          "validator_identities","observer_runs","meta","params_history","ingest_cursors"):
    db.execute(f"DELETE FROM {t}")

PAYMENT_TIMEOUT, RETENTION = 3600, 14400   # 1h / 4h, the spec's default shape
MSU = max(PAYMENT_TIMEOUT, RETENTION)

db.execute("INSERT INTO params_history VALUES (?,?,?,?,?,?,?,?)",
           (1, 0, "genesis", 604800, PAYMENT_TIMEOUT, 100, RETENTION, 1<<40))

for v in vals:
    db.execute("""INSERT INTO validator_identities
        (cons_address, operator_address, moniker, identity, website, tokens,
         jailed, status, first_seen_at, updated_at)
        VALUES (?,?,?,?,?,?,?,?,?,?)""",
        (v["cons"], v["operator"], v["moniker"], v["identity"],
         f"https://{v['moniker'].split()[0].lower().strip('.')}.example",
         v["power"], 1 if v["jailed"] else 0,
         "BOND_STATUS_BONDED", ts(NOW - timedelta(days=30)), ts(NOW)))
    if BEHAVIOUR[v["i"]] != "unregistered":
        db.execute("""INSERT INTO endpoints
            (validator_cons_address, host, first_seen_at, first_seen_height,
             last_seen_at, last_seen_height, closed_at, closed_height, closed_reason)
            VALUES (?,?,?,?,?,?,NULL,NULL,'')""",
            (v["consbech"], v["host"], ts(NOW - timedelta(days=30)), 100,
             ts(NOW), 900_000))

# --- publications ---------------------------------------------------------
# Spread over seven days so the 24h / 7d / 30d window switcher has something to
# switch between, and so a "last 24h" figure differs from an all-time one.
# Publications to write. The default is enough to judge a design; set it higher
# to measure how the API scales — reconstructSample caps the network summary at
# 2000, so that is the number that shows whether an endpoint holds up.
PUBS = int(os.environ.get("FIXTURE_PUBS", "260"))
pubs = []
for p in range(PUBS):
    age_h = rnd.random() ** 1.7 * 168          # skewed toward recent
    created = NOW - timedelta(hours=age_h)
    settled = created + timedelta(seconds=rnd.randint(2, 20))
    msu = created + timedelta(seconds=MSU)
    # Blob sizes cluster small with a long tail, like real DA traffic.
    size = rnd.choice([128, 256, 512] * 6 + [1024, 2048] * 3 + [4096, 8192, 16384]) * 1024
    ph = hashlib.sha256(f"promise-{p}".encode()).hexdigest()
    cm = hashlib.sha256(f"commit-{p}".encode()).hexdigest()
    ns = "00000000000000000000000000000000000000" + hashlib.sha256(
        f"ns-{p % 9}".encode()).hexdigest()[:26]
    # Assignment: clamp(ceil(4096 * power * 3 / total), 148, 4096), the real rule.
    import math
    assigned = []
    for v in vals:
        if BEHAVIOUR[v["i"]] == "unregistered" and rnd.random() < 0.5:
            pass
        rows = min(4096, max(148, math.ceil(4096 * v["power"] * 3 / total_power)))
        assigned.append((v, rows))
    sigma = sum(r for _, r in assigned)
    # Attestation: a validator marked "unattested" is missing from the signature
    # set, which is exactly the case the taxonomy must hold out of the rate.
    # A validator with no registered Fibre host never received an upload, so it
    # cannot have signed the promise. Classify still calls it NOT_REGISTERED
    # (OutcomeNoHost is judged before the attestation check in classify.go), but
    # it is correctly absent from the signature set, which is what keeps it out
    # of the reconstructability denominator.
    def could_attest(v):
        b = BEHAVIOUR[v["i"]]
        if b in ("unattested", "unregistered"):
            return False
        if b in ("unreachable", "identity") and impaired(v["i"], created):
            return False   # already dark at upload: it never got the shard
        return True
    attested = {v["cons"]: could_attest(v) for v, _ in assigned}
    sigcount = sum(1 for a in attested.values() if a)
    pubs.append(dict(ph=ph, cm=cm, ns=ns, created=created, settled=settled,
                     msu=msu, size=size, assigned=assigned, attested=attested))
    att_rows = sum(r for v, r in assigned if attested[v["cons"]])
    att_power = sum(v["power"] for v, _ in assigned if attested[v["cons"]])
    db.execute("""INSERT INTO publications (
        promise_hash, commitment, blob_version, blob_size, namespace, chain_id,
        promise_height, creation_timestamp, signer, signer_public_key,
        validator_signature_count, settlement_height, settlement_time,
        settlement_tx_hash, settlement_tx_index, settlement_tx_code,
        must_serve_until, must_serve_until_basis, shard_retention_s,
        payment_promise_timeout_s, assignment_error,
        protocol_params_fingerprint, pinned_celestia_app, validator_set_height,
        total_voting_power, sigma_rows, distinct_rows, wrap_overlaps,
        validators_with_rows, recorded_at, raw_json, attested_with_rows,
        attested_voting_power, signature_entries, signatures_verified,
        signatures_unmatched, signatures_out_of_position
    ) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)""", (
        ph, cm, 0, size, ns, "mocha-5", 800_000 + p, ts(created),
        bech32ish("celestia", p % 17), hashlib.sha256(f"pk{p}".encode()).hexdigest(),
        sigcount, 800_000 + p + 1, ts(settled),
        hashlib.sha256(f"tx{p}".encode()).hexdigest().upper(), 0, 0,
        ts(msu), "shard_retention", RETENTION, PAYMENT_TIMEOUT, "",
        "fp", "fa5b523b", 800_000 + p, total_power, sigma, min(sigma, 16384), 0,
        len(assigned), ts(settled), json.dumps({"assignment": {"protocol_params": {"original_rows": 4096, "total_rows": 16384}}}),
        sum(1 for v, _ in assigned if attested[v["cons"]]), att_power,
        sigcount, sigcount, 0, 0))
    cursor = 0
    for v, rows in assigned:
        idx = [(cursor + k) % 16384 for k in range(rows)]
        cursor = (cursor + rows) % 16384
        db.execute("""INSERT INTO assignments
            (promise_hash, validator_address, voting_power, row_count, rows_json, attested)
            VALUES (?,?,?,?,?,?)""",
            (ph, v["cons"], v["power"], rows, json.dumps(idx), 1 if attested[v["cons"]] else 0))

# --- probes ---------------------------------------------------------------
# Four in-window points at 12/45/72/92 percent of the retention window, one
# grace probe and one post probe, which is the shipped schedule.
POINTS = [("w1", 0.12), ("w2", 0.45), ("w3", 0.72), ("w4", 0.92)]
counts = {}
def add_probe(pub, v, rows, label, at, phase, outcome, cls, **kw):
    if at > NOW:
        return
    counts[cls] = counts.get(cls, 0) + 1
    key = hashlib.sha256(f"{pub['ph']}{v['cons']}{label}".encode()).hexdigest()
    ok = cls in ("HEALTHY",)
    db.execute("""INSERT INTO probes (
        dedupe_key, vantage, promise_hash, commitment, blob_version,
        must_serve_until, validator_set_height, validator_address,
        validator_host, assigned, assigned_row_count, schedule_label,
        scheduled_at, started_at, finished_at, lateness_ms, dns_ok, dns_ms,
        tcp_ok, tcp_ms, tls_ok, tls_ms, tls_version, peer_cert_sha256,
        identity_ok, identity_reason, download_ok, download_ms, rows_returned,
        rows_expected, commitment_verified, assignment_verified, phase,
        outcome, classification, classification_reason, raw_error,
        total_duration_ms, raw_json, attested
    ) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)""", (
        key, "eu1", pub["ph"], pub["cm"], 0, ts(pub["msu"]), 800_000,
        v["cons"], kw.get("host", v["host"]), 1, rows, label, ts(at), ts(at),
        ts(at + timedelta(milliseconds=kw.get("ms", 400))), rnd.randint(0, 900),
        kw.get("dns", 1), rnd.randint(1, 30), kw.get("tcp", 1), rnd.randint(4, 60),
        kw.get("tls", 1), rnd.randint(8, 90), "TLS1.3" if kw.get("tls", 1) else "",
        hashlib.sha256(f"cert{v['i']}".encode()).hexdigest() if kw.get("tls", 1) else "",
        kw.get("idok", 1), kw.get("idreason", ""), 1 if ok else 0,
        rnd.randint(20, 800) if ok else 0, rows if ok else kw.get("got", 0), rows,
        1 if ok or kw.get("cv") else 0, 1 if ok else 0,
        phase, outcome, cls, kw.get("reason", ""), kw.get("err", ""),
        kw.get("ms", 400), "{}", kw.get("attested", 1)))

for pub in pubs:
    window = (pub["msu"] - pub["created"]).total_seconds()
    for v, rows in pub["assigned"]:
        b = BEHAVIOUR[v["i"]]
        if b == "unregistered":
            for label, frac in POINTS:
                add_probe(pub, v, rows, label, pub["created"] + timedelta(seconds=window*frac),
                          "in_window", "NO_REGISTERED_HOST", "NOT_REGISTERED", host="", dns=0, tcp=0, tls=0, idok=0,
                          reason="no Fibre host registered in x/valaddr when the probe ran")
            continue
        if not pub["attested"][v["cons"]]:
            for label, frac in POINTS:
                add_probe(pub, v, rows, label, pub["created"] + timedelta(seconds=window*frac),
                          "in_window", "SERVED_OK", "UNATTESTED", attested=0,
                          reason="the settled promise carries no verified signature from this validator")
            continue
        if b == "unreachable":
            for label, frac in POINTS:
                at = pub["created"] + timedelta(seconds=window*frac)
                if impaired(v["i"], at):
                    add_probe(pub, v, rows, label, at, "in_window", "TCP_REFUSED",
                              "UNREACHABLE", tcp=0, tls=0, idok=0, ms=0,
                              err=f"dial tcp {v['host']}: connect: connection refused")
                else:
                    add_probe(pub, v, rows, label, at, "in_window", "SERVED_OK", "HEALTHY")
            add_probe(pub, v, rows, "grace", pub["msu"] + timedelta(seconds=120),
                      "grace", "TCP_REFUSED", "UNREACHABLE", tcp=0, tls=0, idok=0, ms=0,
                      err=f"dial tcp {v['host']}: connect: connection refused")
            continue
        if b == "identity":
            for label, frac in POINTS:
                at = pub["created"] + timedelta(seconds=window*frac)
                if impaired(v["i"], at):
                    add_probe(pub, v, rows, label, at, "in_window", "IDENTITY_FAIL",
                              "IDENTITY_EXPIRED", idok=0,
                              idreason="certificate validity window has lapsed")
                else:
                    add_probe(pub, v, rows, label, at, "in_window", "SERVED_OK", "HEALTHY")
            continue
        for label, frac in POINTS:
            at = pub["created"] + timedelta(seconds=window*frac)
            if b == "prunes_early" and frac >= 0.72 and impaired(v["i"], at):
                add_probe(pub, v, rows, label, at, "in_window", "NOT_FOUND", "FAULT",
                          reason="no such shard while the promise still held")
            elif b == "faulty" and rnd.random() < 0.30:
                add_probe(pub, v, rows, label, at, "in_window", "NOT_FOUND", "FAULT",
                          reason="no such shard while the promise still held")
            elif b == "flaky" and rnd.random() < 0.05:
                add_probe(pub, v, rows, label, at, "in_window", "NOT_FOUND", "FAULT",
                          reason="no such shard while the promise still held")
            elif rnd.random() < 0.003:
                add_probe(pub, v, rows, label, at, "in_window", "TCP_TIMEOUT", "UNREACHABLE",
                          tcp=0, tls=0, idok=0, err="dial tcp: i/o timeout")
            else:
                add_probe(pub, v, rows, label, at, "in_window", "SERVED_OK", "HEALTHY")
        # grace, then post
        g = pub["msu"] + timedelta(seconds=120)
        if b in ("faulty",) and rnd.random() < 0.5:
            add_probe(pub, v, rows, "grace", g, "grace", "NOT_FOUND", "TOLERATED",
                      reason="not found just after must_serve_until, within the measured prune lag")
        else:
            add_probe(pub, v, rows, "grace", g, "grace", "SERVED_OK", "HEALTHY")
        p_at = pub["msu"] + timedelta(minutes=30)
        add_probe(pub, v, rows, "post", p_at, "post", "NOT_FOUND", "EXPECTED_GONE",
                  reason="not found after the window plus tolerance; correct behaviour")

# --- reachability heartbeats ---------------------------------------------
for v in vals:
    b = BEHAVIOUR[v["i"]]
    if b == "unregistered":
        continue
    for r in range(12):
        at = NOW - timedelta(minutes=5*r)
        up = b not in ("unreachable",)
        db.execute("""INSERT INTO reachability VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)""", (
            hashlib.sha256(f"reach{v['i']}-{r}".encode()).hexdigest(), "eu1",
            v["cons"], v["host"], 900_000 - r, ts(at), ts(at),
            1, 1 if up else 0, rnd.randint(4, 40), 1 if up else 0, rnd.randint(8, 90),
            hashlib.sha256(f"cert{v['i']}".encode()).hexdigest() if up else "",
            1 if (up and b != "identity") else 0,
            "certificate validity window has lapsed" if b == "identity" else "",
            "OK" if up else "TCP_REFUSED",
            "" if up else f"dial tcp {v['host']}: connect: connection refused",
            rnd.randint(20, 200), "{}"))

for comp in ("collector", "prober", "api", "heartbeat"):
    db.execute("""INSERT INTO observer_runs
        (component, vantage, version, started_at, last_heartbeat_at, stopped_at, stop_reason)
        VALUES (?,?,?,?,?,NULL,NULL)""",
        (comp, "eu1", "0.1.0", ts(NOW - timedelta(days=7)), ts(NOW)))

for k, val in (("chain_id","mocha-5"), ("last_scanned_height","900000"),
               ("pinned_celestia_app","fa5b523b7e3b2b83bd16bc072a45cbd3819fa369"),
               ("protocol_params_fingerprint","fp")):
    db.execute("INSERT INTO meta VALUES (?,?,?)", (k, val, ts(NOW)))

db.commit()
n = lambda t: db.execute(f"select count(*) from {t}").fetchone()[0]
print(f"validators {n('validator_identities')}  publications {n('publications')}  "
      f"assignments {n('assignments')}  probes {n('probes')}  reach {n('reachability')}")
print("verdict mix:")
for cls, c in sorted(counts.items(), key=lambda x: -x[1]):
    print(f"  {cls:26} {c:7,}")
db.close()
