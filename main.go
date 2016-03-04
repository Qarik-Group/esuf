package main

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"github.com/geofffranks/botta"
	"github.com/voxelbrain/goptions"
	"net/http"
	"net/http/httputil"
	"os"
	"regexp"
	"strings"
)

// VERSION holds the Current version of spruce
var VERSION = "0.0.0" // SED MARKER FOR AUTO VERSION BUMPING
// BUILD holds CURRENT BUILD OF SPRUCE
var BUILD = "master" // updated by build.sh
// DIRTY holds Whether any uncommitted changes were found in the working copy
var DIRTY = "" // updated by build.sh

var debug bool
var trace bool

// SkipSSLValidation ...
var SkipSSLValidation bool

// Index ...
type Index struct {
	PrimaryShards []Shard
	ReplicaShards []Shard
	Name          string
}

// Node ...
type Node struct {
	Name string
	ID   string
}

// Shard ...
type Shard struct {
	Index      int
	Node       string
	Relocating string
	Status     string
}

// DEBUG ...
func DEBUG(format string, args ...interface{}) {
	if debug {
		content := fmt.Sprintf(format, args...)
		lines := strings.Split(content, "\n")
		for i, line := range lines {
			lines[i] = "DEBUG> " + line
		}
		content = strings.Join(lines, "\n")
		fmt.Fprintf(os.Stderr, "%s\n", content)
	}
}

// TRACE ...
func TRACE(format string, args ...interface{}) {
	if trace {
		DEBUG(format, args...)
	}
}

// ERROR ...
func ERROR(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "%s\n", fmt.Sprintf(format, args...))
}

// FATAL ...
func FATAL(format string, args ...interface{}) {
	ERROR(format, args...)
	os.Exit(1)
}

func trueString(str string) bool {
	re := regexp.MustCompile("^(?i:no|false|0|off)$")
	if str == "" || re.Match([]byte(str)) {
		return false
	}
	return true
}

