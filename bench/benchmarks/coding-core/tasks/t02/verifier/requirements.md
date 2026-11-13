# t02 requirements map

Every hidden test, the instruction sentence it checks, and what it asserts.

| Hidden test | Instruction sentence | Asserts |
|---|---|---|
| `test_csv_export_matches_documented_example` | "`--format csv` writes the CSV described below instead.", "The header row is exactly `invoice,date,customer,lines,subtotal,discount,tax,total` ...", the `invoice`/`date`/`lines` sentence, the amounts sentence ("An invoice without a discount has `0.00` in `discount`"), "in the same order as the JSON export", and the worked example "for `examples/invoices.csv` the output ... is:" | stdout for a copy of the example input equals the documented four lines byte for byte |
| `test_csv_quotes_only_fields_that_need_it` | "A field is enclosed in double quotes only when it contains a comma, a double quote or a line break; a double quote inside a quoted field is written twice. No other field is quoted." | `Ann "Bo" Lee` -> `"Ann ""Bo"" Lee"`, `Smith, Jones` -> quoted, `O'Hara & Co` and all amounts unquoted |
| `test_csv_rows_end_with_a_single_line_feed` | "Every row, including the header and the last row, ends with a single line feed (`\n`). There are no carriage returns and no blank lines." | no `\r`; output ends in one `\n`; 4 rows, 4 line feeds; no empty line |
| `test_json_output_is_unchanged` | "`--format json` is the default. With it, or with no `--format` at all, the output stays exactly what `export` writes today, byte for byte." | default and `--format json` stdout both equal the baseline JSON bytes |
| `test_output_option_writes_the_same_bytes` | "`-o PATH` / `--output PATH` works with both formats: the file receives exactly the bytes that would otherwise go to standard output, and nothing is written to standard output." | for csv and json, file bytes equal stdout bytes of the same run without `-o`; stdout empty; `--output` long form works |
| `test_unknown_format_is_a_usage_error` | "Any other value is rejected as a usage error: exit status 2 and nothing written to standard output." | `--format xml` exits 2 with empty stdout |
| `test_header_only_input_gives_just_the_header_row` | "When the input file has a header but no rows, the output is just the header row." | stdout is exactly the header line plus `\n` |
