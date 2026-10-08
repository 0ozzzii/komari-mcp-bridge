package execution

import "encoding/json"

func unmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }
func cloneConfig(c config) config {
	b, _ := json.Marshal(c)
	var r config
	json.Unmarshal(b, &r)
	return r
}
