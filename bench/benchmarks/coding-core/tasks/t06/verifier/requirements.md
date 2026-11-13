# t06 hidden tests: requirement map

All tests are in `verifier/tests/test_dedupe_fast.py`. Rule numbers refer to the numbered list in `instruction.md`.

| Test | Instruction sentence it checks |
|---|---|
| `test_normalisation_rules` | 2: `str()` conversion (`42` vs `" 42 "`), whitespace runs collapsed (tab, newline, double space), ends trimmed, `str.casefold()` (`straße` vs `STRASSE`); 5: groups ascending, ordered by smallest index. |
| `test_empty_and_missing_values_never_match` | 2: `None` or a missing field becomes the empty string; 3: "An empty normalised value never matches anything." |
| `test_values_compared_only_within_the_same_field` | 3: "Values are only compared within the same field." |
| `test_matching_is_transitive_and_groups_are_ordered` | 3: match on at least one field; 4: transitive groups, including a record that bridges two earlier groups; 5: group order and index order; 1: `keys` selects the fields. |
| `test_drop_duplicates_keeps_first_of_each_group_in_order` | 6: keeps non-duplicates plus the lowest index of each group, original order, input not modified. |
| `test_find_duplicates_large_input_is_fast_and_exact` | Performance target (200,000 records, two keys, under 10 s around the whole call) and rules 2-5 on that input (10,000 groups of three case/whitespace variants, one 20,000-record chain joined only transitively, 20,000 records with blank or missing values, 130,000 unique records). |
| `test_drop_duplicates_large_input_is_fast_and_exact` | Performance target for `drop_duplicates` and rule 6 on the same input. |

The time limit is enforced with `SIGALRM`, so a quadratic implementation fails after 10 s instead of hanging the grader. The reference patch takes about 0.7 s per call in the base image on the authoring machine.
