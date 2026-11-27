package service

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeCodexBrowserTabsMatchesAmbientContext(t *testing.T) {
	const item = `{"type":"function_call_output","namespace":"agent","name":"browser_tabs","output":"<in-app-browser-context source=\"ambient-ui-state\">Browser fixture</in-app-browser-context>"}`
	wrap := func(raw string) []byte { return []byte(`{"input":[` + raw + `]}`) }

	normalized, changed := NormalizeCodexBrowserTabs(wrap(item))
	require.True(t, changed)
	again, changed := NormalizeCodexBrowserTabs(normalized)
	require.False(t, changed)
	require.Equal(t, normalized, again)

	blankID := strings.Replace(item, `"name":"browser_tabs"`, `"name":"browser_tabs","call_id":" "`, 1)
	got, changed := NormalizeCodexBrowserTabs(wrap(blankID))
	require.True(t, changed)
	require.Equal(t, normalized, got)

	for _, test := range []struct {
		name   string
		before string
		after  string
	}{
		{"real call ID", `"name":"browser_tabs"`, `"name":"browser_tabs","call_id":"call_real"`},
		{"case-variant call ID", `"name":"browser_tabs"`, `"name":"browser_tabs","CALL_ID":"call_real"`},
		{"ambiguous call ID", `"name":"browser_tabs"`, `"name":"browser_tabs","call_id":"call_real","CALL_ID":""`},
		{"null call ID", `"name":"browser_tabs"`, `"name":"browser_tabs","call_id":null`},
		{"duplicate namespace", `"namespace":"agent"`, `"namespace":"agent","namespace":"other"`},
		{"case-variant namespace", `"namespace":"agent"`, `"Namespace":"agent"`},
		{"other tool", "browser_tabs", "other_context"},
		{"other source", "ambient-ui-state", "other-source"},
		{"empty snapshot", "Browser fixture", " "},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := wrap(strings.Replace(item, test.before, test.after, 1))
			got, changed := NormalizeCodexBrowserTabs(body)
			require.False(t, changed)
			require.Equal(t, body, got)
		})
	}
}
