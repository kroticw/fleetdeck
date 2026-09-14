#!/usr/bin/env python3
"""ci-panel-cold-start.py measures how long a panel takes to answer on its first start.

TEMPORARY (T-056, v0.10.1): run by CI's panel-cold-start job on a fresh runner, and
removed before the pull request is merged. The numbers go into the comment at
measuredWorstStart in cmd/fleetdeck-window/owner.go.

Usage: ci-panel-cold-start.py <app-zip> <out-dir> <port>

It unpacks the release-built app, then starts its panel the way a window does
(--owner-pid, --stand-socket, a stand HOME) and looks, every 10 ms, for the first
line of the panel's output, for its "listening" line and for the first HTTP
answer. In order:

  window-h      the window binary's first run with -h, whole process
  cold          the panel's first start from the unpacked bundle
  warm          the same binary at the same path again
  new-path      a copy of the same binary at another path (same cdhash)
  new-cdhash    a copy signed ad hoc under another identifier (another cdhash)

Everything goes to <out-dir>/results.json and to standard output, with the
extended attributes of the unpacked bundle and what the system's policy daemons
logged about the binaries meanwhile.
"""

import json
import os
import shutil
import subprocess
import sys
import tempfile
import time
import urllib.request

POLL = 0.01
CEILING = 30.0


def ms(seconds):
    return round(seconds * 1000, 1)


def run(cmd, **kw):
    return subprocess.run(cmd, capture_output=True, text=True, **kw)


def answers(url):
    try:
        with urllib.request.urlopen(url, timeout=0.2) as resp:
            resp.read(1)
        return True
    except Exception:
        return False


def measure(label, panel, home, socket, port, out):
    log_path = os.path.join(out, label + ".log")
    url = "http://127.0.0.1:%d/" % port
    env = dict(os.environ, HOME=home)
    with open(log_path, "wb") as log:
        start = time.monotonic()
        proc = subprocess.Popen(
            [panel, "--owner-pid", str(os.getpid()), "--stand-socket", socket],
            stdout=log,
            stderr=subprocess.STDOUT,
            env=env,
        )
    first_line = listening = answered = exited = None
    while time.monotonic() - start < CEILING:
        now = time.monotonic() - start
        with open(log_path, "rb") as log:
            text = log.read().decode(errors="replace")
        if first_line is None and text.strip():
            first_line = now
        if listening is None and "listening" in text:
            listening = now
        if answered is None and answers(url):
            answered = time.monotonic() - start
        if proc.poll() is not None:
            exited = now
            break
        if answered is not None and listening is not None:
            break
        time.sleep(POLL)
    proc.terminate()
    try:
        proc.wait(timeout=5)
    except subprocess.TimeoutExpired:
        proc.kill()
        proc.wait()
    # The next start must not find this one's port still held.
    for _ in range(100):
        if not answers(url):
            break
        time.sleep(0.05)
    return {
        "label": label,
        "path": panel,
        "first_output_ms": None if first_line is None else ms(first_line),
        "listening_ms": None if listening is None else ms(listening),
        "answered_ms": None if answered is None else ms(answered),
        "exited_before_answer_ms": None if exited is None else ms(exited),
    }


def main():
    if len(sys.argv) != 4:
        print(__doc__, file=sys.stderr)
        sys.exit(2)
    zip_path, out, port = sys.argv[1], sys.argv[2], int(sys.argv[3])
    os.makedirs(out, exist_ok=True)
    work = tempfile.mkdtemp()
    results = {"sw_vers": run(["sw_vers"]).stdout, "runs": []}

    began = time.monotonic()
    unpacked = run(["ditto", "-x", "-k", zip_path, os.path.join(work, "unpacked")])
    results["unpack_ms"] = ms(time.monotonic() - began)
    if unpacked.returncode != 0:
        print(unpacked.stderr, file=sys.stderr)
        sys.exit(1)
    app = os.path.join(work, "unpacked", "fleetdeck.app")
    macos = os.path.join(app, "Contents", "MacOS")
    results["xattr"] = run(["xattr", "-lr", app]).stdout or "(none)"

    home = os.path.join(work, "home")
    os.makedirs(os.path.join(home, ".config", "fleetdeck"))
    with open(os.path.join(home, ".config", "fleetdeck", "config.yaml"), "w") as f:
        f.write("server:\n  port: %d\n" % port)
    socket = os.path.join(work, "no-daemon-here.sock")
    since = time.strftime("%Y-%m-%d %H:%M:%S")

    began = time.monotonic()
    window = run([os.path.join(macos, "fleetdeck-window"), "-h"])
    results["runs"].append({"label": "window-h", "whole_process_ms": ms(time.monotonic() - began), "exit": window.returncode})

    panel = os.path.join(macos, "fleetdeck")
    results["runs"].append(measure("cold", panel, home, socket, port, out))
    results["runs"].append(measure("warm", panel, home, socket, port, out))

    elsewhere = os.path.join(work, "elsewhere")
    os.makedirs(elsewhere)
    copy = os.path.join(elsewhere, "fleetdeck")
    shutil.copy2(panel, copy)
    results["runs"].append(measure("new-path", copy, home, socket, port, out))

    resigned_dir = os.path.join(work, "resigned")
    os.makedirs(resigned_dir)
    resigned = os.path.join(resigned_dir, "fleetdeck")
    shutil.copy2(panel, resigned)
    signed = run(["codesign", "--force", "--sign", "-", "--identifier", "dev.fleetdeck.stand.coldstart", resigned])
    results["resign"] = signed.stderr.strip() or "ok"
    results["runs"].append(measure("new-cdhash", resigned, home, socket, port, out))

    policy = run([
        "log", "show", "--style", "compact", "--start", since,
        "--predicate",
        'process IN {"syspolicyd", "amfid", "XprotectService", "trustd"} AND eventMessage CONTAINS[c] "fleetdeck"',
    ])
    with open(os.path.join(out, "policy.log"), "w") as f:
        f.write(policy.stdout + policy.stderr)
    results["policy_log_lines"] = len(policy.stdout.splitlines())

    with open(os.path.join(out, "results.json"), "w") as f:
        json.dump(results, f, indent=2)
    print(json.dumps(results, indent=2))


if __name__ == "__main__":
    main()
