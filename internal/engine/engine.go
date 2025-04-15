package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os" // Import os for Stderr
	"path/filepath"
	"runtime" // Import runtime
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/stackvity/convi/internal/cache"
	"github.com/stackvity/convi/internal/config"
	"github.com/stackvity/convi/internal/filesystem"
	"github.com/stackvity/convi/internal/template"
	"github.com/stackvity/convi/internal/worker"
)

var errFailedToAddWatchPaths = errors.New("failed to add one or more paths to the watcher")

// Report summarizes the results of a conversion run.
type Report struct {
	FilesProcessed      int
	FilesSkippedIgnored int
	FilesSkippedBinary  int
	FilesSkippedLarge   int
	FilesFromCache      int
	FilesErrored        int
	Duration            time.Duration
	Errors              map[string]string // Map of FilePath -> ErrorMessage
}

// Engine orchestrates the file conversion process.
type Engine struct {
	Opts          *config.Options
	FS            filesystem.FileSystem
	CM            cache.CacheManager
	Logger        *slog.Logger
	IgnoreMatcher func(string) bool
	TemplateExec  *template.Executor
	ConfigHash    []byte
	TemplateHash  []byte
}

// NewEngine creates a new Engine instance with dependencies.
// It assumes opts.Concurrency has been resolved (e.g., 0 replaced with runtime.NumCPU) before calling.
func NewEngine(
	opts *config.Options,
	fs filesystem.FileSystem,
	cm cache.CacheManager,
	logger *slog.Logger,
	ignoreMatcher func(string) bool,
	templateExec *template.Executor,
	configHash []byte,
	templateHash []byte,
) *Engine {
	return &Engine{
		Opts:          opts,
		FS:            fs,
		CM:            cm,
		Logger:        logger,
		IgnoreMatcher: ignoreMatcher,
		TemplateExec:  templateExec,
		ConfigHash:    configHash,
		TemplateHash:  templateHash,
	}
}

// resolveConcurrency determines the number of workers based on options or CPU cores.
func (e *Engine) resolveConcurrency() int {
	numWorkers := e.Opts.Concurrency
	if numWorkers <= 0 {
		autoWorkers := runtime.NumCPU()
		e.Logger.Debug("Concurrency set to auto-detect", "detected_cores", autoWorkers)
		numWorkers = autoWorkers
		if numWorkers <= 0 {
			e.Logger.Warn("Auto-detected 0 or fewer CPU cores, defaulting to 1 worker")
			numWorkers = 1 // Ensure at least one worker
		}
	}
	return numWorkers
}

// Run executes the main conversion logic, either once or in watch mode.
func (e *Engine) Run(ctx context.Context) (Report, error) {
	startTime := time.Now()
	var report Report
	var err error

	// Initial cache setup (clear or load) and hash calculation happen *before* NewEngine.

	// --- FIX: Use the correct field 'WatchMode' instead of 'Watch.Enabled' ---
	if e.Opts.WatchMode { // <-- Corrected this line
		report, err = e.watch(ctx)
		// Overall duration might be less meaningful in watch mode, but calculate anyway.
		report.Duration = time.Since(startTime)
		return report, err
	}

	// --- Run Once ---
	report, err = e.runOnce(ctx)
	report.Duration = time.Since(startTime)
	if err != nil {
		return report, fmt.Errorf("processing run failed: %w", err) // Keep initial error context
	}

	// Persist cache after successful single run
	if e.Opts.UseCache && e.CM != nil {
		if persistErr := e.CM.Persist(); persistErr != nil {
			e.Logger.Warn("Failed to persist cache", "error", persistErr)
			// Non-fatal, processing completed
		} else {
			e.Logger.Debug("Cache persisted successfully")
		}
	}

	return report, nil
}

