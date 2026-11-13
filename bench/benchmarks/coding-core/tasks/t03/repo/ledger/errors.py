"""Exception types raised by ledger."""


class LedgerError(Exception):
    """Base class for every error ledger raises on bad input or misuse."""


class CsvFormatError(LedgerError, ValueError):
    """A CSV file could not be imported.

    The message always starts with ``line N:``, where N is the 1-based line
    number in the file (the header row is line 1).
    """
