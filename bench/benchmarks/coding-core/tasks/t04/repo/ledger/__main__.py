"""Entry point for ``python -m ledger``."""

import sys

from .cli import main

sys.exit(main())
