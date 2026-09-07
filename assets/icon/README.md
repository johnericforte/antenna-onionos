# Icon

`App/Antenna/icon.png` is generated from the files here, so the mark is edited
by changing numbers rather than by tracing a bitmap.

- `antenna.svg` is the shipped icon: a lattice mast whose splayed legs and
  crossbar read as an A, a solid emitter, and two arcs a side set well clear of
  the body.
- `antenna-mono.svg` is the same mark with `currentColor`, for anywhere the
  colour should come from its surroundings.
- `mast.py` holds the geometry once and emits both the SVG and the PNG, so the
  two cannot drift apart. `mkicon.py` is the rasteriser it uses.

## Why it looks like this

**The A is the point.** Every antenna icon is a mast with arcs, so the mark
needed something that is this app rather than the category. The legs of a real
lattice tower already form an A, so it costs nothing.

**It is a filled tile, not a bare glyph.** OnionOS themes set their own
backgrounds, and a single colour glyph cannot survive all of them: white
disappears on a light theme, dark disappears on a dark one, and both are lost
on an image background. A tile brings its own background and is legible against
anything.

**Rust and cream** are the cabinet and dial colours of 1970s television, which
is the era of the public domain film the shipped sources hold. Amber phosphor
and terminal green were the alternatives, and both point at old computing
rather than old broadcast.

## Regenerating

Standard library Python, no dependencies:

```sh
python3 - <<'PY'
import sys; sys.path.insert(0, "assets/icon")
from mast import svg
open("assets/icon/antenna.svg", "w").write(
    svg(colour="#F4E7D3", ring=False, background="#C0562A", radius=22))
PY
```

The PNG is 120x130 with a transparent margin, which is the size OnionOS draws
app icons at.
