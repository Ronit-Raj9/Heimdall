Customers see old prices: after a price change through the app,
`GET /products/<id>` keeps returning the previous price for up to
five minutes. Fix it so a price change is visible immediately.
Keep the Redis cache in use for reads, and do not throw away the
cached entries of products whose price did not change.
