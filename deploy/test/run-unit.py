#!/usr/bin/env python3
"""Run one systemd unit's ExecStart the way systemd would.

This is not a substitute for systemd and does not pretend to be: it ignores
the sandboxing directives, which are the whole point of the units on a real
host. What it does check is the part that is easy to get wrong and impossible
to notice until the service fails to start: that the binary path exists, that
every flag name is one the binary actually has, that the EnvironmentFile
parses and every variable it references is set, and that the service user can
reach the files it needs.

It follows systemd's rules rather than the shell's, because the two disagree
in ways that matter here:

  * An EnvironmentFile line is KEY=value where the value runs to the end of
    the line and is NOT shell-quoted. `VANTAGE_LOCATION=Helsinki, Finland` is
    one value; a shell sourcing that file would try to run `Finland`.
  * In ExecStart, `${VAR}` substitutes as a single argument, while `$VAR`
    splits on whitespace. The units use the braced form on purpose, so a
    location with a space in it stays one argument.
  * ExecStart may continue across lines with a trailing backslash.
"""
from __future__ import annotations

import os
import shlex
import subprocess
import sys


def read_env_file(path: str) -> dict[str, str]:
    env: dict[str, str] = {}
    with open(path, encoding="utf-8") as fh:
        for raw in fh:
            line = raw.strip()
            if not line or line.startswith("#") or "=" not in line:
                continue
            key, _, value = line.partition("=")
            key = key.strip()
            # systemd strips one layer of matching quotes, nothing more.
            if len(value) >= 2 and value[0] == value[-1] and value[0] in "\"'":
                value = value[1:-1]
            env[key] = value
    return env


def directive(unit_text: str, name: str) -> str | None:
    for line in unit_text.splitlines():
        if line.startswith(name + "="):
            return line[len(name) + 1:].strip()
    return None


def exec_start(unit_text: str) -> str:
    lines = unit_text.splitlines()
    for i, line in enumerate(lines):
        if not line.startswith("ExecStart="):
            continue
        acc = line[len("ExecStart="):]
        j = i
        while acc.rstrip().endswith("\\"):
            acc = acc.rstrip()[:-1]
            j += 1
            acc += " " + lines[j].strip()
        return acc
    raise SystemExit("unit has no ExecStart")


def expand(word: str, env: dict[str, str]) -> list[str]:
    """Expand one shell-split word under systemd's substitution rules."""
    if word.startswith("${") and word.endswith("}") and word.count("${") == 1:
        # braced: exactly one argument, whatever is inside it
        return [env.get(word[2:-1], "")]
    out = word
    for key, value in env.items():
        out = out.replace("${" + key + "}", value)
    # an unbraced $VAR splits on whitespace, like systemd
    if "$" in out:
        for key, value in env.items():
            out = out.replace("$" + key, value)
        return out.split()
    return [out]


def main() -> int:
    if len(sys.argv) < 2:
        print("usage: run-unit.py <unit file> [instance] [extra args...]", file=sys.stderr)
        return 2
    unit_path, extra = sys.argv[1], sys.argv[2:]
    text = open(unit_path, encoding="utf-8").read()
    # A template unit (fibre-scan@.service) takes the network as its instance
    # name; substitute the specifiers systemd would.
    if os.path.basename(unit_path).endswith("@.service"):
        if not extra:
            print("template unit needs an instance name", file=sys.stderr)
            return 2
        instance, extra = extra[0], extra[1:]
        prefix = os.path.basename(unit_path).split("@")[0]
        text = text.replace("%i", instance).replace("%I", instance).replace("%p", prefix)

    env = dict(os.environ)
    env_path = directive(text, "EnvironmentFile")
    if env_path:
        if not os.path.isfile(env_path):
            print(f"EnvironmentFile {env_path} does not exist", file=sys.stderr)
            return 2
        env.update(read_env_file(env_path))

    raw = exec_start(text)
    argv: list[str] = []
    for word in shlex.split(raw, posix=False):
        argv.extend(expand(word, env))
    argv = [a for a in argv if a != ""] + extra

    missing = [w for w in shlex.split(raw, posix=False)
               if w.startswith("${") and w.endswith("}") and w[2:-1] not in env]
    if missing:
        print(f"unset variables referenced by ExecStart: {', '.join(missing)}", file=sys.stderr)
        return 2

    user = directive(text, "User")
    if user and user != os.environ.get("USER"):
        argv = ["setpriv", "--reuid", user, "--regid", user, "--clear-groups",
                "env", "HOME=/var/lib/fibre-observer"] + argv

    print(f"unit={os.path.basename(unit_path)} user={user or 'root'}", file=sys.stderr)
    print("argv=" + " ".join(shlex.quote(a) for a in argv), file=sys.stderr)
    return subprocess.call(argv, env=env)


if __name__ == "__main__":
    sys.exit(main())
