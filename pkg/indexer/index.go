// Package indexer builds hierarchical Bloom filter indices over entire codebases.
package indexer

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/carlpatchett/nullspace/pkg/bloom"
	"github.com/carlpatchett/nullspace/pkg/extractor"
	"github.com/carlpatchett/nullspace/pkg/telemetry"
)

// DefaultExcludes contains directories skipped by default.
var DefaultExcludes = []string{
	".git",
	".jj",
	".hg",
	".svn",
	"node_modules",
	".venv",
	"venv",
	"__pycache__",
	".pytest_cache",
	".mypy_cache",
	".ruff_cache",
	"dist",
	"build",
	"target",
	"bin",
	"obj",
	".idea",
	".vscode",
	".cache",
	".omp",
}

// Config specifies workspace indexing parameters.
type Config struct {
	RootPath    string
	Excludes    []string
	NumBits     uint64
	HashCount   int
	MaxFileSize int64
	WorkerCount int
	Telemetry   *telemetry.Telemetry
}

// DefaultConfig creates a standard index configuration.
func DefaultConfig(rootPath string) Config {
	if rootPath == "" {
		rootPath = "."
	}
	return Config{
		RootPath:    rootPath,
		Excludes:    DefaultExcludes,
		NumBits:     bloom.DefaultNumBits,
		HashCount:   bloom.DefaultHashCount,
		MaxFileSize: 2 * 1024 * 1024, // 2 MB max per file
		WorkerCount: runtime.NumCPU(),
	}
}

// FileEntry stores indexed info and Bloom filter for a single file.
type FileEntry struct {
	RelPath    string
	Dir        string
	Filter     *bloom.Filter
	TokenCount int
	Language   string
	Size       int64
	ModTime    time.Time
}

// DirNode represents a directory in the workspace tree with Bloom filters.
type DirNode struct {
	RelPath         string
	DirectFilter    *bloom.Filter // Files directly in this directory
	RecursiveFilter *bloom.Filter // Files in this directory + all subdirectories
	DirectFiles     []string      // Relative paths of direct files
	Subdirs         []string      // Relative paths of immediate subdirectories
	FileCount       int           // Total files in subtree
	TokenCount      uint64        // Estimated tokens in subtree
}

// WorkspaceIndex manages in-memory Bloom filters for the entire workspace.
type WorkspaceIndex struct {
	mu           sync.RWMutex
	cfg          Config
	telemetry    *telemetry.Telemetry
	rootAbs      string
	files        map[string]*FileEntry // key: normalized relative path
	dirs         map[string]*DirNode   // key: normalized relative directory ("." for root)
	languages    map[string]int        // count of files per language/extension
	built        bool
	lastBuildDur float64
	lastBuildAt  time.Time
}

// New creates an unbuilt WorkspaceIndex.
func New(cfg Config) (*WorkspaceIndex, error) {
	absPath, err := filepath.Abs(cfg.RootPath)
	if err != nil {
		return nil, fmt.Errorf("invalid root path %q: %w", cfg.RootPath, err)
	}

	if cfg.NumBits == 0 {
		cfg.NumBits = bloom.DefaultNumBits
	}
	if cfg.HashCount == 0 {
		cfg.HashCount = bloom.DefaultHashCount
	}
	if cfg.WorkerCount <= 0 {
		cfg.WorkerCount = runtime.NumCPU()
	}
	if cfg.MaxFileSize <= 0 {
		cfg.MaxFileSize = 2 * 1024 * 1024
	}

	return &WorkspaceIndex{
		cfg:       cfg,
		telemetry: cfg.Telemetry,
		rootAbs:   absPath,
		files:     make(map[string]*FileEntry),
		dirs:      make(map[string]*DirNode),
		languages: make(map[string]int),
	}, nil
}