// runOnce performs a single full scan and conversion process.
// Task IDs: TASK-CONVI-027, TASK-CONVI-028 (scan), TASK-CONVI-042 (workers), TASK-CONVI-043 (aggregate)
func (e *Engine) runOnce(ctx context.Context) (Report, error) {
	e.Logger.Info("Starting conversion run", "input", e.Opts.Input, "output", e.Opts.Output)
	report := Report{Errors: make(map[string]string)}

	numWorkers := e.resolveConcurrency() // Use helper
	e.Logger.Debug("Resolved concurrency", "workers", numWorkers)

	taskChan := make(chan string, numWorkers*2)
	resultChan := make(chan worker.Result, numWorkers*2)
	var wg sync.WaitGroup

	// Start worker pool (TASK-CONVI-042)
	e.startWorkers(ctx, numWorkers, taskChan, resultChan, &wg)

	// Start result aggregation (TASK-CONVI-043)
	doneAggregating := make(chan struct{})
	go func() {
		defer close(doneAggregating)
		e.aggregateResults(resultChan, &report)
	}()

	// Scan directory and dispatch tasks (TASK-CONVI-028)
	scanErr := e.scanDirectory(ctx, taskChan)

	// Close task channel once scanning is done
	close(taskChan)

	// Wait for all workers to finish
	wg.Wait()

	// Close result channel and wait for aggregation to complete
	close(resultChan)
	<-doneAggregating

	e.Logger.Info("Conversion run finished",
		"processed", report.FilesProcessed,
		"cached", report.FilesFromCache,
		"skipped_ignored", report.FilesSkippedIgnored,
		"skipped_binary", report.FilesSkippedBinary,
		"skipped_large", report.FilesSkippedLarge,
		"errors", report.FilesErrored,
	)

	return report, scanErr
}

// reRun performs an incremental conversion based on a set of changed paths.
func (e *Engine) reRun(ctx context.Context, pathsToProcess map[string]struct{}) (Report, error) {
	e.Logger.Info("Starting incremental rebuild...", "files_changed", len(pathsToProcess))
	report := Report{Errors: make(map[string]string)}

	if len(pathsToProcess) == 0 {
		e.Logger.Info("No changed paths to process in re-run.")
		return report, nil // Nothing to do
	}

	numWorkers := e.resolveConcurrency() // Use helper
	e.Logger.Debug("Resolved concurrency for re-run", "workers", numWorkers)

	taskChan := make(chan string, len(pathsToProcess)) // Size appropriately
	resultChan := make(chan worker.Result, len(pathsToProcess))
	var wg sync.WaitGroup

	// Start worker pool (TASK-CONVI-042)
	e.startWorkers(ctx, numWorkers, taskChan, resultChan, &wg)

	// Start result aggregation (TASK-CONVI-043)
	doneAggregating := make(chan struct{})
	go func() {
		defer close(doneAggregating)
		e.aggregateResults(resultChan, &report)
	}()

	// Dispatch only the changed tasks
	dispatchErr := e.dispatchTasks(ctx, taskChan, pathsToProcess)

	// Close task channel once dispatching is done
	close(taskChan)

	// Wait for all workers to finish
	wg.Wait()

	// Close result channel and wait for aggregation to complete
	close(resultChan)
	<-doneAggregating

	e.Logger.Info("Incremental rebuild finished",
		"processed", report.FilesProcessed,
		"cached", report.FilesFromCache, // Cache hits are still possible if FS event was spurious
		"skipped_ignored", report.FilesSkippedIgnored,
		"skipped_binary", report.FilesSkippedBinary,
		"skipped_large", report.FilesSkippedLarge,
		"errors", report.FilesErrored,
	)

	return report, dispatchErr
}

// dispatchTasks sends specific file paths to the task channel.
func (e *Engine) dispatchTasks(ctx context.Context, taskChan chan<- string, paths map[string]struct{}) error {
	for path := range paths {
		select {
		case <-ctx.Done():
			e.Logger.Info("Task dispatch cancelled")
			return ctx.Err()
		case taskChan <- path:
			e.Logger.Debug("Dispatched task", "file", path)
		}
	}
	e.Logger.Debug("Finished dispatching tasks")
	return nil
}

