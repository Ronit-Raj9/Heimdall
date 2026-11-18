#!/bin/sh
# Mutant: the starter script as it is (128 units, 5 epochs), about 83% on
# new tickets; it must fail the 90.5% target.
exec python3 -I train.py
