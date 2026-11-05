#!/usr/bin/env python3
"""Capture real booxd TUI search and detail screens through tmux."""

import argparse
import os
import re
import shlex
import shutil
import subprocess
import sys
import tempfile
import time
import uuid
from pathlib import Path

try:
    from PIL import Image, ImageDraw, ImageFont
except ImportError as error:
    print(f"tui-screenshot: Pillow is required ({error})", file=sys.stderr)
    raise SystemExit(1)


ROOT = Path(__file__).resolve().parents[1]
WIDTH = 100
HEIGHT = 30
SGR = re.compile(r"\x1b\[([0-9;]*)m")
ANSI = re.compile(r"\x1b\[[0-?]*[ -/]*[@-~]")
FONT_CANDIDATES = (
    "/System/Library/Fonts/Menlo.ttc",
    "/Library/Fonts/Menlo.ttc",
    "/usr/share/fonts/truetype/dejavu/DejaVuSansMono.ttf",
    "C:/Windows/Fonts/consola.ttf",
)


def run(args, *, check=True, **kwargs):
    result = subprocess.run(args, check=False, text=True, capture_output=True, **kwargs)
    if check and result.returncode:
        detail = (result.stderr or result.stdout).strip().splitlines()
        raise RuntimeError(detail[-1] if detail else f"{args[0]} exited {result.returncode}")
    return result


def capture(session):
    return run(["tmux", "capture-pane", "-p", "-e", "-t", session]).stdout


def wait_for_query_screen(session):
    deadline = time.monotonic() + 10
    while time.monotonic() < deadline:
        view = capture(session)
        if "search:" in view and "? for help" in view:
            return
        time.sleep(0.1)
    raise RuntimeError("TUI query screen did not appear")


def wait_for_results(session):
    deadline = time.monotonic() + 60
    while time.monotonic() < deadline:
        view = capture(session)
        if re.search(r"\b[1-9]\d* results\b", view):
            return view
        if re.search(r"\bno results\b", view):
            raise RuntimeError("query returned no results")
        time.sleep(0.5)
    lines = capture(session).splitlines()
    status = ANSI.sub("", lines[-1]).strip() if lines else ""
    message = "timed out waiting for search results"
    raise RuntimeError(f"{message}: {status}" if status else message)


def cells(text):
    dim = reverse = False
    rows = []
    for raw_line in text.splitlines():
        row = []
        i = 0
        while i < len(raw_line):
            sgr = SGR.match(raw_line, i)
            if sgr:
                values = [int(value or 0) for value in sgr.group(1).split(";")]
                for value in values:
                    if value == 0:
                        dim = reverse = False
                    elif value == 2:
                        dim = True
                    elif value == 7:
                        reverse = True
                    elif value == 22:
                        dim = False
                    elif value == 27:
                        reverse = False
                i = sgr.end()
                continue
            control = ANSI.match(raw_line, i)
            if control:
                i = control.end()
                continue
            char = raw_line[i]
            row.append((char, dim, reverse))
            i += 1
        rows.append(row[:WIDTH])
    return rows[:HEIGHT]


def render(text, path, font):
    rows = cells(text)
    cell_width = font.getlength("M")
    line_height = font.getbbox("Mg")[3] - font.getbbox("Mg")[1] + 8
    image = Image.new("RGB", (max(1200, round(cell_width * WIDTH)), line_height * HEIGHT), "white")
    draw = ImageDraw.Draw(image)
    for y in range(HEIGHT):
        row = rows[y] if y < len(rows) else []
        x = 0
        while x < WIDTH:
            if x < len(row) and row[x][2]:
                end = x + 1
                while end < min(WIDTH, len(row)) and row[end][2]:
                    end += 1
                draw.rectangle((round(x * cell_width), y * line_height, round(end * cell_width), (y + 1) * line_height), fill="black")
                for column in range(x, end):
                    draw.text((round(column * cell_width), y * line_height + 2), row[column][0], font=font, fill="white")
                x = end
                continue
            if x < len(row):
                char, dim, reverse = row[x]
                fill = "white" if reverse else ("#777777" if dim else "black")
                draw.text((round(x * cell_width), y * line_height + 2), char, font=font, fill=fill)
            x += 1
    image.save(path)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--font", help="monospace font path")
    parser.add_argument("--font-size", type=int, default=20)
    args = parser.parse_args()

    try:
        if shutil.which("tmux") is None:
            raise RuntimeError("tmux is required")
        binary = ROOT / "dist" / "booxd"
        build = run(["make", "build"], cwd=ROOT, check=False)
        if build.returncode:
            raise RuntimeError("make build failed")
        if not binary.is_file():
            raise RuntimeError("dist/booxd is missing after make build")

        font_path = args.font or next((path for path in FONT_CANDIDATES if Path(path).is_file()), None)
        if not font_path:
            raise RuntimeError("no monospace font found; pass --font PATH")
        font = ImageFont.truetype(font_path, args.font_size)

        images = ROOT / "docs" / "images"
        images.mkdir(parents=True, exist_ok=True)
        session = "booxd-shot-" + uuid.uuid4().hex[:8]
        with tempfile.TemporaryDirectory(prefix="booxd-screenshot-") as cache:
            env = dict(os.environ, BOOXD_CACHE_DIR=cache, TERM="xterm-256color")
            command = "env " + shlex.join([f"BOOXD_CACHE_DIR={cache}", str(binary), "tui", "--mono"])
            run(["tmux", "new-session", "-d", "-s", session, "-x", str(WIDTH), "-y", str(HEIGHT), command], env=env)
            try:
                wait_for_query_screen(session)
                run(["tmux", "send-keys", "-l", "-t", session, "jane austen"])
                run(["tmux", "send-keys", "-t", session, "Enter"])
                results = wait_for_results(session)
                render(results, images / "tui-search.png", font)
                run(["tmux", "send-keys", "-t", session, "Enter"])
                deadline = time.monotonic() + 5
                while time.monotonic() < deadline:
                    detail = capture(session)
                    if "title:" in detail and "author:" in detail:
                        break
                    time.sleep(0.2)
                else:
                    raise RuntimeError("detail view did not appear")
                render(detail, images / "tui-detail.png", font)
            finally:
                run(["tmux", "kill-session", "-t", session], check=False)
    except (OSError, subprocess.CalledProcessError, RuntimeError) as error:
        print(f"tui-screenshot: {error}", file=sys.stderr)
        return 1
    print("wrote docs/images/tui-search.png and docs/images/tui-detail.png")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
