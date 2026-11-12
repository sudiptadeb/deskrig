"""A small Python client for deskrig, for tests that send many actions or need many frames.

Standard library only; `Frames.array()` also uses numpy when it's there. The same test runs
on any target: pass one, or leave it to DESKRIG_TARGET and the default.

    from deskrig import Target
    t = Target()                   # or Target("pb-test")
    t.click(720, 450)
    t.key("cmd+t")
    t.type("hello")
    t.shot("out.png", "Safari")
    frames = t.frames()            # the screen of a VM target; t.frames("Safari") on local
    frames.wait()                  # until the next frame
    img = frames.array()           # rows x columns x RGB

    rec = t.record()               # every frame, with when it was on screen, captured on the target
    t.click(720, 450)
    rec.stop()
    for n, when, path in rec.frames():   # path is None for a dropped frame
        ...
    for start, end, action in rec.events():
        ...

Copy this file next to your tests, or put clients/python on PYTHONPATH.
"""

import datetime
import json
import mmap
import os
import struct
import subprocess
import time

MAGIC = b"DESKRIG1"
HEADER = "<8sQIIIId"  # magic, count, width, height, bytes per row, hidden, capture time


class Frames:
    """The latest frame, from the file `deskrig frames` keeps current."""

    def __init__(self, path, points_wide, points_high):
        self.path = path
        self.points = (points_wide, points_high)
        self.file = open(path, "rb")
        self.map = None

    def _header(self):
        if self.map is None or self.map.size() != os.fstat(self.file.fileno()).st_size:
            self.map = mmap.mmap(self.file.fileno(), 0, prot=mmap.PROT_READ)
        magic, count, w, h, stride, hidden, t = struct.unpack_from(HEADER, self.map, 0)
        if magic != MAGIC:
            raise RuntimeError(f"{self.path} isn't a deskrig frames file")
        if hidden:
            raise RuntimeError("the watched window is covered or off the desktop, so its frames are "
                               "stale; bring it to the front (deskrig raise)")
        return count, w, h, stride, t

    def count(self):
        return self._header()[0]

    def read(self):
        """(width, height, bytes per row, BGRA bytes, capture time) of one whole frame."""
        while True:
            count, w, h, stride, t = self._header()
            if count and count % 2 == 0:
                pixels = self.map[40:40 + stride * h]
                if self._header()[0] == count:
                    return w, h, stride, pixels, t
            time.sleep(0.001)

    @property
    def scale(self):
        """Pixels per point."""
        return self.read()[0] / self.points[0]

    def array(self):
        """The frame as a numpy array of RGB, row then column."""
        import numpy as np
        w, h, stride, pixels, _ = self.read()
        rows = np.frombuffer(pixels, np.uint8).reshape(h, stride)[:, :w * 4]
        return rows.reshape(h, w, 4)[:, :, 2::-1].copy()

    def wait(self, timeout=20):
        """Until the next frame, or timeout seconds. True if one came."""
        seen, end = self.count(), time.time() + timeout
        while time.time() < end:
            if self.count() > seen + seen % 2:
                return True
            time.sleep(0.002)
        return False


class Target:
    """One deskrig target. Actions go through one `deskrig input`, kept open."""

    def __init__(self, name=None, deskrig="deskrig"):
        self.name = name
        self.deskrig = deskrig
        self._input = None

    def _cmd(self, *args):
        return [self.deskrig] + (["--target", self.name] if self.name else []) + list(args)

    def run(self, *args, **kw):
        """A deskrig command on this target, as subprocess.run; check=True by default."""
        kw.setdefault("check", True)
        return subprocess.run(self._cmd(*args), **kw)

    def _send(self, line):
        if self._input is None:
            self._input = subprocess.Popen(self._cmd("input"), stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                                           text=True, bufsize=1)
            if self._input.stdout.readline().strip() != "ready":
                raise RuntimeError("deskrig input didn't start")
        self._input.stdin.write(line + "\n")
        answer = self._input.stdout.readline().strip()
        if answer != "ok":
            raise RuntimeError(f"{line}: {answer or 'deskrig input ended'}")

    def click(self, x, y): self._send(f"click {x} {y}")
    def dblclick(self, x, y): self._send(f"dblclick {x} {y}")
    def move(self, x, y): self._send(f"move {x} {y}")
    def down(self): self._send("down")
    def up(self): self._send("up")
    def drag(self, x1, y1, x2, y2): self._send(f"drag {x1} {y1} {x2} {y2}")
    def scroll(self, x, y, dy, steps=10, hold_ms=0): self._send(f"scroll {x} {y} {dy} {steps} {hold_ms}")
    def key(self, combo): self._send(f"key {combo}")
    def type(self, text): self._send(f"type {text}")

    def shot(self, path, app=None):
        self.run("shot", path, *([str(app)] if app else []), capture_output=True)
        return path

    def windows(self):
        """[(app, pid, x, y, w, h, title)], front to back."""
        out = self.run("windows", capture_output=True, text=True).stdout
        rows = []
        for line in out.splitlines():
            f = line.split("\t")
            rows.append((f[0], int(f[1]), *map(int, f[2:6]), f[6]))
        return rows

    def exec(self, *cmd, **kw):
        """A command on the target, in its desktop session, as subprocess.run."""
        return self.run("run", "--", *cmd, **kw)

    def frames(self, app=None):
        info = json.loads(self.run("frames", *([str(app)] if app else []), capture_output=True, text=True).stdout)
        return Frames(info["file"], info["pointsWide"], info["pointsHigh"])

    def record(self, app=None):
        """Starts recording the screen (or app's window); returns the Recording."""
        info = json.loads(self.run("record", "start", *([str(app)] if app else []),
                                   capture_output=True, text=True).stdout)
        return Recording(self, info)

    def close(self):
        if self._input:
            self._input.stdin.close()
            self._input.wait()
            self._input = None


def utc(text):
    """deskrig's time format, 2026-11-12T16:45:05.123456Z, as a timezone-aware datetime."""
    return datetime.datetime.strptime(text, "%Y-%m-%dT%H:%M:%S.%fZ").replace(tzinfo=datetime.timezone.utc)


class Recording:
    """A recording's folder: frames.tsv, the PNGs, events.tsv (record_darwin.go has the format).
    The files appear while it records; frames() and events() read what is there so far."""

    def __init__(self, target, info):
        self.target = target
        self.dir = info["dir"]
        self.points = (info["pointsWide"], info["pointsHigh"])
        self.scale = info["pixelsPerPoint"]

    def _rows(self, name):
        try:
            with open(os.path.join(self.dir, name)) as f:
                return [line.rstrip("\n").split("\t") for line in f.readlines()[1:] if line.endswith("\n")]
        except FileNotFoundError:
            return []

    def frames(self):
        """[(frame number, time on screen, PNG path or None)], in frame order; times are UTC datetimes."""
        return sorted((int(r[0]), utc(r[1]), None if r[4] == "-" else os.path.join(self.dir, r[4]))
                      for r in self._rows("frames.tsv"))

    def events(self):
        """[(start, end, action)] for each input action while recording; times are UTC datetimes."""
        return [(utc(r[0]), utc(r[1]), r[2]) for r in self._rows("events.tsv")]

    def stop(self):
        """Stops, and waits until every frame is written. Returns the number of frames."""
        return json.loads(self.target.run("record", "stop", capture_output=True, text=True).stdout)["frames"]
