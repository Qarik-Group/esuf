### Summary

`esuf` is a utility for fixing ElasticSearch clusters with lots of UNASSIGNED shards. Its purpose
is to offer a single command to run, which will examine ElasticSearch, and do its best to return
it to a `green` state.

It iterates over all indices, finding any UNASSIGNED shards, and assigns them to an appropriate
data node, based on the assignments of other shards in the index. It will not assign a shard to
a data node that already has a shard. It will not assign replica shards until the primary
shard is online. It will do its best to balance the primary shard relocations evenly across
data nodes.


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
