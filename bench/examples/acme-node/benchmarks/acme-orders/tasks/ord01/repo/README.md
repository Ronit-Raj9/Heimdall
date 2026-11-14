# @acme/orders

Order pricing, coupons, stock reservations and order import for Acme's tea
stores, plus the `acme-orders` command line tool used by the back office.

Node.js 22 or later, ES modules, no npm dependencies.

```js
import { createCatalog, importOrdersCsv, priceOrders } from '@acme/orders';

const catalog = createCatalog(JSON.parse(catalogJson));
const priced = priceOrders(importOrdersCsv(csvText), { catalog, store });
```

Money is always an integer number of cents. Store settings (time zone, tax
rate, shipping, coupons) are described in `src/store.js`; see
`examples/store.json`.

## Command line

```sh
node bin/acme-orders.js price  examples/orders.csv --catalog examples/catalog.json --store examples/store.json
node bin/acme-orders.js report examples/orders.json --catalog examples/catalog.json --store examples/store.json --json
```

## Tests

```sh
npm test
```
