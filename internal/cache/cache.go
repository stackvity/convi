package cache

import (
	"bytes"
	"crypto/sha256"
	"encoding/gob"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync" // *** IMPORT sync PACKAGE ***
	"time"

	"github.com/stackvity/convi/internal/config"
	"github.com/stackvity/convi/internal/filesystem"
)

// CacheStatus represents the status of a cache check.
type CacheStatus string

const (
	// StatusHit indicates the file was found in the cache and is valid.
	StatusHit CacheStatus = "Hit"
	// StatusMiss indicates the file was not found or is invalid.
	StatusMiss CacheStatus = "Miss"
	// StatusError indicates an error occurred during the cache check.
	StatusError CacheStatus = "Error"
)

// CacheEntry stores metadata about a processed file.
// Corresponds to data structure definition in system-outline.md#section-4.
type CacheEntry struct {
	ModTime      time.Time `json:"mod_time"` // Using JSON tags for potential readability if needed, gob ignores them.
	Size         int64     `json:"size"`
	ConfigHash   []byte    `json:"config_hash"`   // Hash of relevant config sections.
	TemplateHash []byte    `json:"template_hash"` // Hash of custom template content (or null hash).
	SourceHash   []byte    `json:"source_hash"`   // Optional: Hash of the source file content.
	OutputPath   string    `json:"output_path"`   // Path where the output was written.
}

// CacheManager defines the interface for cache operations.
// Corresponds to interface definition in system-outline.md#section-2.
type CacheManager interface {
	// Check determines the cache status for a given file based on metadata and hashes.
	// Corresponds to Capability P.2.1.
	Check(filePath string, currentConfigHash []byte, currentTemplateHash []byte) (CacheStatus, error)

	// Update stores or updates the cache entry for a successfully processed file.
	// Corresponds to Capability P.2.2.
	Update(filePath string, entry CacheEntry) error

	// Persist writes the current in-memory cache state to the persistent storage (e.g., file).
	// Corresponds to Capability P.2.4.
	Persist() error

	// Clear removes all entries from the cache (both in-memory and potentially persistent storage).
	// Implied by P.2.5 logic which calls this internally.
	Clear() error
}

// fileCacheManager implements CacheManager using a local file.
// Corresponds to concrete implementation mentioned in system-outline.md#section-2.
type fileCacheManager struct {
	filePath     string // Path to the .convi.cache file
	fs           filesystem.FileSystem
	logger       *slog.Logger
	cacheData    map[string]CacheEntry // In-memory cache
	isDirty      bool                  // Tracks if the cache needs persisting
	configHash   []byte                // Config hash for the current run (used if Check doesn't provide it)
	templateHash []byte                // Template hash for the current run (used if Check doesn't provide it)
	mu           sync.RWMutex          // *** ADD MUTEX FOR CONCURRENT ACCESS ***
}

// NewFileCacheManager creates a new file-based cache manager.
// Loads existing cache from disk if found.
// TASK-CONVI-022: Define CacheManager interface and CacheEntry struct (fulfilled by definitions above and this struct).
func NewFileCacheManager(cacheFilePath string, fs filesystem.FileSystem, logger *slog.Logger) (CacheManager, error) {
	cm := &fileCacheManager{
		filePath:  cacheFilePath,
		fs:        fs,
		logger:    logger,
		cacheData: make(map[string]CacheEntry),
		isDirty:   false,
		// mu is initialized automatically
	}

	// No lock needed here as it's called during single-threaded initialization
	if err := cm.load(); err != nil {
		// If cache file doesn't exist or is corrupted, log it but start fresh.
		// Don't treat as a fatal error.
		cm.logger.Warn("Failed to load cache file, starting with empty cache", "file", cacheFilePath, "error", err)
		cm.cacheData = make(map[string]CacheEntry) // Ensure cache is empty
		cm.isDirty = true                          // Mark dirty to save the (empty) cache on first successful run
	} else {
		cm.logger.Debug("Cache loaded successfully", "file", cacheFilePath, "entries", len(cm.cacheData))
	}

	return cm, nil
}

