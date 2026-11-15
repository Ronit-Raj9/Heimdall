The regional review needs a summary of last week's orders in
`data/sales.csv`.

1. Write `report.csv` in the workspace root with the header
   `region,orders,units,revenue` and one row per region, sorted by region
   name. Count only completed orders (status `complete`); refunded orders are
   left out. `revenue` is the sum of units times unit price, with two
   decimals.
2. Write `chart.svg` in the workspace root: a bar chart of revenue per
   region. Draw one `<rect>` bar per region, give each bar a `<title>` child
   holding the region name, and make each bar's height proportional to the
   region's revenue.

Leave `data/sales.csv` unchanged.
