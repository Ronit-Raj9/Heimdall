# Rate limits

The partner API allows 120 requests per minute per key, with bursts of up
to 20 requests. Requests over the limit get HTTP 429 and a Retry-After header.
