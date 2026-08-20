package tools

import (
	"context"
	"fmt"
	"time"
)

type DateTimeTool struct{}

func (t *DateTimeTool) Name() string {
	return "get_current_time"
}

func (t *DateTimeTool) Description() string {
	return "Mengambil waktu, tanggal, hari, dan zona waktu saat ini secara akurat."
}

func (t *DateTimeTool) Parameters() ParametersSchema {
	return ParametersSchema{
		Type: "object",
		Properties: map[string]ParameterProperty{
			"timezone": {
				Type:        "string",
				Description: "Nama zona waktu (contoh: 'Asia/Jakarta', 'UTC', 'America/New_York'). Default ke waktu sistem lokal.",
			},
		},
	}
}

func (t *DateTimeTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	loc := time.Local
	if tz, ok := args["timezone"].(string); ok && tz != "" {
		if loadedLoc, err := time.LoadLocation(tz); err == nil {
			loc = loadedLoc
		}
	}
	now := time.Now().In(loc)
	return fmt.Sprintf("Waktu Saat Ini: %s (Zona: %s, Format ISO: %s)",
		now.Format("Monday, 02 January 2006 15:04:05 MST"),
		now.Location().String(),
		now.Format(time.RFC3339),
	), nil
}
