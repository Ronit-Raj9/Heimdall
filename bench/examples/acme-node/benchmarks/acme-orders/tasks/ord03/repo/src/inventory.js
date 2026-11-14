// Stock reservations. Checkout reserves every line of an order at once (all
// or nothing); the reservation holds the units for ttlMinutes, then expires
// and the units are available again. Payment commits the reservation, which
// takes the units out of stock for good.

import { OrderError } from './errors.js';

export class Inventory {
  #onHand = new Map();
  #reservations = new Map(); // orderId -> { lines: Map<sku, qty>, expiresAt }
  #ttlMs;
  #clock;

  /**
   * stock maps SKU to units on hand, e.g. { 'TEA-01': 40 }. options.clock
   * returns the current time in milliseconds (tests pass a fake clock).
   */
  constructor(stock = {}, { ttlMinutes = 15, clock = () => Date.now() } = {}) {
    for (const [sku, units] of Object.entries(stock)) {
      if (!Number.isInteger(units) || units < 0) throw new OrderError(`stock of ${sku} must be a non-negative integer`);
      this.#onHand.set(sku, units);
    }
    this.#ttlMs = ttlMinutes * 60000;
    this.#clock = clock;
  }

  /** Units of sku in stock, reserved or not. */
  onHand(sku) {
    return this.#onHand.get(sku) ?? 0;
  }

  /** Units of sku that a new reservation can take. */
  available(sku) {
    this.#expire();
    let reserved = 0;
    for (const r of this.#reservations.values()) reserved += r.lines.get(sku) ?? 0;
    return this.onHand(sku) - reserved;
  }

  /**
   * Reserve every line of an order, or nothing. Throws OrderError naming
   * each short SKU. Returns { orderId, expiresAt } (milliseconds).
   */
  reserve(orderId, lines) {
    this.#expire();
    if (this.#reservations.has(orderId)) throw new OrderError(`order ${orderId}: already reserved`);
    const wanted = new Map();
    for (const { sku, qty } of lines) wanted.set(sku, (wanted.get(sku) ?? 0) + qty);
    const short = [...wanted]
      .filter(([sku, qty]) => qty > this.available(sku))
      .map(([sku, qty]) => `${sku} (want ${qty}, available ${this.available(sku)})`);
    if (short.length > 0) throw new OrderError(`order ${orderId}: not enough stock: ${short.join(', ')}`);
    const expiresAt = this.#clock() + this.#ttlMs;
    this.#reservations.set(orderId, { lines: wanted, expiresAt });
    return { orderId, expiresAt };
  }

  /** Drop a reservation. Returns false when there was none. */
  release(orderId) {
    return this.#reservations.delete(orderId);
  }

  /** Take a reserved order's units out of stock. Throws when it expired. */
  commit(orderId) {
    this.#expire();
    const r = this.#reservations.get(orderId);
    if (!r) throw new OrderError(`order ${orderId}: no active reservation`);
    for (const [sku, qty] of r.lines) this.#onHand.set(sku, this.onHand(sku) - qty);
    this.#reservations.delete(orderId);
  }

  #expire() {
    const now = this.#clock();
    for (const [orderId, r] of this.#reservations) {
      if (r.expiresAt <= now) this.#reservations.delete(orderId);
    }
  }
}
