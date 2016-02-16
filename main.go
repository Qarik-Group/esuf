package main

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"github.com/voxelbrain/goptions"
	"io/ioutil"
	"net/http"
	"net/http/httputil"
	"os"
	"regexp"
	"strings"
)

var debug bool
var SkipSSLValidation bool

type Index struct {
	ReplicaShards []Shard
	Name          string
	PrimaryShard  Shard
}

type Node struct {
	Name string
	Id   string
}

type Shard struct {
	Index      string
	Node       string
	Relocating string
	Status     string
}

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

func ERROR(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "%s\n", fmt.Sprintf(format, args...))
}

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
		Host          string `goptions:"-H, --elasticsearch_url, description='ElasticSearch URL. Defaults to http://localhost:9200'"`
		SkipSSLVerify bool   `goptions:"-k, --skip-ssl-validation, description='Disable SSL certificate checking'"`
		Help          bool   `goptions:"-h, --help"`
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

	for _, index := range indices {
		DEBUG("Examining index %s", index.Name)
		var availNodeMap map[string]Node
		for _, node := range nodes {
			availNodeMap[node.Id] = node
		}

		delete(availNodeMap, index.PrimaryShard.Node)
		delete(availNodeMap, index.PrimaryShard.Relocating)
		for _, shard := range index.ReplicaShards {
			delete(availNodeMap, shard.Node)
			delete(availNodeMap, shard.Relocating)
		}

		var availNodes []Node
		for _, n := range availNodeMap {
			availNodes = append(availNodes, n)
		}
		DEBUG("Nodes available to host shards: %v", availNodes)

		if index.PrimaryShard.Status == "UNASSIGNED" {
			var node Node
			node, availNodes = availNodes[0], availNodes[1:]
			DEBUG("Primary Shard to be assigned to %s", node.Name)
		}

		for _, shard := range index.ReplicaShards {
			if shard.Status == "UNASSIGNED" {
				var node Node
				node, availNodes = availNodes[0], availNodes[1:]
				DEBUG("Shard %s to be assigned to %s", shard.Index, node.Name)
			}
		}
	}
}

func httpRequest(method string, url string) ([]byte, error) {

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	dumpReq, err := httputil.DumpRequest(req, true)
	if err != nil {
		return nil, err
	}
	DEBUG("HTTP Request:\n%s", dumpReq)

	client := &http.Client{}
	client.Transport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: SkipSSLValidation}}

	resp, err := client.Do(req)
	dumpResp, dumpErr := httputil.DumpResponse(resp, true)
	if dumpErr != nil {
		return nil, err
	}
	DEBUG("HTTP Response:\n%s", dumpResp)
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	return body, nil
}

func getDataNodes(host string) ([]Node, error) {
	data, err := httpRequest("GET", host+"/_nodes?pretty")
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
						if node_settings, ok := settings["node"].(map[string]interface{}); ok {
							var name string
							if name, ok = node_settings["name"].(string); !ok {
								return []Node{}, fmt.Errorf("Could not detect node name for node %s", id)
							}
							if data_node, ok := node_settings["data"].(string); ok {
								if data_node == "true" {
									nodelist = append(nodelist, Node{Name: name, Id: id})
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

func getIndices(host string) ([]Index, error) {
	data, err := httpRequest("GET", host+"/_status?pretty")
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
		if indices, ok := obj["indices"].(map[string]interface{}); ok {
			for indexName, i := range indices {
				if indexData, ok := i.(map[string]interface{}); ok {
					index := Index{Name: indexName}
					if shards, ok := indexData["shards"].(map[string]interface{}); ok {
						for shardIndex, s := range shards {
							if shardList, ok := s.([]interface{}); ok {
								for i, s := range shardList {
									if shard, ok := s.(map[string]interface{}); ok {

										if routing, ok := shard["routing"].(map[string]interface{}); ok {
											var primary bool
											var state string
											var node string
											var relocating string
											if primary, ok = routing["primary"].(bool); !ok {
												return []Index{}, fmt.Errorf("Could not parse `indices.%s.shards.%s.[%d].routing.primary", indexName, shardIndex, i)
											}

											if state, ok = routing["state"].(string); !ok {
												return []Index{}, fmt.Errorf("Could not parse `indices.%s.shards.%s.[%d].routing.state", indexName, shardIndex, i)
											}
											if node, ok = routing["node"].(string); !ok {
												return []Index{}, fmt.Errorf("Could not parse `indices.%s.shards.%s.[%d].routing.node", indexName, shardIndex, i)
											}
											if relocating, ok = routing["relocating_node"].(string); !ok {
												return []Index{}, fmt.Errorf("Could not parse `indices.%s.shards.%s.[%d].routing.relocating_node", indexName, shardIndex, i)
											}

											parsedShard := Shard{
												Index:      shardIndex,
												Status:     state,
												Node:       node,
												Relocating: relocating,
											}

											if primary {
												index.PrimaryShard = parsedShard
											} else {
												index.ReplicaShards = append(index.ReplicaShards, parsedShard)
											}
										} else {
											return []Index{}, fmt.Errorf("Unexpected data type for `indices.%s.shards.%s.[%d].routing", indexName, shardIndex, i)
										}
									} else {
										return []Index{}, fmt.Errorf("Unexpected data type for `indices.%s.shards.%s.[%d]", indexName, shardIndex, i)
									}
								}
							} else {
								return []Index{}, fmt.Errorf("Unexpected data type for `indices.%s.shards.%s", indexName, shardIndex)
							}
						}
					} else {
						return []Index{}, fmt.Errorf("Unexpected data type for `indices.%s.shards", indexName)
					}
				} else {
					return []Index{}, fmt.Errorf("Unexpected data type for `indices.%s`", indexName)
				}
			}
		} else {
			return []Index{}, fmt.Errorf("Unexpected data type for `indices`")
		}
	} else {
		return []Index{}, fmt.Errorf("Unexpected data type returned from `GET /_status`")
	}

	return indexlist, nil
}
