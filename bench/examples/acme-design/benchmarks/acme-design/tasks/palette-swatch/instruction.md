`brief.md` is the brand brief from our client Larkspur Ferries. Make the
swatch sheet for its palette.

Write it as `palette.svg` in the workspace root:

- A standalone SVG document: the root element is `svg` in the SVG namespace
  (`xmlns="http://www.w3.org/2000/svg"`), and the file is well-formed XML.
- Exactly one `<rect>` per palette colour, five in all, in the brief's
  order (first colour first in the document).
- Each rect's `fill` attribute is the colour's hex value from the brief,
  written `#RRGGBB`.
- Each rect has a `<title>` child holding the colour's name exactly as the
  brief writes it.
- Each rect is at least 40 by 40, with `x`, `y`, `width` and `height` as
  plain numbers, and no two rects overlap. Do not use `transform`.

Other elements (text labels, outlines, a background) are fine, as long as
they are not `<rect>` elements. Only `palette.svg` is read; do not change
`brief.md`.
