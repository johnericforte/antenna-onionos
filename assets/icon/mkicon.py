"""Draw the Antenna icon.

No image library is available here, so this rasterises shapes by supersampling
and writes the PNG by hand. Shapes are described as coverage predicates, which
keeps the drawing readable and gives clean edges without an antialiasing pass.
"""

import math
import struct
import zlib

CANVAS_W, CANVAS_H = 120, 130
SS = 4  # subsamples per axis


def write_png(path, width, height, pixels):
    """pixels is a list of rows, each a list of (r, g, b, a)."""
    raw = bytearray()
    for row in pixels:
        raw.append(0)  # filter: none
        for r, g, b, a in row:
            raw += bytes((r, g, b, a))

    def chunk(tag, data):
        out = struct.pack(">I", len(data)) + tag + data
        return out + struct.pack(">I", zlib.crc32(tag + data) & 0xFFFFFFFF)

    header = struct.pack(">IIBBBBB", width, height, 8, 6, 0, 0, 0)
    png = (b"\x89PNG\r\n\x1a\n"
           + chunk(b"IHDR", header)
           + chunk(b"IDAT", zlib.compress(bytes(raw), 9))
           + chunk(b"IEND", b""))
    with open(path, "wb") as fh:
        fh.write(png)


# Shape predicates. Each returns True when the point is inside.

def disc(cx, cy, r):
    return lambda x, y: (x - cx) ** 2 + (y - cy) ** 2 <= r * r


def ring_sector(cx, cy, r_inner, r_outer, a_start, a_end):
    """An arc with round caps, angles in degrees, 0 pointing right, y down."""
    a0, a1 = math.radians(a_start), math.radians(a_end)
    mid = (r_inner + r_outer) / 2
    half = (r_outer - r_inner) / 2
    caps = [disc(cx + mid * math.cos(a), cy + mid * math.sin(a), half) for a in (a0, a1)]

    def inside(x, y):
        dx, dy = x - cx, y - cy
        d = math.hypot(dx, dy)
        if r_inner <= d <= r_outer:
            ang = math.atan2(dy, dx)
            while ang < a0:
                ang += 2 * math.pi
            if ang <= a1:
                return True
        return any(cap(x, y) for cap in caps)

    return inside


def polygon(points):
    def inside(x, y):
        hit = False
        n = len(points)
        for i in range(n):
            x0, y0 = points[i]
            x1, y1 = points[(i + 1) % n]
            if (y0 > y) != (y1 > y):
                cross = x0 + (y - y0) * (x1 - x0) / (y1 - y0)
                if x < cross:
                    hit = not hit
        return hit

    return inside


def capsule(x0, y0, x1, y1, r):
    """A thick line with round ends."""
    def inside(px, py):
        dx, dy = x1 - x0, y1 - y0
        length2 = dx * dx + dy * dy
        t = 0.0 if length2 == 0 else max(0.0, min(1.0, ((px - x0) * dx + (py - y0) * dy) / length2))
        nx, ny = x0 + t * dx, y0 + t * dy
        return (px - nx) ** 2 + (py - ny) ** 2 <= r * r

    return inside


def coverage(shapes, width, height, negatives=()):
    """Alpha per pixel, with negatives punched out of the positives."""
    step, offset = 1.0 / SS, 1.0 / (2 * SS)
    rows = []
    for py in range(height):
        row = []
        for px in range(width):
            hits = 0
            for sy in range(SS):
                y = py + offset + sy * step
                for sx in range(SS):
                    x = px + offset + sx * step
                    if any(shape(x, y) for shape in shapes) and not any(n(x, y) for n in negatives):
                        hits += 1
            row.append(hits / (SS * SS))
        rows.append(row)
    return rows


def render(shapes, colour, path, width=CANVAS_W, height=CANVAS_H, negatives=()):
    r, g, b = colour
    rows = [[(r, g, b, round(255 * a)) for a in row]
            for row in coverage(shapes, width, height, negatives)]
    write_png(path, width, height, rows)
    return path
