package domain

import "encoding/json"

// VideoUsage preserves the supplier's decimal seconds as JSON numbers. It is
// stored with the usage row, independently of Grok's legacy integer duration.
// No request text, media URLs, credentials, or inferred token counts belong here.
type VideoUsage struct {
	Provider            string      `json:"provider"`
	InputVideoDuration  json.Number `json:"input_video_duration"`
	OutputVideoDuration json.Number `json:"output_video_duration"`
	Duration            json.Number `json:"duration"`
	Resolution          string      `json:"resolution"`
}
