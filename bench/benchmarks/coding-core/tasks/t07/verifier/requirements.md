# t07 hidden tests: requirement map

All tests are in `verifier/tests/test_env_config.py`. Numbers refer to the numbered list in `instruction.md`.

| Test | Instruction sentence it checks |
|---|---|
| `test_environment_beats_file_beats_defaults` | 2: "an environment variable beats the file, and the file beats the default", "A setting that no variable names keeps its file value, or its default"; 1: `env` mapping argument. |
| `test_variable_names_for_nested_and_top_level_settings` | 3: `__` between section and key, keys containing single underscores (`EXEMPT_KEYS`), top-level `GATEKEEPER_NAME`; 4: list rule (`" health, metrics ,,ops "`, `""` gives `[]`) and string rule ("used exactly as given, with no stripping"). |
| `test_boolean_values` | 4: boolean words in any letter case with surrounding whitespace, for two boolean settings. |
| `test_numeric_values` | 4: integer with surrounding whitespace gives an `int`; `"30"` for a float setting gives `30.0`, a `float`. |
| `test_unconvertible_value_raises_config_error_naming_the_variable` | 5: "raises `gatekeeper.ConfigError`, and the error message contains the variable name"; 4: `"ten"` and `"2.5"` are not integers, `"soon"` not a number, `"maybe"` and `""` not booleans. |
| `test_variables_that_name_no_setting_are_ignored` | 3: "Names are matched exactly, in upper case. Any variable that does not name a setting in exactly this way (other spellings, other cases, unknown settings) is ignored." Lower case, mixed case, single underscore, unknown key, unknown top-level key, missing prefix. |
| `test_env_none_reads_os_environ_and_a_mapping_replaces_it` | 1: "When `env` is `None`, `os.environ` is used. When a mapping is given, only that mapping is read and `os.environ` is ignored." |
| `test_cli_config_reflects_the_process_environment` | 6: CLI output reflects the process environment; 2: precedence over `--file`; 4: list conversion. |
