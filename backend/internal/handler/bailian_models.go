package handler

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func writeBailianModelsList(c *gin.Context, ids []string) {
	if c.Request.URL.Path != "/api/v1/models" {
		items := make([]gin.H, 0, len(ids))
		for _, id := range ids {
			items = append(items, gin.H{"id": id, "object": "model", "owned_by": service.PlatformBailian})
		}
		c.JSON(http.StatusOK, gin.H{"object": "list", "data": items})
		return
	}
	page, size := 1, 20
	for _, field := range []struct {
		key  string
		dest *int
	}{{"page_no", &page}, {"page_size", &size}} {
		if raw := c.Query(field.key); raw != "" {
			v, err := strconv.Atoi(raw)
			if err != nil || v < 1 || (field.key == "page_size" && v > 100) || v > 1000000 {
				c.JSON(http.StatusBadRequest, gin.H{"code": "InvalidParameter", "message": "invalid model-list pagination"})
				return
			}
			*field.dest = v
		}
	}
	filtered := make([]string, 0, len(ids))
	for _, id := range ids {
		if model := c.Query("model"); model != "" && id != model {
			continue
		}
		if name := c.Query("name"); name != "" && !strings.Contains(strings.ToLower(id), strings.ToLower(name)) {
			continue
		}
		filtered = append(filtered, id)
	}
	start := min((page-1)*size, len(filtered))
	end := min(start+size, len(filtered))
	items := make([]gin.H, 0, end-start)
	for _, id := range filtered[start:end] {
		items = append(items, gin.H{"model": id, "name": id, "provider": "wan", "capabilities": []string{"VG"}})
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "output": gin.H{"models": items, "total": len(filtered), "page_no": page, "page_size": size}})
}
