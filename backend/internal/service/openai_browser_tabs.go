package service

import (
	"encoding/json"
	"strings"
)

// NormalizeCodexBrowserTabs converts Codex's ambient browser tab snapshot to
// user context for HTTP Responses, where a tool result needs a matching call ID.
func NormalizeCodexBrowserTabs(body []byte) ([]byte, bool) {
	if !ResponsesInputHasFunctionCallOutputNamed(body, "browser_tabs") || !HasUniqueJSONMembers(body) {
		return body, false
	}
	var request map[string]json.RawMessage
	if err := json.Unmarshal(body, &request); err != nil {
		return body, false
	}
	var input []json.RawMessage
	if err := json.Unmarshal(request["input"], &input); err != nil {
		return body, false
	}
	changed := false
	for index, raw := range input {
		var item map[string]json.RawMessage
		if err := json.Unmarshal(raw, &item); err != nil {
			continue
		}
		readString := func(name string) string {
			var value string
			_ = json.Unmarshal(item[name], &value)
			return value
		}
		if readString("type") != "function_call_output" || readString("namespace") != "agent" || readString("name") != "browser_tabs" {
			continue
		}
		var ambiguousCallID bool
		for name := range item {
			if name != "call_id" && strings.EqualFold(name, "call_id") {
				ambiguousCallID = true
				break
			}
		}
		if ambiguousCallID {
			continue
		}
		if callID, exists := item["call_id"]; exists {
			var id *string
			if err := json.Unmarshal(callID, &id); err != nil || id == nil || strings.TrimSpace(*id) != "" {
				continue
			}
		}
		output := readString("output")
		tabs, open := strings.CutPrefix(strings.TrimSpace(output), `<in-app-browser-context source="ambient-ui-state">`)
		tabs, closed := strings.CutSuffix(tabs, `</in-app-browser-context>`)
		if !open || !closed || strings.TrimSpace(tabs) == "" {
			continue
		}
		message := map[string]any{
			"type": "message", "role": "user",
			"content": []map[string]string{{"type": "input_text", "text": "<external_browser_tabs>" + output + "</external_browser_tabs>"}},
		}
		converted, err := json.Marshal(message)
		if err != nil {
			return body, false
		}
		input[index] = converted
		changed = true
	}
	if !changed {
		return body, false
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return body, false
	}
	request["input"] = encoded
	normalized, err := json.Marshal(request)
	if err != nil {
		return body, false
	}
	return normalized, true
}
