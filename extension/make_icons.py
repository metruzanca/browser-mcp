#!/usr/bin/env python3
"""Generate the extension icons as PNGs without any image library.

Renders a rounded-square indigo badge with a white "browser tab" glyph
(a ring + top bar), supersampled for anti-aliasing.
"""
import math
import os
import struct
import zlib

SIZES = [16, 32, 48, 128]
SS = 4  # supersample factor

BG = (79, 70, 229, 255)      # indigo #4F46E5
FG = (255, 255, 255, 255)
CORNER_RATIO = 0.22          # corner radius as fraction of size


def rounded_square_alpha(x, y, size):
    r = size * CORNER_RATIO
    cx = min(max(x, r), size - r)
    cy = min(max(y, r), size - r)
    return 1.0 if (x - cx) ** 2 + (y - cy) ** 2 <= r * r else 0.0


def glyph_alpha(x, y, size):
    """White ring (globe) + top bar, returned as coverage in [0,1]."""
    c = size / 2.0
    ring_r = size * 0.30
    ring_w = size * 0.06
    d = math.hypot(x - c, y - c)
    ring = 1.0 if abs(d - ring_r) <= ring_w / 2.0 else 0.0

    bar_h = size * 0.06
    bar_top = size * 0.22
    bar = 1.0 if bar_top <= y <= bar_top + bar_h else 0.0

    dot_r = size * 0.05
    dot = 1.0 if d <= dot_r else 0.0
    return max(ring, bar, dot)


def render(size):
    s = size * SS
    px = bytearray()
    for sy in range(s):
        for sx in range(s):
            fx = sx / SS + 0.5 / SS
            fy = sy / SS + 0.5 / SS
            bg_a = rounded_square_alpha(fx, fy, size)
            fg_a = glyph_alpha(fx, fy, size)
            a = max(bg_a, fg_a)
            if a <= 0:
                px += b"\x00\x00\x00\x00"
                continue
            r = round(BG[0] + (FG[0] - BG[0]) * fg_a)
            g = round(BG[1] + (FG[1] - BG[1]) * fg_a)
            b = round(BG[2] + (FG[2] - BG[2]) * fg_a)
            px += bytes((r, g, b, round(255 * min(1.0, a))))
    # downsample by averaging SS*SS blocks
    raw = bytearray()
    out = bytearray()
    for y in range(size):
        raw.append(0)  # filter type 0
        for x in range(size):
            rs = gs = bs = al = 0
            for dy in range(SS):
                for dx in range(SS):
                    i = ((y * SS + dy) * s + (x * SS + dx)) * 4
                    rs += px[i]
                    gs += px[i + 1]
                    bs += px[i + 2]
                    al += px[i + 3]
            n = SS * SS
            out += bytes((rs // n, gs // n, bs // n, al // n))
            raw += out[-4:]
    raw = bytes(raw)
    return raw


def png_bytes(width, height, raw):
    def chunk(tag, data):
        c = tag + data
        return struct.pack(">I", len(data)) + c + struct.pack(">I", zlib.crc32(c) & 0xFFFFFFFF)

    sig = b"\x89PNG\r\n\x1a\n"
    ihdr = struct.pack(">IIBBBBB", width, height, 8, 6, 0, 0, 0)
    idat = zlib.compress(raw, 9)
    return sig + chunk(b"IHDR", ihdr) + chunk(b"IDAT", idat) + chunk(b"IEND", b"")


def main():
    out_dir = os.path.join(os.path.dirname(os.path.abspath(__file__)), "icons")
    os.makedirs(out_dir, exist_ok=True)
    for size in SIZES:
        raw = render(size)
        path = os.path.join(out_dir, "icon%d.png" % size)
        with open(path, "wb") as f:
            f.write(png_bytes(size, size, raw))
        print("wrote", path)


if __name__ == "__main__":
    main()