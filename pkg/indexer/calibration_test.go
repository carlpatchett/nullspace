package indexer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type GroundTruthTarget struct {
	Token           string   `json:"token"`
	ExpectedDirs    []string `json:"expected_dirs"`
	ExpectedFiles   []string `json:"expected_files"`
	MinPrunePercent float64  `json:"min_prune_percent"`
}

type GroundTruth struct {
	Codebase string              `json:"codebase"`
	Targets  []GroundTruthTarget `json:"targets"`
}

func TestGroundTruthCalibration(t *testing.T) {
	// Locate repository root relative to this test
	gtPath := filepath.Join("..", "..", "evals", "ground_truth.json")
	gtBytes, err := os.ReadFile(gtPath)
	if err != nil {
		t.Fatalf("failed reading ground truth file: %v", err)
	}

	var gt GroundTruth
	if err := json.Unmarshal(gtBytes, &gt); err != nil {
		t.Fatalf("failed parsing ground truth json: %v", err)
	}

	codebasePath := filepath.Join("..", "..", gt.Codebase)
	cfg := DefaultConfig(codebasePath)
	idx, err := New(cfg)
	if err != nil {
		t.Fatalf("failed initializing index: %v", err)
	}

	reindexRes, err := idx.Build()
	if err != nil {
		t.Fatalf("failed building index: %v", err)
	}

	t.Logf("Indexed %d files across %d directories in %.2fms",
		reindexRes.TotalFiles, reindexRes.TotalDirectories, reindexRes.DurationMS)

	for _, target := range gt.Targets {
		t.Run(target.Token, func(t *testing.T) {
			res, err := idx.PruneSearchSpace(target.Token, false)
			if err != nil {
				t.Fatalf("PruneSearchSpace failed: %v", err)
			}

			// 1. Zero False Negatives Check: all expected dirs must be present in candidate dirs
			candidateDirMap := make(map[string]bool)
			for _, cd := range res.CandidateDirectories {
				candidateDirMap[cd.Path] = true
			}

			for _, expDir := range target.ExpectedDirs {
				if !candidateDirMap[expDir] {
					t.Errorf("FALSE NEGATIVE: expected directory %q not found in candidates: %v",
						expDir, candidateDirMap)
				}
			}

			// 2. Expected files check
			candidateFileMap := make(map[string]bool)
			for _, cd := range res.CandidateDirectories {
				for _, cf := range cd.CandidateFiles {
					candidateFileMap[cf] = true
				}
			}
			for _, expFile := range target.ExpectedFiles {
				if !candidateFileMap[expFile] {
					t.Errorf("FALSE NEGATIVE: expected file %q not found in candidate files: %v",
						expFile, candidateFileMap)
				}
			}

			// 3. Minimum Pruning Percent Check
			if res.PrunedPercent < target.MinPrunePercent {
				t.Errorf("prune percent %.1f%% below minimum required %.1f%%",
					res.PrunedPercent, target.MinPrunePercent)
			}

			t.Logf("Token %q: Pruned %d/%d dirs (%.1f%%), %d candidate files in %.3fms",
				target.Token, res.PrunedDirCount, res.TotalDirectories, res.PrunedPercent,
				res.CandidateFiles, res.DurationMS)
		})
	}
}
