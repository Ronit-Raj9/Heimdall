# drone-window: what each check holds, and why

| Check | Instruction |
|---|---|
| `date` | "tomorrow's (2026-11-16) morning" |
| `riverside_flies` | 09:00 at riverside: gusts 22 km/h, rain 10 percent, both under the DISPATCH.md limits |
| `hilltop_holds_wind` | 09:00 at hilltop: gusts 41 km/h, at or over the 35 km/h limit |
| `quayside_holds_rain` | 09:00 at quayside: rain 70 percent, at or over the 50 percent limit |
| `no_extra_sites` | "our three depots", each in exactly one list, with the API's site ids |

The forecasts are the ones the cassette records, so a replay run has one
right plan.
