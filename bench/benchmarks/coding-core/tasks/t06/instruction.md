# Make duplicate detection fast

`gatekeeper.find_duplicates(records, keys)` and `gatekeeper.drop_duplicates(records, keys)` are far too slow on real inputs: their running time grows with the square of the number of records, so a file of a few hundred thousand users never finishes. Make both fast while keeping their results exactly as they are today.

The results are defined by these rules, which must not change:

1. `records` is a list of mappings (dicts); `keys` is a list of field names.
2. Each field value is normalised before comparison: `None` or a missing field becomes the empty string; any other value is converted with `str()`, every run of whitespace is collapsed to a single space, leading and trailing whitespace is removed, and the result is case-folded with `str.casefold()`. This is what `gatekeeper.normalise_key` returns.
3. Two records match when, for at least one field in `keys`, both have the same non-empty normalised value in that field. Values are only compared within the same field. An empty normalised value never matches anything.
4. Matching is transitive: if record A matches B and B matches C, then A, B and C belong to one duplicate group, even when A and C share no value. A duplicate group is a maximal set of records connected this way that contains at least two records.
5. `find_duplicates` returns a list of groups. Each group is a list of record indices (positions in `records`) in ascending order. Groups are ordered by their smallest index. Records that belong to no group do not appear.
6. `drop_duplicates` returns a new list holding every record that belongs to no group plus the first record (lowest index) of each group, in their original order. The input list and its records are not modified.

Performance target: with 200,000 records and two key fields, each of `find_duplicates` and `drop_duplicates` must return in under 10 seconds, measured around the whole call, on a slow machine. Inputs of that size can contain thousands of groups and groups with tens of thousands of records.

The existing tests must keep passing.
