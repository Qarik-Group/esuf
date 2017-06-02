package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/httputil"
	"os"
	"regexp"
	"strings"

	"github.com/voxelbrain/goptions"
)

var Version string

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
		ES5           bool   `goptions:"--elastic-search-5, description='Enable support for Elastic Search 5.x API'"`
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
		if Version != "" {
			fmt.Fprintf(os.Stderr, "esuf v%s\n", Version)
		} else {
			fmt.Fprintf(os.Stderr, "esuf safe (development build)\n")
		}
		os.Exit(0)
	}

	if options.SkipSSLVerify {
		SkipSSLValidation = true
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
						err := rerouteShard(options.Host, index.Name, shard.Index, node.Name, true, options.ES5)
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
						err := rerouteShard(options.Host, index.Name, shard.Index, node.Name, false, options.ES5)
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

func httpRequest(method string, url string, data io.Reader) ([]byte, error) {

	req, err := http.NewRequest(method, url, data)
	if err != nil {
		return nil, err
	}
	req.Header.Add("Content-Type", "application/json")
	dumpReq, err := httputil.DumpRequest(req, true)
	if err != nil {
		return nil, err
	}
	TRACE("HTTP Request:\n%s", dumpReq)

	client := &http.Client{}
	client.Transport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: SkipSSLValidation}}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	dumpResp, err := httputil.DumpResponse(resp, true)
	if err != nil {
		return nil, err
	}
	TRACE("HTTP Response:\n%s", dumpResp)

	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("%s returned %d: %s", url, resp.StatusCode, body)
	}

	return body, nil
}

func getDataNodes(host string) ([]Node, error) {
	data, err := httpRequest("GET", host+"/_nodes?pretty", nil)
	if err != nil {
		return []Node{}, err
	}

	var o interface{}
	err = json.Unmarshal(data, &o)
	if err != nil {
		return []Node{}, err
	}

	var nodelist []Node
	if obj, ok := o.(map[string]interface{}); ok {
		if nodes, ok := obj["nodes"].(map[string]interface{}); ok {
			for id, n := range nodes {
				if node, ok := n.(map[string]interface{}); ok {
					if settings, ok := node["settings"].(map[string]interface{}); ok {
						if nodeSettings, ok := settings["node"].(map[string]interface{}); ok {
							var name string
							if name, ok = nodeSettings["name"].(string); !ok {
								return []Node{}, fmt.Errorf("Could not detect node name for node %s", id)
							}
							if dataNode, ok := nodeSettings["data"].(string); ok {
								if dataNode == "true" {
									nodelist = append(nodelist, Node{Name: name, ID: id})
								}
							} else {
								return []Node{}, fmt.Errorf("Unexpected data type for `nodes.%s.settings.node.data` key", id)
							}
						} else {
							return []Node{}, fmt.Errorf("Unexpected data type for `nodes.%s.settings.node` key", id)
						}
					} else {
						return []Node{}, fmt.Errorf("Unexpected data type for `nodes.%s.settings` key", id)
					}
				} else {
					return []Node{}, fmt.Errorf("Unexpected data type for `nodes.%s` key", id)
				}
			}
		} else {
			return []Node{}, fmt.Errorf("Unexpected data type for `nodes` key")
		}
	} else {
		return []Node{}, fmt.Errorf("Unexpected data type returned from `GET /_nodes`")
	}

	return nodelist, nil
}

func rerouteShard(host string, index string, shard int, node string, primary bool, elasticSearch5 bool) error {
	data := fmt.Sprintf(`{"commands":[{"allocate":{"index":"%s","shard":%d,"node":"%s","allow_primary":%t}}]}`, index, shard, node, primary)
	if elasticSearch5 {
		cmd := "allocate_replica"
		if primary {
			cmd = "allocate_primary"
		}
		data = fmt.Sprintf(`{"commands":[{"%s":{"index":"%s","shard":%d,"node":"%s"}}]}`, cmd, index, shard, node)
	}

	dataBuf := bytes.NewBuffer([]byte(data))
	_, err := httpRequest("POST", host+"/_cluster/reroute", dataBuf)
	return err
}

