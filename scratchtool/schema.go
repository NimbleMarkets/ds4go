package scratchtool

import (
	"encoding/json"

	ds4 "github.com/NimbleMarkets/ds4go"
)

func schema(name, desc, params string) ds4.ToolSchema {
	return ds4.ToolSchema{Name: name, Description: desc, Parameters: json.RawMessage(params)}
}

const listParams = `{"type":"object","properties":{}}`
const getParams = `{"type":"object","properties":{"key":{"type":"string"},"head":{"type":"integer"},"tail":{"type":"integer"}},"required":["key"]}`
const setParams = `{"type":"object","properties":{"key":{"type":"string"},"value":{"type":"string"}},"required":["key","value"]}`
const appendParams = setParams
const deleteParams = `{"type":"object","properties":{"key":{"type":"string"}},"required":["key"]}`

const listDesc = "List scratchpad keys, one per line: key, size in bytes, last-modified time. " +
	"Scratchpad is private working memory for this session. It is not the user-visible reply and not the project workspace. " +
	"Put plans, intermediate findings, and open questions in keys. Prefer small keys: plan, findings, open. " +
	"List before get when unsure what exists."
const getDesc = "Read one scratchpad value. " +
	"Set head or tail to read only the first or last N bytes (bytes, not characters); set at most one of them."
const setDesc = "Replace one scratchpad value, creating the key if missing. Replace plan when the plan changes. " +
	"Do not dump the full conversation into a key. Do not write secrets, tokens, or raw file dumps."
const appendDesc = "Append text to one scratchpad value, creating the key if missing. Append to findings as you work."
const deleteDesc = "Delete one scratchpad key."
