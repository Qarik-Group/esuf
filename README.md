### Summary

`esuf` is a utility for fixing ElasticSearch clusters with lots of UNASSIGNED shards. Its purpose
is to offer a single command to run, which will examine ElasticSearch, and do its best to return
it to a `green` state.

It iterates over all indices, finding any UNASSIGNED shards, and assigns them to an appropriate
data node, based on the assignments of other shards in the index. It will not assign a shard to
a data node that already has a shard. It will not assign replica shards until the corresponding
primary shard is online.

**NOTE:** This takes a heavy-handed approach to bringing things online. If a primary shard is
stuck in an UNASSGINED state, `esuf` will pass the `allow_primary: true` data to ElasticSearch,
telling it to bring the primary online, even if it does not have the data for it. This
may result in data loss to that shard of the index, but will get the cluster into a working
state again.

### Installation

`go get github.com/starkandwayne/esuf`

### Usage

For discovering what's wrong with ElasticSearch:

```
./esuf -H http://ip.of.elasticsearch.box:9200/
```

For fixing ElasticSearch:

```
./esuf -H http://ip.of.elasticsearch.box:9200/ --fix
```