func main() {
	var options struct {
		Debug         bool   `goptions:"-D, --debug, description='Enable debugging'"`
		Trace         bool   `goptions:"--trace, description='Enable API call tracing'"`
		Fix           bool   `goptions:"-f, --fix, description='Fix any issues detected'"`
		Host          string `goptions:"-H, --elasticsearch_url, description='ElasticSearch URL. Defaults to http://localhost:9200'"`
		SkipSSLVerify bool   `goptions:"-k, --skip-ssl-validation, description='Disable SSL certificate checking'"`
		Help          bool   `goptions:"-h, --help"`
		Version       bool   `goptions:"-v, --version"`
	}

	goptions.ParseAndFail(&options)
	if options.Help {
		goptions.PrintHelp()
		os.Exit(0)
	}

	if options.Debug {
		debug = true
	}
	if trueString(os.Getenv("DEBUG")) {
		debug = true
	}

	if options.Trace {
		trace = true
		debug = true
	}
	if trueString(os.Getenv("TRACE")) {
		trace = true
		debug = true
	}

	if options.Version {
		plus := ""
		if BUILD != "release" {
			plus = "+"
		}
		fmt.Fprintf(os.Stderr, "%s - Version %s%s (%s%s)\n", os.Args[0], VERSION, plus, BUILD, DIRTY)
		os.Exit(0)
	}

	if options.SkipSSLVerify {
		client := botta.Client()
		client.Transport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: SkipSSLValidation}}
	}

	if options.Host == "" {
		options.Host = "http://localhost:9200/"
	}

	DEBUG("Checking ElasticSearch on %s", options.Host)

	nodes, err := getDataNodes(options.Host)
	if err != nil {
		FATAL(err.Error())
	}

	indices, err := getIndices(options.Host)
	if err != nil {
		FATAL(err.Error())
	}

	TRACE("Indices: %v\nNodes: %v", indices, nodes)
	for _, index := range indices {
		fmt.Printf("Checking index %s\n", index.Name)
		availNodeMaps := make([]map[string]Node, len(index.PrimaryShards))
		for i := range availNodeMaps {
			availNodeMaps[i] = map[string]Node{}
			for _, node := range nodes {
				availNodeMaps[i][node.ID] = node
			}
		}

		for _, shard := range index.PrimaryShards {
			delete(availNodeMaps[shard.Index], shard.Node)
			delete(availNodeMaps[shard.Index], shard.Relocating)
		}

		for _, shard := range index.ReplicaShards {
			delete(availNodeMaps[shard.Index], shard.Node)
			delete(availNodeMaps[shard.Index], shard.Relocating)
		}

		availNodesList := make([][]Node, len(index.PrimaryShards))
		for i := range availNodesList {
			var availNodes []Node
			for _, n := range availNodeMaps[i] {
				availNodes = append(availNodes, n)
			}
			availNodesList[i] = availNodes
		}

		for _, shard := range index.PrimaryShards {
			if shard.Status == "UNASSIGNED" {
				availNodes := availNodesList[shard.Index]
				if len(availNodes) > 0 {
					var node Node
					node, availNodes = availNodes[0], availNodes[1:]
					fmt.Printf("    %s shard %d primary is UNASSIGNED.\n", index.Name, shard.Index)
					DEBUG("Would assign %s as new node", node.Name)
					if options.Fix {
						fmt.Printf("    - bringing up %s shard %d primary on %s\n", index.Name, shard.Index, node.Name)
						err := rerouteShard(options.Host, index.Name, shard.Index, node.Name, true)
						if err != nil {
							ERROR(err.Error())
						}
					}
				} else {
					fmt.Printf("    %s shard %d primary is UNASSIGNED, and no nodes are available to assign it to\n", index.Name, shard.Index)
				}
			}
		}

		for _, shard := range index.ReplicaShards {
			if shard.Status == "UNASSIGNED" {
				availNodes := availNodesList[shard.Index]
				if len(availNodes) > 0 {
					var node Node
					node, availNodes = availNodes[0], availNodes[1:]
					fmt.Printf("    %s shard %d replica is UNASSIGNED.\n", index.Name, shard.Index)
					DEBUG("Would assign %s as new node", node.Name)
					if options.Fix {
						fmt.Printf("    - bringing up %s shard %d replica on %s\n", index.Name, shard.Index, node.Name)
						err := rerouteShard(options.Host, index.Name, shard.Index, node.Name, false)
						if err != nil {
							ERROR(err.Error())
						}
					}
				} else {
					fmt.Printf("    %s shard %d replica is UNASSIGNED, and no nodes are available to assign it to\n", index.Name, shard.Index)
				}
			}
		}
	}
}

func get(url string) (*botta.Response, error) {
	req, err := botta.Get(url)
	if err != nil {
		return nil, err
	}
	dumpReq, err := httputil.DumpRequest(req, true)
	if err != nil {
		return nil, err
	}
	TRACE("HTTP Request:\n%s", dumpReq)
	data, err := botta.Issue(req)
	if err != nil {
		return nil, err
	}
	dumpResp, err := httputil.DumpResponse(data.HTTPResponse, true)
	if err != nil {
		return nil, err
	}
	TRACE("HTTP Response:\n%s", dumpResp)

	return data, nil
}

func getDataNodes(host string) ([]Node, error) {
	data, err := get(host + "/_nodes?pretty")
	if err != nil {
		return []Node{}, err
	}

	var nodelist []Node

	nodes, err := data.ArrayVal("nodes")
	if err != nil {
		return []Node{}, fmt.Errorf("Unexpected data type for `nodes` key")
	}
	for id, _ := range nodes {
		name, err := data.StringVal(fmt.Sprintf("nodes.[%d].settings.node.name", id))
		if err != nil {
			return []Node{}, fmt.Errorf("Could not detect node name for node %s", id)
		}
		dataNode, err := data.StringVal(fmt.Sprintf("nodes.[%d].settings.node.data", id))
		if err != nil {
			return []Node{}, fmt.Errorf("Unexpected data type for `nodes.%s.settings.node.data` key", id)
		}
		if dataNode == "true" {
			nodelist = append(nodelist, Node{Name: name, ID: fmt.Sprintf("%d", id)})
		}
	}

	return nodelist, nil
}

