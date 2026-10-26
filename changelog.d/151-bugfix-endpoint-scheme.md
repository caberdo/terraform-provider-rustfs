`endpoint` accepts `host`, `host:port` and `scheme://host[:port]`, and both API clients are now
built from one normalized reading of it. Before this change an endpoint that carried its own
scheme, for example `RUSTFS_ENDPOINT=https://rustfs.example.com`, produced a working S3 client and
an admin client that dialled `http://https://rustfs.example.com`. An explicit `http`/`https`
scheme now selects the transport; when `ssl` is also set and contradicts the scheme the plan fails
with a diagnostic naming both values, and an endpoint with a path, query or inline credentials is
rejected instead of being pasted into a URL. Configurations that set `ssl` and omit the scheme are
unchanged.
