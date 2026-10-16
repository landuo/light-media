package app

import (
	"math/big"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

type categoryYAMLPair struct{ key, value *yaml.Node }

func categoryYAMLNode(node *yaml.Node) (*yaml.Node, error) {
	seen := map[*yaml.Node]bool{}
	for node != nil && node.Kind == yaml.AliasNode {
		if seen[node] || len(seen) >= 32 {
			return nil, categoryPolicyError("YAML别名循环或层级超过限制")
		}
		seen[node] = true
		node = node.Alias
	}
	if node == nil {
		return nil, categoryPolicyError("YAML别名没有有效目标")
	}
	return node, nil
}

func categoryYAMLEmpty(node *yaml.Node) bool {
	if node == nil {
		return true
	}
	if node.Kind == yaml.MappingNode || node.Kind == yaml.SequenceNode {
		return len(node.Content) == 0
	}
	if node.Kind != yaml.ScalarNode {
		return false
	}
	switch node.Tag {
	case "!!null":
		return true
	case "!!str":
		return node.Value == ""
	case "!!bool":
		return node.Value == "false" || node.Value == "False" || node.Value == "FALSE"
	case "!!int":
		value, ok := new(big.Int).SetString(strings.ReplaceAll(node.Value, "_", ""), 0)
		return ok && value.Sign() == 0
	case "!!float":
		value, err := strconv.ParseFloat(node.Value, 64)
		return err == nil && value == 0
	}
	return false
}

// ruamel.yaml's round-trip map iterates local keys before inherited merge keys.
// Resolve only the three small configuration levels and cap merge expansion;
// valid aliases work without allowing recursive or exponential YAML expansion.
func categoryYAMLMapping(node *yaml.Node, ancestors map[*yaml.Node]bool, budget *int) ([]categoryYAMLPair, error) {
	*budget--
	if *budget < 0 {
		return nil, categoryPolicyError("YAML合并展开次数超过8192项")
	}
	node, err := categoryYAMLNode(node)
	if err != nil {
		return nil, err
	}
	if node.Kind != yaml.MappingNode {
		return nil, categoryPolicyError("分类分区、目录及条件必须为映射或空值")
	}
	if len(ancestors) >= 32 || ancestors[node] {
		return nil, categoryPolicyError("YAML合并循环或层级超过限制")
	}
	path := make(map[*yaml.Node]bool, len(ancestors)+1)
	for key := range ancestors {
		path[key] = true
	}
	path[node] = true
	pairs := []categoryYAMLPair{}
	seen := map[string]bool{}
	var merges []*yaml.Node
	mergeSeen := false
	for i := 0; i < len(node.Content); i += 2 {
		key, value := node.Content[i], node.Content[i+1]
		value, err = categoryYAMLNode(value)
		if err != nil {
			return nil, err
		}
		if key.Tag == "!!merge" {
			if mergeSeen {
				return nil, categoryPolicyError("YAML合并键重复")
			}
			mergeSeen = true
			if value.Kind == yaml.SequenceNode {
				merges = append(merges, value.Content...)
			} else {
				merges = append(merges, value)
			}
			continue
		}
		if key.Kind != yaml.ScalarNode || key.Tag != "!!str" || seen[key.Value] {
			return nil, categoryPolicyError("分类键无效或重复")
		}
		seen[key.Value] = true
		pairs = append(pairs, categoryYAMLPair{key, value})
	}
	for _, merge := range merges {
		inherited, err := categoryYAMLMapping(merge, path, budget)
		if err != nil {
			return nil, err
		}
		for _, pair := range inherited {
			if !seen[pair.key.Value] {
				seen[pair.key.Value] = true
				pairs = append(pairs, pair)
			}
		}
		if len(pairs) > 1024 {
			return nil, categoryPolicyError("YAML合并后条目超过1024项")
		}
	}
	return pairs, nil
}