// Build scans the workspace and constructs file and directory Bloom filters.
func (idx *WorkspaceIndex) Build() (ReindexResult, error) {
	start := time.Now()
	span := idx.telemetry.StartSpan("build_index", telemetry.A("root", idx.rootAbs))
	defer span.End()

	excludeMap := make(map[string]bool, len(idx.cfg.Excludes))
	for _, ex := range idx.cfg.Excludes {
		excludeMap[strings.ToLower(ex)] = true
	}

	// 1. Discover all candidate files
	type fileJob struct {
		absPath string
		relPath string
		dirRel  string
		size    int64
		modTime time.Time
	}

	var jobs []fileJob
	dirSet := make(map[string]bool)
	dirSet["."] = true
	skippedCount := 0

	err := filepath.WalkDir(idx.rootAbs, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // ignore unreadable paths
		}

		name := d.Name()
		if d.IsDir() {
			if path != idx.rootAbs && excludeMap[strings.ToLower(name)] {
				return filepath.SkipDir
			}
			rel, relErr := filepath.Rel(idx.rootAbs, path)
			if relErr == nil && rel != "." {
				dirSet[filepath.ToSlash(rel)] = true
			}
			return nil
		}

		// File checks
		rel, relErr := filepath.Rel(idx.rootAbs, path)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)

		info, infoErr := d.Info()
		if infoErr != nil {
			return nil
		}

		if info.Size() > idx.cfg.MaxFileSize {
			skippedCount++
			return nil
		}

		dirRel := filepath.ToSlash(filepath.Dir(rel))
		if dirRel == "" {
			dirRel = "."
		}
		dirSet[dirRel] = true

		jobs = append(jobs, fileJob{
			absPath: path,
			relPath: rel,
			dirRel:  dirRel,
			size:    info.Size(),
			modTime: info.ModTime(),
		})
		return nil
	})

	if err != nil {
		return ReindexResult{}, fmt.Errorf("failed walking workspace: %w", err)
	}

	// 2. Parallel token extraction and Bloom filter building
	jobChan := make(chan fileJob, len(jobs))
	for _, job := range jobs {
		jobChan <- job
	}
	close(jobChan)

	type fileResult struct {
		entry *FileEntry
		lang  string
	}

	resultChan := make(chan fileResult, len(jobs))
	var wg sync.WaitGroup

	extOpts := extractor.DefaultExtractOptions()
	workerCount := min(idx.cfg.WorkerCount, len(jobs))
	if workerCount < 1 {
		workerCount = 1
	}

	for range workerCount {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobChan {
				content, readErr := os.ReadFile(job.absPath)
				if readErr != nil {
					continue
				}

				tokens := extractor.ExtractTokens(job.relPath, content, extOpts)
				fb := bloom.NewWithConfig(bloom.Config{
					NumBits:   idx.cfg.NumBits,
					HashCount: idx.cfg.HashCount,
				})

				for tok := range tokens.Tokens {
					fb.AddString(tok)
				}

				ext := filepath.Ext(job.relPath)
				lang := strings.TrimPrefix(ext, ".")
				if lang == "" {
					lang = "unknown"
				}

				resultChan <- fileResult{
					entry: &FileEntry{
						RelPath:    job.relPath,
						Dir:        job.dirRel,
						Filter:     fb,
						TokenCount: tokens.Len(),
						Language:   lang,
						Size:       job.size,
						ModTime:    job.modTime,
					},
					lang: lang,
				}
			}
		}()
	}

	wg.Wait()
	close(resultChan)

	// 3. Assemble index structures
	newFiles := make(map[string]*FileEntry, len(jobs))
	newDirs := make(map[string]*DirNode, len(dirSet))
	newLangs := make(map[string]int)

	for dir := range dirSet {
		newDirs[dir] = &DirNode{
			RelPath:         dir,
			DirectFilter:    bloom.NewWithConfig(bloom.Config{NumBits: idx.cfg.NumBits, HashCount: idx.cfg.HashCount}),
			RecursiveFilter: bloom.NewWithConfig(bloom.Config{NumBits: idx.cfg.NumBits, HashCount: idx.cfg.HashCount}),
		}
	}

	for res := range resultChan {
		e := res.entry
		newFiles[e.RelPath] = e
		newLangs[res.lang]++

		// Add to direct directory filter
		if dirNode, ok := newDirs[e.Dir]; ok {
			dirNode.DirectFiles = append(dirNode.DirectFiles, e.RelPath)
			_ = dirNode.DirectFilter.Union(e.Filter)
		}
	}

	// 4. Build directory hierarchy relationships and recursive bloom filters
	for dir := range newDirs {
		if dir == "." {
			continue
		}
		parent := filepath.ToSlash(filepath.Dir(dir))
		if parent == "" {
			parent = "."
		}
		if pNode, ok := newDirs[parent]; ok {
			pNode.Subdirs = append(pNode.Subdirs, dir)
		}
	}

	// Bottom-up topological aggregation for recursive bloom filters
	allDirsList := make([]string, 0, len(newDirs))
	for dir := range newDirs {
		allDirsList = append(allDirsList, dir)
	}
	// Sort by depth descending (deepest directories first)
	sort.Slice(allDirsList, func(i, j int) bool {
		depthI := strings.Count(allDirsList[i], "/")
		depthJ := strings.Count(allDirsList[j], "/")
		if allDirsList[i] == "." {
			depthI = -1
		}
		if allDirsList[j] == "." {
			depthJ = -1
		}
		return depthI > depthJ
	})

	for _, dir := range allDirsList {
		node := newDirs[dir]
		// Start recursive filter with direct filter
		_ = node.RecursiveFilter.Union(node.DirectFilter)
		node.FileCount = len(node.DirectFiles)

		// Union all immediate child recursive filters
		for _, childDir := range node.Subdirs {
			if childNode, ok := newDirs[childDir]; ok {
				_ = node.RecursiveFilter.Union(childNode.RecursiveFilter)
				node.FileCount += childNode.FileCount
			}
		}
		node.TokenCount = node.RecursiveFilter.EstimatedCount()
	}

	duration := float64(time.Since(start).Nanoseconds()) / 1e6

	idx.mu.Lock()
	idx.files = newFiles
	idx.dirs = newDirs
	idx.languages = newLangs
	idx.built = true
	idx.lastBuildDur = duration
	idx.lastBuildAt = time.Now()
	idx.mu.Unlock()

	span.SetAttribute("indexed_files", len(newFiles))
	span.SetAttribute("indexed_dirs", len(newDirs))
	span.SetAttribute("duration_ms", duration)

	return ReindexResult{
		TotalFiles:       len(newFiles),
		TotalDirectories: len(newDirs),
		SkippedFiles:     skippedCount,
		Languages:        newLangs,
		DurationMS:       duration,
	}, nil
}

