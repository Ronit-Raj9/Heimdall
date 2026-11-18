package db

// RebindDollar exposes the Postgres rebind to tests without a server.
var RebindDollar = rebindDollar

// SplitPassword exposes the DSN password split to tests.
var SplitPassword = splitPassword
