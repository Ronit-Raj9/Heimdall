"""Shop prices: list price with tax and a bulk price."""

from acme.legacy import legacy_round

TAX = 0.2


def gross(net):
    """Price with tax, in pounds, rounded to pence."""
    return legacy_round(net * (1 + TAX))


def bulk(net, units):
    """Price of units items with 5% off from ten items up."""
    total = net * units
    if units >= 10:
        total *= 0.95
    return legacy_round(total)