func (idx *WorkspaceIndex) ensureBuilt() error {
	idx.mu.RLock()
	built := idx.built
	idx.mu.RUnlock()

	if !built {
		_, err := idx.Build()
		return err
	}
	return nil
}

// PruneSearchSpace evaluates the Bloom filters across all workspace directories
// to eliminate directories that mathematically cannot contain the queried token.
func (idx *WorkspaceIndex) PruneSearchSpace(query string, caseSensitive bool) (PruneResult, error) {
	start := time.Now()
	if err := idx.ensureBuilt(); err != nil {
		return PruneResult{}, err
	}

	span := idx.telemetry.StartSpan("prune_search_space",
		telemetry.A("query", query),
		telemetry.A("case_sensitive", caseSensitive),
	)
	defer span.End()

	cleanQuery := strings.TrimSpace(query)
	if cleanQuery == "" {
		return PruneResult{
			Query: cleanQuery,
			Hint:  "Empty query matches no code.",
		}, nil
	}

	idx.mu.RLock()
	defer idx.mu.RUnlock()

	totalDirs := len(idx.dirs)
	totalFiles := len(idx.files)

	var candidateDirs []CandidateDir
	var prunedDirs []string
	candidateFilesCount := 0

	// Check each directory
	for dirPath, dirNode := range idx.dirs {
		matchDirect := false
		if caseSensitive {
			matchDirect = dirNode.DirectFilter.ContainsString(cleanQuery)
		} else {
			matchDirect = dirNode.DirectFilter.ContainsString(cleanQuery) ||
				dirNode.DirectFilter.ContainsString(strings.ToLower(cleanQuery))
		}

		if matchDirect {
			// Find specific candidate files in this direct directory
			var candFiles []string
			for _, fileRel := range dirNode.DirectFiles {
				if fEntry, ok := idx.files[fileRel]; ok {
					fileMatch := false
					if caseSensitive {
						fileMatch = fEntry.Filter.ContainsString(cleanQuery)
					} else {
						fileMatch = fEntry.Filter.ContainsString(cleanQuery) ||
							fEntry.Filter.ContainsString(strings.ToLower(cleanQuery))
					}
					if fileMatch {
						candFiles = append(candFiles, fileRel)
					}
				}
			}

			if len(candFiles) > 0 {
				sort.Strings(candFiles)
				candidateDirs = append(candidateDirs, CandidateDir{
					Path:           dirPath,
					CandidateFiles: candFiles,
					MatchCount:     len(candFiles),
				})
				candidateFilesCount += len(candFiles)
			} else {
				prunedDirs = append(prunedDirs, dirPath)
			}
		} else {
			prunedDirs = append(prunedDirs, dirPath)
		}
	}

	sort.Slice(candidateDirs, func(i, j int) bool {
		return candidateDirs[i].Path < candidateDirs[j].Path
	})
	sort.Strings(prunedDirs)

	prunedDirCount := len(prunedDirs)
	candidateDirCount := len(candidateDirs)
	prunedPercent := 0.0
	if totalDirs > 0 {
		prunedPercent = (float64(prunedDirCount) / float64(totalDirs)) * 100.0
	}

	prunedFilesCount := totalFiles - candidateFilesCount
	prunedFilesPercent := 0.0
	if totalFiles > 0 {
		prunedFilesPercent = (float64(prunedFilesCount) / float64(totalFiles)) * 100.0
	}

	duration := float64(time.Since(start).Nanoseconds()) / 1e6

	var hint string
	if candidateDirCount == 0 {
		hint = fmt.Sprintf("Token %q definitively does not exist in any indexed file (100%% space pruned).", cleanQuery)
	} else {
		hint = fmt.Sprintf("Pruned %d of %d directories (%.1f%% reduction). Token %q may exist in %d candidate directories (%d files).",
			prunedDirCount, totalDirs, prunedPercent, cleanQuery, candidateDirCount, candidateFilesCount)
	}

	span.SetAttribute("candidate_dirs", candidateDirCount)
	span.SetAttribute("pruned_dirs", prunedDirCount)
	span.SetAttribute("pruned_percent", prunedPercent)
	span.SetAttribute("candidate_files", candidateFilesCount)
	span.SetAttribute("pruned_files", prunedFilesCount)
	span.SetAttribute("duration_ms", duration)

	return PruneResult{
		Query:                cleanQuery,
		CaseSensitive:        caseSensitive,
		TotalDirectories:     totalDirs,
		CandidateDirectories: candidateDirs,
		PrunedDirectories:    prunedDirs,
		CandidateDirCount:    candidateDirCount,
		PrunedDirCount:       prunedDirCount,
		PrunedPercent:        mathRound1(prunedPercent),
		TotalFiles:           totalFiles,
		CandidateFiles:       candidateFilesCount,
		PrunedFiles:          prunedFilesCount,
		PrunedFilesPercent:   mathRound1(prunedFilesPercent),
		DurationMS:           duration,
		Hint:                 hint,
	}, nil
}

