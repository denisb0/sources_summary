package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type ContentSource struct {
	ID        uuid.UUID       `gorm:"column:id;type:uuid" json:"id"`
	EngineID  string          `gorm:"column:engine_id" json:"engine_id"`
	SourceID  string          `gorm:"column:source_id" json:"source_id"`
	URL       string          `gorm:"column:url" json:"url"`
	Options   json.RawMessage `gorm:"column:options;type:jsonb" json:"options,omitempty"`
	Status    string          `gorm:"column:status" json:"status"`
	State     json.RawMessage `gorm:"column:state;type:jsonb" json:"state,omitempty"` // additional data related to source processing (like last checked)
	CreatedAt time.Time       `gorm:"column:created_at" json:"created_at"`
	UpdatedAt time.Time       `gorm:"column:updated_at" json:"updated_at"`
}

func (c ContentSource) TableName() string {
	return "content_source"
}