func rerouteShard(host string, index string, shard int, node string, primary bool) error {
	data := fmt.Sprintf(
		`{"commands":[{"allocate":{"index":"%s","shard":%d,"node":"%s","allow_primary":%t}}]}`,
		index, shard, node, primary)

	dataBuf := bytes.NewBuffer([]byte(data))

	req, err := http.NewRequest("POST", host+"/_cluster/reroute", dataBuf)
	if err != nil {
		return err
	}
	req.Header.Add("Accept", "application/json")
	req.Header.Add("Content-Type", "application/json")

	_, err = botta.Issue(req)
	return err
}

func getIndices(host string) ([]Index, error) {
	data, err := get(host + "/_cluster/state?pretty")
	if err != nil {
		return []Index{}, err
	}

	var indexlist []Index

	indices, err := data.MapVal("routing_table.indices")
	if err != nil {
		return []Index{}, fmt.Errorf("Unexpected data type for `routing_table.indices`")
	}
	for indexName, _ := range indices {
		index := Index{Name: indexName}
		shardStr := fmt.Sprintf("routing_table.indices.%s.shards", indexName)

		shards, err := data.MapVal(shardStr)
		if err != nil {
			return []Index{}, fmt.Errorf("Unexpected data type for `%s`", shardStr)
		}
		index.PrimaryShards = make([]Shard, len(shards))
		index.ReplicaShards = make([]Shard, len(shards))

		for shardKey, _ := range shards {
			shardList, err := data.ArrayVal(fmt.Sprintf("%s.shards.%s", shardStr, shardKey))
			if err != nil {
				return []Index{}, fmt.Errorf("Unexpected data type for `%s.shards.%s`", shardStr, shardKey)
			}
			for i, _ := range shardList {
				subShard := fmt.Sprintf("%s.shards.%s.[%d]", shardStr, shardKey, i)
				primary, err := data.BoolVal(fmt.Sprintf("%s.primary", subShard))
				if err != nil {
					return []Index{}, fmt.Errorf("Could not parse `%s.primary`", subShard)
				}

				state, err := data.StringVal(fmt.Sprintf("%s.state", subShard))
				if err != nil {
					return []Index{}, fmt.Errorf("Could not parse `%s.state`", subShard)
				}

				node, err := data.StringVal(fmt.Sprintf("%s.node", subShard))
				if err != nil {
					return []Index{}, fmt.Errorf("Could not parse `%s.node`", subShard)
				}

				relocating, err := data.StringVal(fmt.Sprintf("%s.relocating", subShard))
				if err != nil {
					return []Index{}, fmt.Errorf("Could not parse `%s.relocating_node`", subShard)
				}
				shardNum, err := data.NumVal(fmt.Sprintf("%s.shard", subShard))
				if err != nil {
					return []Index{}, fmt.Errorf("Could not parse `%s.shard`", subShard)
				}
				shardIndex, err := shardNum.Int64()
				if err != nil {
					return []Index{}, fmt.Errorf("Could not convert `%s.shard` to int", subShard)
				}

				parsedShard := Shard{
					Index:      int(shardIndex),
					Status:     state,
					Node:       node,
					Relocating: relocating,
				}

				if primary {
					index.PrimaryShards[shardIndex] = parsedShard
				} else {
					index.ReplicaShards[shardIndex] = parsedShard
				}
			}
		}
		indexlist = append(indexlist, index)
	}

	return indexlist, nil
}
