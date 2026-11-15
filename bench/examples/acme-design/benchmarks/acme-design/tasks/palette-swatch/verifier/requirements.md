# palette-swatch: what each check holds, and why

The instruction asks for `palette.svg`, one labelled swatch per colour of
`brief.md`, in order. `tests/verify.py` holds the palette copied from the
brief and reads only the exported artifact:

| Check | Source |
|---|---|
| `svg` | "A standalone SVG document: the root element is `svg` in the SVG namespace, and the file is well-formed XML" |
| `colors` | "Exactly one `<rect>` per palette colour, five in all, in the brief's order", "`fill` is the colour's hex value" (case ignored), "a `<title>` child holding the colour's name" |
| `layout` | "at least 40 by 40, with `x`, `y`, `width` and `height` as plain numbers, and no two rects overlap. Do not use `transform`" |

Expected, in order: Harbor Navy #1F3A5F, Sandstone #D8C3A5, Signal Coral
#E85D4A, Moss #5B7F4A, Paper White #F7F4EE.

The mutants: Signal Coral one digit off (#E85D4B), and Sandstone and Moss
in swapped places. Both fail `colors`.
