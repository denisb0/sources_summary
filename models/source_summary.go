package models

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"time"
)

type SummaryData struct {
	SourceCount        uint64    `json:"source_count"`       // can be multiple sources for each source id
	Enabled            bool      `json:"enabled"`            // if at least one is processing
	StalledProcessing  bool      `json:"stalled_processing"` // marked as processing but updated timestamp is outdated
	EngagementCheck    bool      `json:"engagement_check"`
	AddedAt            time.Time `json:"added_at"`    // earliest creation date
	EntryCount         uint64    `json:"entry_count"` // not including engagement check
	CompletedCount     uint64    `json:"completed_count"`
	ErrorCount         uint64    `json:"error_count"` // failed entries
	DaysSinceLastEntry float64   `json:"days_since_last_entry"`
	AvgEntriesDay      float64   `json:"avg_entries_day"` // based on completed entries with order=0
	Activity           float64   `json:"activity"`        // value [0..1) indicating if source performs well (new posts appear with expected frequency)
	LLMEnrichedRatio   float64   `json:"llm_enriched"`    // to detect cases without llm enrichment
}

func (d SummaryData) Value() (driver.Value, error) {
	return json.Marshal(d)
}

func (d *SummaryData) Scan(value interface{}) error {
	b, ok := value.([]byte)
	if !ok {
		return errors.New("summary data assertion to []byte failed")
	}

	return json.Unmarshal(b, d)
}

type SourceSummary struct {
	SourceID  string      `gorm:"column:source_id;primaryKey;unique" json:"source_id"`
	Summary   SummaryData `gorm:"column:summary;type:jsonb" json:"summary"`
	CreatedAt time.Time   `gorm:"column:created_at" json:"created_at"`
	UpdatedAt time.Time   `gorm:"column:updated_at" json:"updated_at"`
}

func (c SourceSummary) TableName() string {
	return "source_summary"
}
