package dsml

import "encoding/json"

func jsonUnmarshalTest(s string, v any) error { return json.Unmarshal([]byte(s), v) }