func mathRound1(val float64) float64 {
	return float64(int64(val*10+0.5)) / 10
}

// CheckToken tests whether a token exists in a specific file or directory.
func (idx *WorkspaceIndex) CheckToken(query, targetPath string) (CheckTokenResult, error) {
	if err := idx.ensureBuilt(); err != nil {
		return CheckTokenResult{}, err
	}

	span := idx.telemetry.StartSpan("check_token",
		telemetry.A("query", query),
		telemetry.A("target_path", targetPath),
	)
	defer span.End()

	cleanQuery := strings.TrimSpace(query)
	targetRel := filepath.ToSlash(filepath.Clean(targetPath))
	if targetRel == "" {
		targetRel = "."
	}

	idx.mu.RLock()
	defer idx.mu.RUnlock()

	// Check if target is a file
	if fEntry, ok := idx.files[targetRel]; ok {
		present := fEntry.Filter.ContainsString(cleanQuery) || fEntry.Filter.ContainsString(strings.ToLower(cleanQuery))
		hint := fmt.Sprintf("Token %q is possibly present in %s", cleanQuery, targetRel)
		if !present {
			hint = fmt.Sprintf("Token %q definitively does NOT exist in %s", cleanQuery, targetRel)
		}
		span.SetAttribute("present", present)
		return CheckTokenResult{
			Query:      cleanQuery,
			Path:       targetRel,
			Present:    present,
			Definitive: !present,
			Hint:       hint,
		}, nil
	}

	// Check if target is a directory
	if dirNode, ok := idx.dirs[targetRel]; ok {
		present := dirNode.RecursiveFilter.ContainsString(cleanQuery) ||
			dirNode.RecursiveFilter.ContainsString(strings.ToLower(cleanQuery))
		hint := fmt.Sprintf("Token %q is possibly present in directory subtree %s", cleanQuery, targetRel)
		if !present {
			hint = fmt.Sprintf("Token %q definitively does NOT exist in directory subtree %s", cleanQuery, targetRel)
		}
		span.SetAttribute("present", present)
		return CheckTokenResult{
			Query:      cleanQuery,
			Path:       targetRel,
			Present:    present,
			Definitive: !present,
			Hint:       hint,
		}, nil
	}

	return CheckTokenResult{
		Query:      cleanQuery,
		Path:       targetRel,
		Present:    false,
		Definitive: true,
		Hint:       fmt.Sprintf("Path %q is not tracked in the index", targetRel),
	}, nil
}