// load reads the cache file from disk and deserializes it.
// No lock needed if called only during single-threaded initialization.
func (cm *fileCacheManager) load() error {
	data, err := cm.fs.ReadFile(cm.filePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			cm.logger.Info("Cache file not found, creating new cache.", "file", cm.filePath)
			return nil // Not an error, just means start fresh
		}
		return fmt.Errorf("failed to read cache file '%s': %w", cm.filePath, err)
	}

	// Handle empty cache file case
	if len(data) == 0 {
		cm.logger.Info("Cache file is empty, initializing new cache.", "file", cm.filePath)
		cm.cacheData = make(map[string]CacheEntry)
		return nil
	}

	decoder := gob.NewDecoder(bytes.NewReader(data))
	// Use a temporary map to decode into, then swap under lock if needed,
	// though if only called at init, direct decode is fine.
	// Adding lock for safety if load could theoretically be called later.
	cm.mu.Lock() // Lock before modifying shared state
	defer cm.mu.Unlock()
	if err := decoder.Decode(&cm.cacheData); err != nil {
		// Log specific decode error for better debugging
		cm.logger.Error("Failed to decode cache file, cache will be rebuilt.", "file", cm.filePath, "error", err)
		// Return the error so the caller knows loading failed, but allow fallback to empty cache.
		return fmt.Errorf("failed to decode cache file '%s': %w", cm.filePath, err)
	}
	return nil
}

// Check implements the CacheManager interface.
// TASK-CONVI-030: Implement `RealCacheManager.Check` logic.
// TASK-CONVI-074: Include template hash in check.
// TASK-CONVI-075: Include relevant config hash in check.
// TASK-CONVI-083: Optionally include source hash in check.
func (cm *fileCacheManager) Check(filePath string, currentConfigHash []byte, currentTemplateHash []byte) (CacheStatus, error) {
	absFilePath, err := filepath.Abs(filePath)
	if err != nil {
		cm.logger.Warn("Failed to get absolute path for cache check", "file", filePath, "error", err)
		return StatusError, fmt.Errorf("failed to get absolute path for '%s': %w", filePath, err)
	}

	// *** ACQUIRE READ LOCK ***
	cm.mu.RLock()
	entry, found := cm.cacheData[absFilePath]
	cm.mu.RUnlock() // *** RELEASE READ LOCK ***

	if !found {
		cm.logger.Debug("Cache miss: File not found in cache", "file", absFilePath)
		return StatusMiss, nil
	}

	// 1. Get current file metadata
	info, err := cm.fs.Stat(filePath) // Use original path for Stat
	if err != nil {
		cm.logger.Warn("Cache check failed: Could not stat file", "file", filePath, "error", err)
		// Treat stat error as a cache miss, but log it. Don't return StatusError unless check itself fails badly.
		return StatusMiss, nil // Report as Miss, processing will occur
	}

	// 2. Compare basic metadata (ModTime, Size)
	if !info.ModTime().Equal(entry.ModTime) || info.Size() != entry.Size {
		cm.logger.Debug("Cache miss: File metadata mismatch (ModTime/Size)", "file", absFilePath, "cached_mod", entry.ModTime, "current_mod", info.ModTime(), "cached_size", entry.Size, "current_size", info.Size())
		return StatusMiss, nil
	}

	// 3. Compare config hash
	if !bytes.Equal(currentConfigHash, entry.ConfigHash) {
		cm.logger.Debug("Cache miss: Configuration hash mismatch", "file", absFilePath)
		return StatusMiss, nil
	}

	// 4. Compare template hash
	if !bytes.Equal(currentTemplateHash, entry.TemplateHash) {
		cm.logger.Debug("Cache miss: Template hash mismatch", "file", absFilePath)
		return StatusMiss, nil
	}

	// 5. Optional: Compare source hash (TASK-CONVI-083)
	// This provides robustness if ModTime/Size are unreliable, but adds I/O cost.
	// Only perform if entry.SourceHash is populated (indicating it was calculated previously)
	// and if the feature is enabled (e.g., via config or build tag - simplified here).
	if len(entry.SourceHash) > 0 {
		currentSourceHash, err := calculateSourceHash(filePath, cm.fs)
		if err != nil {
			cm.logger.Warn("Cache check failed: Could not calculate source hash", "file", filePath, "error", err)
			return StatusMiss, nil // Treat hash error as Miss
		}
		if !bytes.Equal(currentSourceHash, entry.SourceHash) {
			cm.logger.Debug("Cache miss: Source content hash mismatch", "file", absFilePath)
			return StatusMiss, nil
		}
		cm.logger.Debug("Cache key part match: Source content hash", "file", absFilePath)
	} else {
		cm.logger.Debug("Cache check: Skipping source hash comparison (not present in cache entry or feature disabled)", "file", absFilePath)
	}

	// All checks passed
	cm.logger.Debug("Cache hit", "file", absFilePath)
	return StatusHit, nil
}

