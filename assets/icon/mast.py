"""Antenna mark: one set of numbers, emitted as SVG and rasterised for preview.

The geometry lives here once so the SVG and the PNG cannot drift apart.
Modelled on the reference: a lattice mast whose splayed legs and crossbar read
as an A, a ring emitter, and two well separated arcs a side.
"""
import sys
sys.path.insert(0, "/tmp/claude-1000/-home-e-forte-code-antenna/0198220f-f182-4762-950a-87a7ac10cbc5/scratchpad")
import math
from mkicon import coverage, disc, ring_sector, capsule

W, H = 120, 130
CX, TOP = 60.0, 44.0          # mast centre line, emitter centre
RING_R, RING_W = 10.0, 6.0    # emitter ring radius and stroke
SOLID_R = 11.0                # solid emitter radius, the no hole variant
LEG_W = 6.5
LEG_TOP_DX, LEG_TOP_Y = 3.0, 54.0
LEG_FOOT_DX, LEG_FOOT_Y = 22.0, 104.0
BAR_Y, BAR_DX, BAR_W = 86.0, 14.0, 5.5
ARCS = [(24.0, -52, 52), (36.0, -46, 46)]   # radius, start, end (right side)
ARC_W = 6.5


def svg(colour="currentColor", ring=True, background=None, radius=0):
    """The mark as SVG. Strokes with round caps, so it scales cleanly."""
    parts = []
    if background:
        parts.append(f'<rect x="0" y="0" width="{W}" height="{H}" rx="{radius}" fill="{background}"/>')

    g = [f'<g fill="none" stroke="{colour}" stroke-linecap="round">']
    for r, a0, a1 in ARCS:
        for mirror in (1, -1):
            x0 = CX + mirror * r * math.cos(math.radians(a0))
            y0 = TOP + r * math.sin(math.radians(a0))
            x1 = CX + mirror * r * math.cos(math.radians(a1))
            y1 = TOP + r * math.sin(math.radians(a1))
            sweep = 1 if mirror > 0 else 0
            g.append(f'  <path d="M {x0:.1f} {y0:.1f} A {r} {r} 0 0 {sweep} {x1:.1f} {y1:.1f}" '
                     f'stroke-width="{ARC_W}"/>')
    g.append(f'  <path d="M {CX - LEG_TOP_DX:.1f} {LEG_TOP_Y} L {CX - LEG_FOOT_DX:.1f} {LEG_FOOT_Y}" stroke-width="{LEG_W}"/>')
    g.append(f'  <path d="M {CX + LEG_TOP_DX:.1f} {LEG_TOP_Y} L {CX + LEG_FOOT_DX:.1f} {LEG_FOOT_Y}" stroke-width="{LEG_W}"/>')
    g.append(f'  <path d="M {CX - BAR_DX:.1f} {BAR_Y} L {CX + BAR_DX:.1f} {BAR_Y}" stroke-width="{BAR_W}"/>')
    if ring:
        g.append(f'  <circle cx="{CX}" cy="{TOP}" r="{RING_R}" stroke-width="{RING_W}"/>')
    g.append('</g>')
    if not ring:
        g.append(f'<circle cx="{CX}" cy="{TOP}" r="{SOLID_R}" fill="{colour}"/>')
    parts += g
    body = "\n".join("  " + p for p in parts)
    return (f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {W} {H}" width="{W}" height="{H}">\n'
            f'{body}\n</svg>\n')


def shapes(ring=True, s=1.0, dy=0.0):
    """The same geometry as coverage predicates, for the PNG preview."""
    top = TOP * s + dy
    out = []
    for r, a0, a1 in ARCS:
        half = ARC_W / 2 * s
        out.append(ring_sector(CX * s, top, r * s - half, r * s + half, a0, a1))
        out.append(ring_sector(CX * s, top, r * s - half, r * s + half, 180 - a1, 180 - a0))
    out.append(capsule((CX - LEG_TOP_DX) * s, LEG_TOP_Y * s + dy,
                       (CX - LEG_FOOT_DX) * s, LEG_FOOT_Y * s + dy, LEG_W / 2 * s))
    out.append(capsule((CX + LEG_TOP_DX) * s, LEG_TOP_Y * s + dy,
                       (CX + LEG_FOOT_DX) * s, LEG_FOOT_Y * s + dy, LEG_W / 2 * s))
    out.append(capsule((CX - BAR_DX) * s, BAR_Y * s + dy,
                       (CX + BAR_DX) * s, BAR_Y * s + dy, BAR_W / 2 * s))
    if ring:
        half = RING_W / 2 * s
        out.append(ring_sector(CX * s, top, RING_R * s - half, RING_R * s + half, -180, 180))
    else:
        out.append(disc(CX * s, top, SOLID_R * s))
    return out


def alpha(ring=True, s=1.0, dy=0.0):
    return coverage(shapes(ring, s, dy), round(W * s), round(H * s))
