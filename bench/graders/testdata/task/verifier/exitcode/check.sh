#!/bin/sh
# Exit 0 only when calc.add is right.
exec python -I -B -c 'import sys; sys.path.insert(0, "/workspace"); from calc import add; sys.exit(0 if add(2, 3) == 5 and add(-2, -3) == -5 else 1)'