func getIndices(host string) ([]Index, error) {
	data, err := httpRequest("GET", host+"/_cluster/state?pretty", nil)
	if err != nil {
		return []Index{}, err
	}

	var o interface{}
	err = json.Unmarshal(data, &o)
	if err != nil {
		return []Index{}, err
	}

	var indexlist []Index
	if obj, ok := o.(map[string]interface{}); ok {
		if rt, ok := obj["routing_table"].(map[string]interface{}); ok {
			// indices
			if indices, ok := rt["indices"].(map[string]interface{}); ok {
				for indexName, i := range indices {
					if indexData, ok := i.(map[string]interface{}); ok {
						index := Index{Name: indexName}
						if shards, ok := indexData["shards"].(map[string]interface{}); ok {
							index.PrimaryShards = make([]Shard, len(shards))
							index.ReplicaShards = make([]Shard, len(shards))
							for shardKey, s := range shards {
								if shardList, ok := s.([]interface{}); ok {
									for i, s := range shardList {
										if shard, ok := s.(map[string]interface{}); ok {

											var primary bool
											var state string
											var node string
											var relocating string
											var shardFloat float64
											shardFloat = -1.0

											if primary, ok = shard["primary"].(bool); !ok {
												return []Index{}, fmt.Errorf("Could not parse `routing_table.indices.%s.shards.%s.[%d].primary", indexName, shardKey, i)
											}

											if state, ok = shard["state"].(string); !ok {
												return []Index{}, fmt.Errorf("Could not parse `routing_table.indices.%s.shards.%s.[%d].state", indexName, shardKey, i)
											}
											if node, ok = shard["node"].(string); shard["node"] != nil && !ok {
												return []Index{}, fmt.Errorf("Could not parse `routing_table.indices.%s.shards.%s.[%d].node", indexName, shardKey, i)
											}
											if relocating, ok = shard["relocating_node"].(string); shard["relocating"] != nil && !ok {
												return []Index{}, fmt.Errorf("Could not parse `routing_table.indices.%s.shards.%s.[%d].relocating_node", indexName, shardKey, i)
											}
											if shardFloat, ok = shard["shard"].(float64); !ok {
												return []Index{}, fmt.Errorf("Could not parse 'routing_table.indices.%s.shards.%s.[%d].shard", indexName, shardKey, i)
											}
											shardIndex := int(shardFloat)

											parsedShard := Shard{
												Index:      shardIndex,
												Status:     state,
												Node:       node,
												Relocating: relocating,
											}

											if primary {
												index.PrimaryShards[shardIndex] = parsedShard
											} else {
												index.ReplicaShards[shardIndex] = parsedShard
											}
										} else {
											return []Index{}, fmt.Errorf("Unexpected data type for `routing_table.indices.%s.shards.%s.[%d]", indexName, shardKey, i)
										}
									}
								} else {
									return []Index{}, fmt.Errorf("Unexpected data type for `routing_table.indices.%s.shards.%s", indexName, shardKey)
								}
							}
						} else {
							return []Index{}, fmt.Errorf("Unexpected data type for `routing_table.indices.%s.shards", indexName)
						}
						indexlist = append(indexlist, index)
					} else {
						return []Index{}, fmt.Errorf("Unexpected data type for `routing_table.indices.%s`", indexName)
					}
				}
			} else {
				return []Index{}, fmt.Errorf("Unexpected data type for `routing_table.indices`")
			}
		} else {
			return []Index{}, fmt.Errorf("Unexpected data type for `routing_table`")
		}
	} else {
		return []Index{}, fmt.Errorf("Unexpected data type returned from `GET /_status`")
	}

	return indexlist, nil
}