// startWorkers launches the worker goroutines. (TASK-CONVI-042)
func (e *Engine) startWorkers(ctx context.Context, numWorkers int, taskChan <-chan string, resultChan chan<- worker.Result, wg *sync.WaitGroup) {
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			e.Logger.Debug("Worker started", "id", workerID)

			w := worker.NewWorker(
				e.Opts,
				e.FS,
				e.CM,
				e.Logger,
				e.IgnoreMatcher,
				e.TemplateExec,
				e.ConfigHash,
				e.TemplateHash,
			)

			for {
				select {
				case <-ctx.Done(): // Check for cancellation first (TASK-CONVI-069 implication)
					e.Logger.Debug("Worker shutting down due to context cancellation", "id", workerID)
					return
				case filePath, ok := <-taskChan:
					if !ok {
						e.Logger.Debug("Worker shutting down as task channel closed", "id", workerID)
						return // Exit if task channel is closed
					}
					e.Logger.Debug("Worker received task", "id", workerID, "file", filePath)
					// Process the file using the worker instance
					// TASK-CONVI-044 implication: ProcessFile handles internal error wrapping
					res := w.ProcessFile(ctx, filePath)
					// Send the result back to the engine
					select {
					case resultChan <- res:
					case <-ctx.Done():
						e.Logger.Debug("Worker could not send result due to context cancellation", "id", workerID, "file", filePath)
						return
					}
				}
			}
		}(i)
	}
}

