# Weather API (v2)

Every answer is JSON.

- `GET /sites`: the depots the service knows, `{"sites": [{"id": "...", "name": "..."}]}`.
- `GET /forecast?site=<id>&date=YYYY-MM-DD`: the hourly forecast of one site,
  `{"site": "...", "date": "...", "hourly": [{"time": "HH:MM", "gust_kmh": n, "precip_prob": n}, ...]}`.
  `precip_prob` is a percentage.

Authentication is added for you; do not send any credential.
