# Acme house chart spec: quarterly bar chart

All coordinates are SVG user units; the chart has no transforms.

- Canvas: `width="480" height="320" viewBox="0 0 480 320"`, a white
  (`#ffffff`) rectangle covering it.
- Title: the text `Quarterly revenue (k$)`, centred at x 240, baseline y 32,
  `font-family="Liberation Sans"`, `font-size="18"`, `font-weight="bold"`,
  fill `#111827`, `text-anchor="middle"`.
- Baseline: a line from (40, 270) to (450, 270), stroke `#6b7280`,
  `stroke-width="2"`.
- Bars: one per quarter in CSV order, 60 wide, left edges at x 60, 160, 260
  and 360, standing on y 270, one unit of height per thousand dollars (a
  value of 120 is a bar from y 150 to y 270), fill `#2563eb`. Draw the bars
  before the baseline.
- Quarter labels: the quarter name centred under each bar, at the bar's
  centre x, baseline y 292, `font-family="Liberation Sans"`,
  `font-size="14"`, fill `#374151`, `text-anchor="middle"`.
- Value labels: the value (for example `120`) centred above each bar, at the
  bar's centre x, baseline 8 units above the bar's top,
  `font-family="Liberation Sans"`, `font-size="13"`, fill `#111827`,
  `text-anchor="middle"`.