// Stats returns workspace index statistics.
func (idx *WorkspaceIndex) Stats() (IndexStats, error) {
	if err := idx.ensureBuilt(); err != nil {
		return IndexStats{}, err
	}

	idx.mu.RLock()
	defer idx.mu.RUnlock()

	var totalTokens uint64
	var totalFPR float64
	var filterCount int

	for _, dirNode := range idx.dirs {
		totalTokens += dirNode.DirectFilter.EstimatedCount()
		totalFPR += dirNode.DirectFilter.CurrentFalsePositiveRate()
		filterCount++
	}

	avgFPR := 0.0
	if filterCount > 0 {
		avgFPR = totalFPR / float64(filterCount)
	}

	memBytes := (len(idx.files) + len(idx.dirs)*2) * int(idx.cfg.NumBits/8)
	memKB := float64(memBytes) / 1024.0

	return IndexStats{
		RootPath:             idx.rootAbs,
		TotalFiles:           len(idx.files),
		TotalDirectories:     len(idx.dirs),
		TotalTokens:          totalTokens,
		MemoryFootprintKB:    memKB,
		AvgFalsePositiveRate: avgFPR,
		Languages:            idx.languages,
		DurationMS:           idx.lastBuildDur,
		LastIndexedAt:        idx.lastBuildAt,
	}, nil
}
