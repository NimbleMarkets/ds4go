package workspacetool

import (
	"encoding/json"

	ds4 "github.com/NimbleMarkets/ds4go"
)

func schema(name, desc, params string) ds4.ToolSchema {
	return ds4.ToolSchema{Name: name, Description: desc, Parameters: json.RawMessage(params)}
}

const readParams = `{"type":"object","properties":{"path":{"type":"string"},"start_line":{"type":"integer"},"max_lines":{"type":"integer"},"whole":{"type":"boolean"},"raw":{"type":"boolean"}},"required":["path"]}`
const moreParams = `{"type":"object","properties":{"count":{"type":"integer"}}}`
const listParams = `{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`
const searchParams = `{"type":"object","properties":{"query":{"type":"string"},"path":{"type":"string"},"mode":{"type":"string"},"glob":{"type":"string"},"context":{"type":"integer"},"max_results":{"type":"integer"},"case_sensitive":{"type":"boolean"}},"required":["query"]}`
const writeParams = `{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"]}`
const editParams = `{"type":"object","properties":{"path":{"type":"string"},"old":{"type":"string"},"new":{"type":"string"}},"required":["path","old","new"]}`
const bashParams = `{"type":"object","properties":{"command":{"type":"string"},"timeout_sec":{"type":"number"},"refresh_sec":{"type":"number"}},"required":["command"]}`
const bashStatusParams = `{"type":"object","properties":{"job":{"type":"integer"},"pid":{"type":"integer"},"refresh_sec":{"type":"number"}},"required":["job"]}`
const bashStopParams = `{"type":"object","properties":{"job":{"type":"integer"},"pid":{"type":"integer"},"kill_children":{"type":"boolean"}},"required":["job"]}`
const viewImageParams = `{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`
