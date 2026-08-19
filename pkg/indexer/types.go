package indexer

import (
	"time"
)

// CandidateDir represents a directory that passed the Bloom filter check and may contain the query token.
type CandidateDir struct {
	Path           string   `json:"path"`
	CandidateFiles []string `json:"candidate_files"`
	MatchCount     int      `json:"match_count"`
}

// PruneResult represents the output of a search space pruning query.
type PruneResult struct {
	Query                string         `json:"query"`
	CaseSensitive        bool           `json:"case_sensitive"`
	TotalDirectories     int            `json:"total_directories"`
	CandidateDirectories []CandidateDir `json:"candidate_directories"`
	PrunedDirectories    []string       `json:"pruned_directories"`
	CandidateDirCount    int            `json:"candidate_dir_count"`
	PrunedDirCount       int            `json:"pruned_dir_count"`
	PrunedPercent        float64        `json:"pruned_percent"`
	TotalFiles           int            `json:"total_files"`
	CandidateFiles       int            `json:"candidate_files"`
	PrunedFiles          int            `json:"pruned_files"`
	PrunedFilesPercent   float64        `json:"pruned_files_percent"`
	DurationMS           float64        `json:"duration_ms"`
	Hint                 string         `json:"hint"`
}

// CheckTokenResult represents the result of checking a specific token in a path.
type CheckTokenResult struct {
	Query      string `json:"query"`
	Path       string `json:"path"`
	Present    bool   `json:"present"`
	Definitive bool   `json:"definitive"` // true when Present is false (zero false negatives)
	Hint       string `json:"hint"`
}

// IndexStats provides summary metrics of the indexed workspace.
type IndexStats struct {
	RootPath             string         `json:"root_path"`
	TotalFiles           int            `json:"total_files"`
	TotalDirectories     int            `json:"total_directories"`
	TotalTokens          uint64         `json:"total_tokens"`
	MemoryFootprintKB    float64        `json:"memory_footprint_kb"`
	AvgFalsePositiveRate float64        `json:"avg_false_positive_rate"`
	Languages            map[string]int `json:"languages"`
	DurationMS           float64        `json:"duration_ms"`
	LastIndexedAt        time.Time      `json:"last_indexed_at"`
}

// ReindexResult provides stats about a completed re-indexing operation.
type ReindexResult struct {
	TotalFiles       int            `json:"total_files"`
	TotalDirectories int            `json:"total_directories"`
	SkippedFiles     int            `json:"skipped_files"`
	Languages        map[string]int `json:"languages"`
	DurationMS       float64        `json:"duration_ms"`
}
