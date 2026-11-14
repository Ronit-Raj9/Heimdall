// Order pricing. An order is
//   { id, customer, placedAt, coupon, lines: [{ sku, qty }] }
// and prices to
//   { orderId, customer, businessDay, lines, subtotal, coupon, discount,
//     shipping, tax, total }
// with every amount in cents:
//   line amount = qty x unit price
//   subtotal    = sum of line amounts
//   discount    = the coupon's amount on the subtotal (see discounts.js)
//   shipping    = flat rate, or 0 when subtotal - discount reaches freeOver
//   tax         = taxRate % of (subtotal - discount), rounded half up
//   total       = subtotal - discount + shipping + tax

import { businessDay } from './dates.js';
import { applyCoupon } from './discounts.js';
import { percentOf } from './money.js';
import { normalizeStore } from './store.js';
import { OrderError } from './errors.js';

/**
 * Price one order. options.catalog is required (an object with get(sku));
 * options.store holds the store settings (see store.js) and defaults to a
 * UTC store with no tax.
 */
export function priceOrder(order, options = {}) {
  const { catalog, store } = checkOptions(options);
  checkOrder(order);
  const lines = order.lines.map((line) => priceLine(order, line, catalog.get(line.sku)));
  return totals(order, lines, store);
}

/** Price a batch of orders with the same options, in input order. */
export function priceOrders(orders, options = {}) {
  if (!Array.isArray(orders)) throw new TypeError('orders must be an array');
  const checked = checkOptions(options);
  return orders.map((order) => priceOrder(order, checked));
}

function checkOptions(options) {
  if (typeof options?.catalog?.get !== 'function') throw new TypeError('options.catalog must have a get(sku) method');
  return { catalog: options.catalog, store: normalizeStore(options.store) };
}

function checkOrder(order) {
  if (typeof order?.id !== 'string' || order.id === '') throw new OrderError('order: id is required');
  if (!Array.isArray(order.lines) || order.lines.length === 0) throw new OrderError(`order ${order.id}: no lines`);
  for (const line of order.lines) {
    if (!Number.isInteger(line?.qty) || line.qty < 1) {
      throw new OrderError(`order ${order.id}: quantity of ${line?.sku} must be a positive integer`);
    }
  }
}

function priceLine(order, line, item) {
  if (!item) throw new OrderError(`order ${order.id}: unknown sku ${line.sku}`);
  return { sku: line.sku, name: item.name, qty: line.qty, unitPrice: item.price, amount: item.price * line.qty };
}

function totals(order, lines, store) {
  const day = businessDay(order.placedAt, store.timeZone);
  const subtotal = lines.reduce((sum, line) => sum + line.amount, 0);
  const coupon = applyCoupon(store, order.coupon, subtotal, day);
  const discounted = subtotal - coupon.amount;
  const free = store.shipping.freeOver !== null && discounted >= store.shipping.freeOver;
  const shipping = free ? 0 : store.shipping.flat;
  const tax = percentOf(discounted, store.taxRate);
  return {
    orderId: order.id,
    customer: order.customer ?? null,
    businessDay: day,
    lines,
    subtotal,
    coupon: { code: coupon.code, status: coupon.status },
    discount: coupon.amount,
    shipping,
    tax,
    total: discounted + shipping + tax,
  };
}