// scanDirectory walks the input directory and sends file paths to the task channel. (TASK-CONVI-028)
func (e *Engine) scanDirectory(ctx context.Context, taskChan chan<- string) error {
	e.Logger.Debug("Starting directory scan", "path", e.Opts.Input)
	var firstScanErr error
	var errMu sync.Mutex // Protect firstScanErr

	err := e.FS.WalkDir(e.Opts.Input, func(path string, d fs.DirEntry, walkErr error) error {
		select {
		case <-ctx.Done():
			return ctx.Err() // Stop walking if context is cancelled
		default:
		}

		if walkErr != nil {
			e.Logger.Warn("Error accessing path during scan", "path", path, "error", walkErr)
			errMu.Lock()
			if firstScanErr == nil {
				firstScanErr = fmt.Errorf("accessing '%s': %w", path, walkErr)
			}
			errMu.Unlock()
			// Stop on the first access error during the walk.
			return walkErr
		}

		// Skip the input directory itself
		if path == e.Opts.Input {
			return nil
		}

		// Apply ignore rules early
		if e.IgnoreMatcher != nil && e.IgnoreMatcher(path) {
			e.Logger.Debug("Skipping ignored path during scan", "path", path)
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		// Skip hidden files/dirs if configured
		if e.Opts.SkipHiddenFiles && len(d.Name()) > 0 && d.Name()[0] == '.' {
			e.Logger.Debug("Skipping hidden path during scan", "path", path)
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if !d.IsDir() {
			select {
			case taskChan <- path:
				e.Logger.Debug("Dispatched task", "file", path)
			case <-ctx.Done():
				e.Logger.Info("Directory scan cancelled while sending task")
				return ctx.Err()
			}
		}
		return nil
	})

	// Handle WalkDir's final returned error
	if err != nil && !errors.Is(err, context.Canceled) {
		errMu.Lock()
		if firstScanErr == nil {
			firstScanErr = fmt.Errorf("directory scan failed: %w", err)
		}
		errMu.Unlock()
	}

	errMu.Lock()
	defer errMu.Unlock()

	if firstScanErr != nil {
		e.Logger.Error("Directory scan completed with errors", "error", firstScanErr)
		return firstScanErr
	}

	e.Logger.Debug("Directory scan completed successfully")
	return nil
}

// aggregateResults collects results from workers and updates the report. (TASK-CONVI-043 & TASK-CONVI-044)
func (e *Engine) aggregateResults(resultChan <-chan worker.Result, report *Report) {
	for res := range resultChan {
		switch res.Status {
		case worker.StatusProcessed:
			report.FilesProcessed++
		case worker.StatusCached:
			report.FilesFromCache++
		case worker.StatusSkipped:
			// Categorize skipped reasons
			switch res.Reason {
			case "Ignored":
				report.FilesSkippedIgnored++
			case "Binary":
				report.FilesSkippedBinary++
			case "Large":
				report.FilesSkippedLarge++
			case "Hidden":
				report.FilesSkippedIgnored++ // Group hidden under ignored
			default:
				e.Logger.Warn("Unknown skip reason", "file", res.FilePath, "reason", res.Reason)
				report.FilesSkippedIgnored++ // Default category if reason is unexpected
			}
		case worker.StatusError:
			report.FilesErrored++
			// Aggregate specific error message
			errMsg := "Unknown processing error"
			if res.Error != nil {
				errMsg = res.Error.Error() // Use the wrapped error message
			}
			report.Errors[res.FilePath] = errMsg
			// Logging level WARN for file errors remains appropriate
			e.Logger.Warn("File processing error reported", "file", res.FilePath, "reason", res.Reason, "error", errMsg)
		default:
			e.Logger.Warn("Received result with unknown status", "status", res.Status, "file", res.FilePath)
		}
	}
	e.Logger.Debug("Result aggregation finished")
}

// watch monitors the filesystem for changes and triggers re-runs. (TASK-CONVI-066, TASK-CONVI-068, TASK-CONVI-069)
func (e *Engine) watch(ctx context.Context) (Report, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return Report{}, fmt.Errorf("failed to create file watcher: %w", err)
	}
	defer watcher.Close()

	if err := e.addPathsToWatcher(watcher); err != nil {
		// Check if it was the specific error indicating some paths failed
		if errors.Is(err, errFailedToAddWatchPaths) {
			e.Logger.Warn("Failed to add some paths to the watcher, proceeding but some changes might be missed", "error", err)
			// Continue, but log the warning
		} else {
			// Treat other errors (e.g., cannot walk input dir at all) as fatal for setup
			return Report{}, fmt.Errorf("failed to add paths to watcher: %w", err)
		}
	}

	// Perform initial run (TASK-CONVI-068 requirement)
	lastReport, initialErr := e.runOnce(ctx)
	if initialErr != nil && !errors.Is(initialErr, context.Canceled) {
		e.Logger.Error("Initial run failed in watch mode", "error", initialErr)
		// Heuristic check for critical errors remains, but log if continuing
		if _, ok := initialErr.(*fs.PathError); ok || errors.Is(initialErr, fs.ErrNotExist) {
			return lastReport, fmt.Errorf("aborting watch mode due to critical initial run failure: %w", initialErr)
		} else {
			e.Logger.Warn("Initial run had non-critical errors, watch mode will continue", "error", initialErr)
			// Continue watching, report contains errors
		}
	} else if initialErr == nil {
		// Print initial summary only if initial run was successful or context wasn't cancelled
		e.printWatchSummary(os.Stderr, lastReport) // Use Stderr
	}

	// Persist cache after initial run
	if e.Opts.UseCache && e.CM != nil {
		if persistErr := e.CM.Persist(); persistErr != nil {
			e.Logger.Warn("Failed to persist cache after initial run in watch mode", "error", persistErr)
		}
	}

	e.Logger.Info("Entering watch mode, monitoring for changes...", "path", e.Opts.Input)

	var debounceTimer *time.Timer
	debounceDuration := e.Opts.Watch.Debounce
	if debounceDuration <= 0 {
		debounceDuration = 300 * time.Millisecond // Default debounce
	}
	pendingPaths := make(map[string]struct{})
	var pendingPathsMu sync.Mutex
	triggerRebuildChan := make(chan struct{}, 1)

	for {
		select {
		case <-ctx.Done(): // Handle graceful shutdown (TASK-CONVI-069)
			e.Logger.Info("Received cancellation signal, exiting watch mode gracefully.")
			if e.Opts.UseCache && e.CM != nil {
				if err := e.CM.Persist(); err != nil {
					e.Logger.Warn("Failed to persist cache during shutdown", "error", err)
				} else {
					e.Logger.Debug("Cache persisted during shutdown.")
				}
			}
			return lastReport, context.Canceled // Return Canceled error

		case event, ok := <-watcher.Events:
			if !ok {
				e.Logger.Warn("File watcher event channel closed unexpectedly.")
				return lastReport, errors.New("watcher event channel closed")
			}
			e.Logger.Debug("Watcher event received", "event", event.String())

			// Basic filtering
			if (e.IgnoreMatcher != nil && e.IgnoreMatcher(event.Name)) ||
				(e.Opts.SkipHiddenFiles && len(event.Name) > 0 && filepath.Base(event.Name)[0] == '.') {
				e.Logger.Debug("Ignoring change in ignored/hidden path", "path", event.Name)
				continue
			}

			// Track relevant events
			if event.Has(fsnotify.Create) || event.Has(fsnotify.Write) || event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename) {
				pendingPathsMu.Lock()
				// Normalize path before adding? Consider edge cases. For now, use raw event name.
				pendingPaths[event.Name] = struct{}{}
				pendingPathsMu.Unlock()

				// Reset debounce timer
				if debounceTimer != nil {
					debounceTimer.Stop()
				}
				debounceTimer = time.AfterFunc(debounceDuration, func() {
					select {
					case triggerRebuildChan <- struct{}{}:
					default:
						e.Logger.Debug("Rebuild trigger channel full, skipping signal")
					}
				})
				e.Logger.Debug("Debounce timer reset", "duration", debounceDuration)
			}

		case <-triggerRebuildChan: // Debounce timer fired
			pendingPathsMu.Lock()
			pathsToProcessNow := make(map[string]struct{}, len(pendingPaths))
			for path := range pendingPaths {
				pathsToProcessNow[path] = struct{}{}
			}
			pendingPaths = make(map[string]struct{})
			pendingPathsMu.Unlock()

			if len(pathsToProcessNow) > 0 {
				// Call triggerReRun, which now returns the report
				rebuildReport, rebuildErr := e.triggerReRun(ctx, &lastReport, pathsToProcessNow)
				if rebuildErr != nil && !errors.Is(rebuildErr, context.Canceled) {
					// Log error from reRun itself (e.g., context cancellation during dispatch)
					// File processing errors are already logged within triggerReRun/aggregateResults
					e.Logger.Error("Rebuild triggering failed", "error", rebuildErr)
				}
				// Update lastReport with the results of the rebuild attempt
				lastReport = rebuildReport
				// Print the summary for this rebuild attempt
				e.printWatchSummary(os.Stderr, lastReport) // Use Stderr
			} else {
				e.Logger.Debug("Debounce timer fired but no pending paths.")
			}
			e.Logger.Info("Watching for changes...") // Indicate readiness for next change

		case err, ok := <-watcher.Errors:
			if !ok {
				e.Logger.Warn("File watcher error channel closed unexpectedly.")
				return lastReport, errors.New("watcher error channel closed")
			}
			// Log fsnotify errors and continue watching for now, as per recommendation A.1
			e.Logger.Error("File watcher error encountered, attempting to continue", "error", err)
			// Consider if specific errors should cause watch mode to exit in the future
		}
	}
}

