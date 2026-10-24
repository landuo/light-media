package plugins

// Brush inventory is a read-only projection, never a future-operation authorization.
const BrushInventoryReadCapability = "brush.inventory.read"

type BrushInventoryV1 struct {
	SchemaVersion int                          `json:"schema_version"`
	Sites         []BrushSiteReferenceV1       `json:"sites"`
	Downloaders   []BrushDownloaderReferenceV1 `json:"downloaders"`
}

type BrushSiteReferenceV1 struct {
	SourceID        int64                  `json:"source_id"`
	Name            string                 `json:"name"`
	Kind            string                 `json:"kind"`
	URL             *string                `json:"url"`
	Domain          *string                `json:"domain"`
	Public          bool                   `json:"public"`
	Enabled         bool                   `json:"enabled"`
	LocalStatistics BrushLocalStatisticsV1 `json:"local_statistics"`
}

type BrushLocalStatisticsV1 struct {
	// "available" means a selected local observation exists, not online,
	// successful, recent, non-null ratio or safe to perform an external operation.
	Status      string   `json:"status"` // "available" or "missing"
	Ratio       *float64 `json:"ratio"`
	Upload      *float64 `json:"upload"`       // bytes, preserve fractional source values
	Download    *float64 `json:"download"`     // bytes
	UpdatedDay  *string  `json:"updated_day"`  // YYYY-MM-DD; Asia/Shanghai
	UpdatedTime *string  `json:"updated_time"` // HH:MM:SS or ""; same timezone
	// Missing: all five pointer values are nil. Never zero-fill.
}

type BrushDownloaderReferenceV1 struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Enabled bool   `json:"enabled"` // downloaderEnabled, NOT connected/Active
}
