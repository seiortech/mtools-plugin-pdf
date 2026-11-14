package pdf

import (
	"path/filepath"
	"time"

	"github.com/seiortech/mtools-sdk/job"
)

// HistoryItem records metadata of a completed PDF job.
type HistoryItem struct {
	ID          string    `json:"id"`
	Operation   string    `json:"operation"`
	Summary     string    `json:"summary"`
	OutputFile  string    `json:"output_file"`
	FileCount   int       `json:"file_count"`
	CompletedAt time.Time `json:"completed_at"`
}

// GetID implements job.HistoryRecord.
func (h HistoryItem) GetID() string {
	return h.ID
}

// HistoryStore persists job history to JSON.
type HistoryStore = job.HistoryStore[HistoryItem]

// NewHistoryStore initializes history store from plugin data directory.
func NewHistoryStore(dataDir string) *HistoryStore {
	return job.NewHistoryStore[HistoryItem](filepath.Join(dataDir, "history.json"))
}
