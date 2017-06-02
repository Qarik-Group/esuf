# Improvements

Added support for Elastic Search 5.X APIs with `--elastic-search-5`.
The ES 5 API changed how shard allocation is done, so prior to this,
`esuf` failed to re-allocate any shards on ES5.