// Update implements the CacheManager interface.
// TASK-CONVI-031: Implement `RealCacheManager.Update` logic.
func (cm *fileCacheManager) Update(filePath string, entry CacheEntry) error {
	absFilePath, err := filepath.Abs(filePath)
	if err != nil {
		// Log error but don't fail the entire operation, just skip caching this file.
		cm.logger.Warn("Failed to get absolute path for cache update, skipping cache update for this file", "file", filePath, "error", err)
		return nil // Don't return error, just couldn't cache it.
	}

	// *** ACQUIRE WRITE LOCK ***
	cm.mu.Lock()
	defer cm.mu.Unlock() // *** RELEASE WRITE LOCK ***

	cm.cacheData[absFilePath] = entry // <<< THIS IS THE WRITE OPERATION
	cm.isDirty = true
	cm.logger.Debug("Cache entry updated in memory", "file", absFilePath)
	return nil
}

// Persist implements the CacheManager interface.
// TASK-CONVI-032: Implement `RealCacheManager.Persist` logic (atomic write).
func (cm *fileCacheManager) Persist() error {
	// *** ACQUIRE WRITE LOCK (before checking isDirty and reading cacheData) ***
	cm.mu.Lock()
	// Defer unlock until after potential write operations
	// defer cm.mu.Unlock() // Moved down

	if !cm.isDirty {
		cm.logger.Debug("Cache persistence skipped: Cache not dirty.")
		cm.mu.Unlock() // *** RELEASE LOCK if not dirty ***
		return nil     // Nothing to persist
	}

	startTime := time.Now()
	cm.logger.Debug("Persisting cache...", "file", cm.filePath, "entries", len(cm.cacheData))

	// Create a copy of the data to encode *while holding the lock*
	// This prevents other goroutines from modifying it during encoding.
	cacheDataCopy := make(map[string]CacheEntry, len(cm.cacheData))
	for k, v := range cm.cacheData {
		cacheDataCopy[k] = v
	}
	// *** RELEASE LOCK *after* copying data for encoding ***
	cm.mu.Unlock()

	// Encode the *copy* outside the lock
	var buffer bytes.Buffer
	encoder := gob.NewEncoder(&buffer)
	if err := encoder.Encode(cacheDataCopy); err != nil {
		// Encoding failed, nothing was written, no state changed regarding lock
		return fmt.Errorf("failed to encode cache data: %w", err)
	}

	// Atomic write: Write to temp file then rename (FS operations are generally safe)
	tempFilePath := cm.filePath + ".tmp" + fmt.Sprintf(".%d", time.Now().UnixNano())
	perm := os.FileMode(0644) // Standard file permissions

	// Ensure directory exists (needed for temp file)
	cacheDir := filepath.Dir(cm.filePath)
	if err := cm.fs.MkdirAll(cacheDir, 0755); err != nil {
		return fmt.Errorf("failed to ensure cache directory exists '%s': %w", cacheDir, err)
	}

	if err := cm.fs.WriteFile(tempFilePath, buffer.Bytes(), perm); err != nil {
		// Attempt to remove the temporary file if write failed
		_ = cm.fs.Remove(tempFilePath)
		return fmt.Errorf("failed to write temporary cache file '%s': %w", tempFilePath, err)
	}

	// Attempt atomic rename
	if err := cm.fs.Rename(tempFilePath, cm.filePath); err != nil {
		// Cleanup temp file on rename failure
		_ = cm.fs.Remove(tempFilePath)
		return fmt.Errorf("failed to rename temporary cache file to '%s': %w", cm.filePath, err)
	}

	// *** ACQUIRE WRITE LOCK *again* to safely update isDirty ***
	cm.mu.Lock()
	cm.isDirty = false // Cache is now persisted
	cm.mu.Unlock()     // *** RELEASE LOCK ***

	duration := time.Since(startTime)
	cm.logger.Debug("Cache persisted successfully", "file", cm.filePath, "duration", duration)
	return nil
}

