# Let environment variables override the configuration

`gatekeeper.load_config(path=None)` builds the configuration, a nested dict, from built-in defaults (`gatekeeper.DEFAULTS`) and an optional TOML file. Deployments need to adjust single settings without editing the file, so add a third layer: environment variables.

1. The signature becomes `load_config(path=None, env=None)`. `env` is a mapping from variable names to string values. When `env` is `None`, `os.environ` is used. When a mapping is given, only that mapping is read and `os.environ` is ignored.
2. Precedence is decided setting by setting: an environment variable beats the file, and the file beats the default. A setting that no variable names keeps its file value, or its default if the file does not set it either.
3. The variable for a setting is `GATEKEEPER_` followed by the setting's path in upper case, with `__` (two underscores) between a section and a key. For example `limiter.window` is `GATEKEEPER_LIMITER__WINDOW`, `limiter.exempt_keys` is `GATEKEEPER_LIMITER__EXEMPT_KEYS`, and the top-level `name` is `GATEKEEPER_NAME`. Names are matched exactly, in upper case. Any variable that does not name a setting in exactly this way (other spellings, other cases, unknown settings) is ignored.
4. The string value is converted to the type of the setting's default:
   - integer: an optional sign and decimal digits, surrounding whitespace allowed (`" 120 "` gives `120`);
   - float: a decimal number, surrounding whitespace allowed (`"30"` gives `30.0`, a `float`);
   - boolean: `1`, `true`, `yes`, `on` give `True` and `0`, `false`, `no`, `off` give `False`, in any letter case, surrounding whitespace allowed; any other value (including an empty one) cannot be converted;
   - list of strings: split on commas, each item stripped of surrounding whitespace, empty items dropped (`" a, b ,,c "` gives `["a", "b", "c"]`, and `""` gives `[]`);
   - string: used exactly as given, with no stripping.
5. A value that cannot be converted raises `gatekeeper.ConfigError`, and the error message contains the variable name.
6. `python -m gatekeeper config [--file PATH]` prints the effective configuration, so its output reflects the environment of the process.

Everything that works today, including the existing tests, must keep working.
