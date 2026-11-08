package dto

import (
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestBailianUsageAPIPreservesDecimalStrings(t *testing.T) {
	usage := &domain.VideoUsage{Provider: service.PlatformBailian, InputVideoDuration: json.Number("0.000000000000000001"), OutputVideoDuration: json.Number("1"), Duration: json.Number("1.000000000000000001"), Resolution: "720p"}
	log := &service.UsageLog{VideoCount: 1, VideoUsage: usage}
	for _, dto := range []any{UsageLogFromService(log), UsageLogFromServiceAdmin(log)} {
		raw, err := json.Marshal(dto)
		require.NoError(t, err)
		require.Contains(t, string(raw), `"input_video_duration":"0.000000000000000001"`)
		require.Contains(t, string(raw), `"duration":"1.000000000000000001"`)
	}
}
