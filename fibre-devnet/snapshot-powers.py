#!/usr/bin/env python3
"""Snapshot a live chain's voting-power distribution for the devnet to mirror.

Why this exists: the devnet gave every validator near-equal stake, and almost
everything that behaves differently on a real network comes from the stake
being skewed, not from there being more validators.

  - The 148-row floor in the assignment only binds for small validators. With
    equal stake it never binds until the set passes about eighty.
  - The publisher stops collecting signatures at two thirds of voting POWER
    (fibre/validator/signature_set.go). How many validators that is, and which
    ones, is entirely a function of the distribution.

So a twenty-validator devnet running mocha's real curve tests more than forty
equal ones. The output is committed, so a run needs no network and two runs a
month apart are comparable.

  python3 fibre-devnet/snapshot-powers.py https://rpc-mocha.pops.one \
      > fibre-devnet/powers/mocha-5.txt
"""
import json
import sys
import urllib.request
from datetime import datetime, timezone


def fetch(base: str) -> list[int]:
    powers: list[int] = []
    for page in range(1, 21):
        url = f"{base.rstrip('/')}/validators?page={page}&per_page=100"
        with urllib.request.urlopen(url, timeout=60) as r:
            result = json.load(r)["result"]
        powers += [int(v["voting_power"]) for v in result.get("validators", [])]
        if len(powers) >= int(result.get("total", "0")):
            break
    return powers


def main() -> int:
    if len(sys.argv) != 2:
        sys.stderr.write(__doc__)
        return 2
    base = sys.argv[1]
    with urllib.request.urlopen(base.rstrip("/") + "/status", timeout=60) as r:
        st = json.load(r)["result"]
    chain = st["node_info"]["network"]
    height = st["sync_info"]["latest_block_height"]

    powers = sorted(fetch(base), reverse=True)
    if not powers:
        sys.stderr.write("no validators returned\n")
        return 1
    total = sum(powers)

    # How many of the largest validators it takes to reach two thirds of the
    # power: the floor on quorum size, and the number the 2/3 rule turns on.
    run = 0
    floor = 0
    for i, p in enumerate(powers, 1):
        run += p
        if run * 3 >= total * 2:
            floor = i
            break

    print(f"# voting power per validator, largest first, one per line.")
    print(f"# chain     {chain}")
    print(f"# height    {height}")
    print(f"# taken     {datetime.now(timezone.utc).strftime('%Y-%m-%dT%H:%M:%SZ')}")
    print(f"# source    {base}/validators")
    print(f"# validators {len(powers)}   total power {total}")
    print(f"# smallest quorum: the largest {floor} validators reach 2/3 of power.")
    print(f"# A quorum is a race though, not a fixed list: the publisher stops when")
    print(f"# two thirds of power has answered, so membership follows arrival order.")
    for p in powers:
        print(p)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