// addPathsToWatcher recursively adds directories to the fsnotify watcher.
// Returns errFailedToAddWatchPaths if some paths fail, or another error if walking fails.
func (e *Engine) addPathsToWatcher(watcher *fsnotify.Watcher) error {
	addedPaths := make(map[string]bool)
	var encounteredAddError bool = false // Track if any Add call failed

	walkErr := e.FS.WalkDir(e.Opts.Input, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			e.Logger.Warn("Error accessing path during watcher setup", "path", path, "error", err)
			// If the root input dir fails, WalkDir might return the error immediately.
			// Otherwise, try to continue walking other parts.
			return nil
		}

		if d.IsDir() {
			// Skip ignored directories
			if e.IgnoreMatcher != nil && e.IgnoreMatcher(path) {
				e.Logger.Debug("Skipping ignored directory for watching", "path", path)
				return filepath.SkipDir
			}
			// Skip hidden dirs if configured
			if e.Opts.SkipHiddenFiles && len(d.Name()) > 0 && d.Name()[0] == '.' && path != e.Opts.Input {
				e.Logger.Debug("Skipping hidden directory for watching", "path", path)
				return filepath.SkipDir
			}

			// Add path to watcher if not already added
			if !addedPaths[path] {
				e.Logger.Debug("Adding path to watcher", "path", path)
				if addErr := watcher.Add(path); addErr != nil {
					// Log the specific error but don't return it from WalkDir func
					e.Logger.Error("Failed to add path to watcher, continuing...", "path", path, "error", addErr)
					encounteredAddError = true // Mark that at least one error occurred
				} else {
					addedPaths[path] = true
				}
			}
		}
		return nil
	})

	if walkErr != nil {
		// Error walking the directory structure itself
		return fmt.Errorf("error walking input directory for watcher setup: %w", walkErr)
	}

	if encounteredAddError {
		// Indicate that some paths failed to be added
		return errFailedToAddWatchPaths
	}

	return nil // Success
}

