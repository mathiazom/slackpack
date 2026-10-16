package packing

import (
	"bytes"
	"encoding/json"
)

// stripKeys removes keys, by path, from a raw Slack object. Snapshots strip what
// changes too often to snapshot (statuses, huddles, dormancy), and messages the token
// owner's personal state. Numbers are kept verbatim.
func stripKeys(raw json.RawMessage, paths ...[]string) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var object map[string]interface{}
	if err := decoder.Decode(&object); err != nil {
		return nil, err
	}
	for _, path := range paths {
		parent := object
		for _, key := range path[:len(path)-1] {
			child, ok := parent[key].(map[string]interface{})
			if !ok {
				parent = nil
				break
			}
			parent = child
		}
		if parent != nil {
			delete(parent, path[len(path)-1])
		}
	}
	return json.Marshal(object)
}
