package config

import (
	"fmt"
	"github.com/pelletier/go-toml/v2"
	"strings"
)

func SecretValues(data []byte) []string {
	var tree map[string]any
	if toml.Unmarshal(data, &tree) != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	var walk func(any, bool)
	walk = func(v any, secret bool) {
		switch value := v.(type) {
		case map[string]any:
			for key, child := range value {
				k := strings.ToLower(key)
				walk(child, secret || strings.Contains(k, "token") || strings.Contains(k, "secret") || strings.Contains(k, "password") || strings.Contains(k, "authorization") || k == "metadatas" || k == "proxyurl")
			}
		case []any:
			for _, child := range value {
				walk(child, secret)
			}
		default:
			if secret {
				str := fmt.Sprint(v)
				if str != "" && !seen[str] {
					seen[str] = true
					out = append(out, str)
				}
			}
		}
	}
	walk(tree, false)
	return out
}