// Clear implements the CacheManager interface.
// Called internally by `--clear-cache` logic or potentially other scenarios.
func (cm *fileCacheManager) Clear() error {
	cm.logger.Info("Clearing cache...", "file", cm.filePath)

	// *** ACQUIRE WRITE LOCK ***
	cm.mu.Lock()
	cm.cacheData = make(map[string]CacheEntry) // Clear in-memory
	cm.isDirty = true                          // Mark as dirty to persist the empty state
	cm.mu.Unlock()                             // *** RELEASE LOCK ***

	// Attempt to delete the cache file on disk
	// FS operations are usually safe, no lock needed around os.Remove
	err := cm.fs.Remove(cm.filePath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		// Log error but don't necessarily fail the operation, cache is already cleared in memory.
		// Return the error as per recommendation B.2 analysis outcome (Keep original behavior for now).
		cm.logger.Warn("Failed to delete cache file on disk during clear", "file", cm.filePath, "error", err)
		// Return nil here as clearing memory succeeded, and failure to delete the file isn't fatal for next run.
		return nil
	} else if errors.Is(err, os.ErrNotExist) {
		cm.logger.Debug("Cache file not found on disk during clear, nothing to delete.", "file", cm.filePath)
	} else {
		cm.logger.Debug("Cache file deleted successfully from disk.", "file", cm.filePath)
	}

	return nil
}

// noOpCacheManager provides a CacheManager implementation that does nothing.
type noOpCacheManager struct{}

// NewNoOpCacheManager creates a CacheManager that performs no operations.
func NewNoOpCacheManager() CacheManager {
	return &noOpCacheManager{}
}

func (n *noOpCacheManager) Check(filePath string, currentConfigHash []byte, currentTemplateHash []byte) (CacheStatus, error) {
	return StatusMiss, nil
}

func (n *noOpCacheManager) Update(filePath string, entry CacheEntry) error {
	return nil
}

func (n *noOpCacheManager) Persist() error {
	return nil
}

func (n *noOpCacheManager) Clear() error {
	return nil
}

// --- Helper Functions ---

// CalculateConfigHash calculates a hash of the relevant configuration options.
// (TASK-CONVI-033 / TASK-CONVI-075 implication: Define which parts of config affect cache.)
func CalculateConfigHash(opts *config.Options) ([]byte, error) {
	// Select only the options that should invalidate the cache if changed.
	cacheRelevantConfig := struct {
		LanguageMappings map[string]string        `json:"languageMappings"`
		FrontMatter      config.FrontMatterConfig `json:"frontMatter"`
		BinaryMode       string                   `json:"binaryMode"`
		LargeFileMode    string                   `json:"largeFileMode"`
		// Add other relevant fields here, e.g.,
		// CustomProcessingFlags map[string]bool `json:"customProcessingFlags"`
	}{
		LanguageMappings: opts.LanguageMappings,
		FrontMatter:      opts.FrontMatter,
		BinaryMode:       opts.BinaryMode,
		LargeFileMode:    opts.LargeFileMode,
	}

	// Use JSON marshalling for a stable representation (keys are sorted).
	configBytes, err := json.Marshal(cacheRelevantConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal config for hashing: %w", err)
	}

	hash := sha256.Sum256(configBytes)
	return hash[:], nil // Return slice of the hash array
}

// CalculateTemplateHash calculates a hash of the template file content.
// Returns a nil hash if templateFilePath is empty.
// (TASK-CONVI-033 / TASK-CONVI-074 implication.)
func CalculateTemplateHash(templateFilePath string, fs filesystem.FileSystem) ([]byte, error) {
	if templateFilePath == "" {
		return nil, nil // No template, use nil hash consistently
	}

	templateContent, err := fs.ReadFile(templateFilePath)
	if err != nil {
		// Distinguish between file not found (could be transient or expected if optional) vs. read error
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("template file '%s' not found for hashing: %w", templateFilePath, err)
		}
		return nil, fmt.Errorf("failed to read template file '%s' for hashing: %w", templateFilePath, err)
	}

	hash := sha256.Sum256(templateContent)
	return hash[:], nil
}

// calculateSourceHash calculates the SHA256 hash of a file's content.
// (TASK-CONVI-083 implication.)
func calculateSourceHash(filePath string, fs filesystem.FileSystem) ([]byte, error) {
	content, err := fs.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read source file '%s' for hashing: %w", filePath, err)
	}
	hash := sha256.Sum256(content)
	return hash[:], nil
}

// DefaultCacheFileName defines the standard name for the cache file.
const DefaultCacheFileName = ".convi.cache"

// DefaultCachePath calculates the default path for the cache file,
// placing it directly within the specified base directory (usually the output directory).
func DefaultCachePath(baseDir string) string {
	// Ensure baseDir is cleaned
	cleanBaseDir := filepath.Clean(baseDir)
	return filepath.Join(cleanBaseDir, DefaultCacheFileName)
}