// triggerReRun initiates an incremental conversion run in watch mode.
// It now returns the report and error, and no longer prints the summary.
func (e *Engine) triggerReRun(ctx context.Context, lastReport *Report, pathsToProcess map[string]struct{}) (Report, error) {
	e.Logger.Info("Change detected, triggering rebuild...", "changed_count", len(pathsToProcess))

	// Call reRun for targeted processing
	report, err := e.reRun(ctx, pathsToProcess)

	// Update the persistent lastReport state ONLY based on the outcome
	if err == nil { // Re-run was successful (even if there were file processing errors)
		// Clear previous errors before assigning potentially new ones
		lastReport.Errors = nil // Clear previous errors
		*lastReport = report    // Update the state completely
	} else { // Re-run itself failed critically (e.g., context cancelled)
		// Merge errors from the failed re-run attempt into the last known state
		lastReport.FilesErrored += report.FilesErrored // Add errors from the failed attempt
		if report.Errors != nil {
			if lastReport.Errors == nil {
				lastReport.Errors = make(map[string]string)
			}
			for k, v := range report.Errors { // Merge new errors, potentially overwriting
				lastReport.Errors[k] = v
			}
		}
		// Return the critical error from reRun
		return *lastReport, err
	}

	// Persist cache only if the re-run processed files or encountered errors
	if e.Opts.UseCache && e.CM != nil {
		if report.FilesProcessed > 0 || report.FilesErrored > 0 {
			if persistErr := e.CM.Persist(); persistErr != nil {
				e.Logger.Warn("Failed to persist cache after rebuild", "error", persistErr)
			} else {
				e.Logger.Debug("Cache persisted after rebuild.")
			}
		} else {
			e.Logger.Debug("Skipping cache persistence as no files were processed or errored in rebuild.")
		}
	}

	// Return the latest report and nil error (since reRun itself didn't fail critically)
	return *lastReport, nil
}

// printWatchSummary prints the report summary, intended for use in watch mode.
func (e *Engine) printWatchSummary(writer io.Writer, report Report) {
	// Use the provided writer (e.g., os.Stderr)
	fmt.Fprintf(writer, "\n--- Rebuild Summary ---\n")
	fmt.Fprintf(writer, "Duration: %s\n", report.Duration.Round(time.Millisecond))
	fmt.Fprintf(writer, "Files Processed:      %d\n", report.FilesProcessed)
	fmt.Fprintf(writer, "Files From Cache:     %d\n", report.FilesFromCache)
	fmt.Fprintf(writer, "Files Skipped (Ignored): %d\n", report.FilesSkippedIgnored)
	fmt.Fprintf(writer, "Files Skipped (Binary):  %d\n", report.FilesSkippedBinary)
	fmt.Fprintf(writer, "Files Skipped (Large):   %d\n", report.FilesSkippedLarge)
	fmt.Fprintf(writer, "Files Errored:        %d\n", report.FilesErrored)

	if report.FilesErrored > 0 && report.Errors != nil {
		fmt.Fprintf(writer, "\n--- Errors Encountered ---\n")
		for filePath, errMsg := range report.Errors {
			fmt.Fprintf(writer, "ERROR: %s: %s\n", filePath, errMsg)
		}
	}
	fmt.Fprintf(writer, "-----------------------\n\n")
}
