package service

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"

	"github.com/tidwall/gjson"
)

// ResponsesInputHasFunctionCallOutputNamed reports whether the top-level input
// array holds an object item whose type is "function_call_output" and whose
// name starts with one of namePrefixes. Keys and string values are compared
// after JSON unescaping, through a zero-copy view of body. When a member is
// duplicated, the first occurrence is read; callers that decode the body
// treat duplicate members separately.
func ResponsesInputHasFunctionCallOutputNamed(body []byte, namePrefixes ...string) bool {
	input := parseRawJSONView(body).Get("input")
	if !input.IsArray() {
		return false
	}
	found := false
	input.ForEach(func(_, item gjson.Result) bool {
		if !item.IsObject() || item.Get("type").String() != "function_call_output" {
			return true
		}
		name := item.Get("name").String()
		for _, prefix := range namePrefixes {
			if strings.HasPrefix(name, prefix) {
				found = true
				return false
			}
		}
		return true
	})
	return found
}

// HasUniqueJSONMembers reports whether body is a single JSON value whose object keys are unique.
func HasUniqueJSONMembers(body []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(body))
	if !consumeUniqueJSONValue(decoder) {
		return false
	}
	_, err := decoder.Token()
	return err == io.EOF
}

func consumeUniqueJSONValue(decoder *json.Decoder) bool {
	token, err := decoder.Token()
	if err != nil {
		return false
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return true
	}

	switch delim {
	case '{':
		members := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return false
			}
			key, ok := keyToken.(string)
			if !ok {
				return false
			}
			if _, duplicate := members[key]; duplicate {
				return false
			}
			members[key] = struct{}{}
			if !consumeUniqueJSONValue(decoder) {
				return false
			}
		}
		end, err := decoder.Token()
		return err == nil && end == json.Delim('}')
	case '[':
		for decoder.More() {
			if !consumeUniqueJSONValue(decoder) {
				return false
			}
		}
		end, err := decoder.Token()
		return err == nil && end == json.Delim(']')
	default:
		return false
	}
}
