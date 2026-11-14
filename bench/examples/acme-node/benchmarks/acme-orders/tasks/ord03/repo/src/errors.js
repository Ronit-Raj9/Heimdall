// Errors raised by @acme/orders. Callers branch on the class, never on the
// message text, except for the "line N:" prefix of ImportError, which the CLI
// prints as it is.

/** An order, catalog or store setting that cannot be priced or reserved. */
export class OrderError extends Error {
  constructor(message) {
    super(message);
    this.name = 'OrderError';
  }
}

/** Input that cannot be imported. CSV messages start with "line N:" (the header is line 1). */
export class ImportError extends Error {
  constructor(message) {
    super(message);
    this.name = 'ImportError';
  }
}
