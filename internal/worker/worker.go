package worker

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"unicode/utf8" // Added for validation

	"github.com/BurntSushi/toml"
	"gopkg.in/yaml.v3"

	"github.com/stackvity/convi/internal/cache"
	"github.com/stackvity/convi/internal/config"
	"github.com/stackvity/convi/internal/filesystem"
	"github.com/stackvity/convi/internal/template"
)

// Constants for result status
const (
	StatusProcessed = "Processed"
	StatusSkipped   = "Skipped"
	StatusCached    = "Cached"
	StatusError     = "Error"
)

// Result holds the outcome of processing a single file.
type Result struct {
	FilePath string
	Status   string
	Reason   string
	Error    error
}

// Worker holds dependencies needed for processing a file.
type Worker struct {
	Opts          *config.Options
	FS            filesystem.FileSystem
	CacheManager  cache.CacheManager
	Logger        *slog.Logger
	IgnoreMatcher func(string) bool
	TemplateExec  *template.Executor
	ConfigHash    []byte
	TemplateHash  []byte
}

// NewWorker creates a new Worker instance.
func NewWorker(
	opts *config.Options,
	fs filesystem.FileSystem,
	cm cache.CacheManager,
	logger *slog.Logger,
	ignoreMatcher func(string) bool,
	templateExec *template.Executor,
	configHash []byte,
	templateHash []byte,
) *Worker {
	return &Worker{
		Opts:          opts,
		FS:            fs,
		CacheManager:  cm,
		Logger:        logger,
		IgnoreMatcher: ignoreMatcher,
		TemplateExec:  templateExec,
		ConfigHash:    configHash,
		TemplateHash:  templateHash,
	}
}

// ProcessFile handles the conversion logic for a single file path.
func (w *Worker) ProcessFile(ctx context.Context, filePath string) Result {
	// Check for context cancellation frequently
	select {
	case <-ctx.Done():
		return Result{FilePath: filePath, Status: StatusError, Reason: "Processing cancelled", Error: ctx.Err()}
	default:
	}

	// --- Skip Checks ---
	if w.Opts.SkipHiddenFiles && strings.HasPrefix(filepath.Base(filePath), ".") {
		w.Logger.Debug("Skipping hidden file", "file", filePath)
		return Result{FilePath: filePath, Status: StatusSkipped, Reason: "Hidden"}
	}

	if w.IgnoreMatcher != nil && w.IgnoreMatcher(filePath) {
		w.Logger.Debug("Skipping file due to ignore rule", "file", filePath)
		return Result{FilePath: filePath, Status: StatusSkipped, Reason: "Ignored"}
	}

	// --- Get File Info ---
	fileInfo, err := w.FS.Stat(filePath)
	if err != nil {
		wrappedErr := fmt.Errorf("failed to get file info for '%s': %w", filePath, err)
		w.Logger.Error("File processing error", "file", filePath, "error", wrappedErr)
		return Result{FilePath: filePath, Status: StatusError, Reason: "Stat failed", Error: wrappedErr}
	}

	// --- Large File Check ---
	fileSizeMB := float64(fileInfo.Size()) / (1024 * 1024)
	if fileSizeMB > float64(w.Opts.LargeFileThresholdMB) {
		reason := fmt.Sprintf("Large (%.2fMB > %dMB)", fileSizeMB, w.Opts.LargeFileThresholdMB)
		w.Logger.Debug("Handling large file", "file", filePath, "size_mb", fileSizeMB, "threshold_mb", w.Opts.LargeFileThresholdMB, "mode", w.Opts.LargeFileMode)

		// --- FIX: Refactor using switch statement ---
		switch w.Opts.LargeFileMode {
		case "skip":
			return Result{FilePath: filePath, Status: StatusSkipped, Reason: reason}
		case "error":
			wrappedErr := fmt.Errorf("file '%s' exceeds size threshold (%s)", filePath, reason)
			w.Logger.Error("File processing error", "file", filePath, "error", wrappedErr)
			return Result{FilePath: filePath, Status: StatusError, Reason: reason, Error: wrappedErr}
		default: // Defensive check for invalid mode (should be caught by config validation)
			wrappedErr := fmt.Errorf("invalid largeFileMode '%s' configured for file '%s'", w.Opts.LargeFileMode, filePath)
			w.Logger.Error("Configuration error", "file", filePath, "error", wrappedErr)
			// Treat invalid config as an error preventing processing
			return Result{FilePath: filePath, Status: StatusError, Reason: "Invalid largeFileMode", Error: wrappedErr}
		}
		// --- End FIX ---
	}

	// --- Cancellation Check ---
	select {
	case <-ctx.Done():
		return Result{FilePath: filePath, Status: StatusError, Reason: "Processing cancelled", Error: ctx.Err()}
	default:
	}

	// --- Cache Check ---
	var cacheStatus cache.CacheStatus = cache.StatusMiss
	if w.Opts.UseCache && w.CacheManager != nil {
		cacheStatus, err = w.CacheManager.Check(filePath, w.ConfigHash, w.TemplateHash)
		if err != nil {
			w.Logger.Warn("Cache check failed, proceeding as cache miss", "file", filePath, "error", err)
			cacheStatus = cache.StatusMiss
		}
	}

	if cacheStatus == cache.StatusHit {
		w.Logger.Debug("Cache hit", "file", filePath)
		return Result{FilePath: filePath, Status: StatusCached}
	}
	w.Logger.Debug("Cache miss or disabled", "file", filePath, "status", cacheStatus)

	// --- Cancellation Check ---
	select {
	case <-ctx.Done():
		return Result{FilePath: filePath, Status: StatusError, Reason: "Processing cancelled", Error: ctx.Err()}
	default:
	}

	// --- Read & Detect ---
	contentBytes, err := w.FS.ReadFile(filePath)
	if err != nil {
		wrappedErr := fmt.Errorf("failed to read file content for '%s': %w", filePath, err)
		w.Logger.Error("File processing error", "file", filePath, "error", wrappedErr)
		return Result{FilePath: filePath, Status: StatusError, Reason: "Read failed", Error: wrappedErr}
	}

	// --- Binary Check ---
	checkLen := 512
	if len(contentBytes) < checkLen {
		checkLen = len(contentBytes)
	}
	contentType := http.DetectContentType(contentBytes[:checkLen])
	isBinary := !strings.HasPrefix(contentType, "text/")

	var finalContent string // Declare earlier

	if isBinary {
		reason := "Binary"
		w.Logger.Debug("Handling binary file", "file", filePath, "detected_type", contentType, "mode", w.Opts.BinaryMode)
		if w.Opts.BinaryMode == "skip" {
			return Result{FilePath: filePath, Status: StatusSkipped, Reason: reason}
		} else if w.Opts.BinaryMode == "placeholder" {
			// Generate placeholder content and store it in finalContent
			finalContent = fmt.Sprintf("**Note:** File `%s` is a binary file and its content is not displayed.", filePath)
			// Let execution flow continue to the "Write Output" section below
		} else {
			// Defensive check for invalid mode (should be caught by config validation)
			wrappedErr := fmt.Errorf("invalid binaryMode '%s' for file '%s'", w.Opts.BinaryMode, filePath)
			w.Logger.Error("File processing error", "file", filePath, "error", wrappedErr)
			return Result{FilePath: filePath, Status: StatusError, Reason: "Invalid binaryMode", Error: wrappedErr}
		}
	} else { // --- Process Text File ---
		// --- UTF-8 Validation ---
		if !utf8.Valid(contentBytes) {
			wrappedErr := fmt.Errorf("file '%s' contains invalid UTF-8 sequences", filePath)
			w.Logger.Error("File processing error", "file", filePath, "error", wrappedErr)
			return Result{FilePath: filePath, Status: StatusError, Reason: "Invalid UTF-8", Error: wrappedErr}
		}

		// --- Detect Language ---
		detectedLang := DetectLanguageByExtension(filePath, w.Opts.LanguageMappings)
		// Optional: Implement content-based fallback if needed and configured

		// --- Cancellation Check ---
		select {
		case <-ctx.Done():
			return Result{FilePath: filePath, Status: StatusError, Reason: "Processing cancelled", Error: ctx.Err()}
		default:
		}

		// --- Front Matter ---
		frontMatterContent := "" // Keep scope local to text processing
		if w.Opts.FrontMatter.Enabled {
			frontMatterData := make(map[string]interface{})
			for key, value := range w.Opts.FrontMatter.Static {
				frontMatterData[key] = value
			}
			for _, key := range w.Opts.FrontMatter.Include {
				switch key {
				case "FilePath":
					frontMatterData["FilePath"] = filePath
				case "DetectedLanguage":
					frontMatterData["DetectedLanguage"] = detectedLang
				}
			}

			var fmBytes []byte
			var marshalErr error
			var startDelim, endDelim string

			if w.Opts.FrontMatter.Format == "yaml" {
				startDelim, endDelim = "---\n", "---\n"
				fmBytes, marshalErr = yaml.Marshal(frontMatterData)
			} else if w.Opts.FrontMatter.Format == "toml" {
				startDelim, endDelim = "+++\n", "+++\n"
				buf := new(bytes.Buffer)
				enc := toml.NewEncoder(buf)
				marshalErr = enc.Encode(frontMatterData)
				if marshalErr == nil {
					fmBytes = buf.Bytes()
				}
			} else {
				marshalErr = fmt.Errorf("invalid front matter format: %s", w.Opts.FrontMatter.Format)
			}

			if marshalErr != nil {
				wrappedErr := fmt.Errorf("failed to generate front matter for '%s': %w", filePath, marshalErr)
				w.Logger.Error("File processing error", "file", filePath, "error", wrappedErr)
				return Result{FilePath: filePath, Status: StatusError, Reason: "Front matter generation failed", Error: wrappedErr}
			}
			frontMatterContent = startDelim + string(fmBytes) + endDelim
		}

		// --- Cancellation Check ---
		select {
		case <-ctx.Done():
			return Result{FilePath: filePath, Status: StatusError, Reason: "Processing cancelled", Error: ctx.Err()}
		default:
		}

		// --- Generate Markdown Content ---
		if w.TemplateExec != nil { // Custom template
			templateData := map[string]interface{}{
				"FilePath":         filePath,
				"Content":          string(contentBytes),
				"DetectedLanguage": detectedLang,
			}
			renderedContent, err := w.TemplateExec.Execute(templateData)
			if err != nil {
				wrappedErr := fmt.Errorf("failed to execute template '%s' for file '%s': %w", w.Opts.TemplateFile, filePath, err)
				w.Logger.Error("File processing error", "file", filePath, "template", w.Opts.TemplateFile, "error", wrappedErr)
				return Result{FilePath: filePath, Status: StatusError, Reason: "Template execution failed", Error: wrappedErr}
			}
			finalContent = frontMatterContent + renderedContent
		} else { // Default generation
			var mdBuilder strings.Builder
			mdBuilder.WriteString(frontMatterContent) // Add front matter first
			mdBuilder.WriteString("```")
			mdBuilder.WriteString(detectedLang)
			mdBuilder.WriteString("\n")
			mdBuilder.Write(contentBytes)
			mdBuilder.WriteString("\n```\n")
			finalContent = mdBuilder.String()
		}
	} // End "else" block for processing text files

	// --- Write Output --- (This section now executes for both text files and binary placeholders)
	relativeFilePath, err := filepath.Rel(w.Opts.Input, filePath)
	if err != nil {
		wrappedErr := fmt.Errorf("failed to calculate relative path for '%s' from base '%s': %w", filePath, w.Opts.Input, err)
		w.Logger.Error("File processing error", "file", filePath, "input_base", w.Opts.Input, "error", wrappedErr)
		return Result{FilePath: filePath, Status: StatusError, Reason: "Path calculation failed", Error: wrappedErr}
	}
	outputFilePath := filepath.Join(w.Opts.Output, relativeFilePath+".md")

	outputDir := filepath.Dir(outputFilePath)
	if err := w.FS.MkdirAll(outputDir, 0755); err != nil {
		wrappedErr := fmt.Errorf("failed to create output directory '%s' for file '%s': %w", outputDir, outputFilePath, err)
		w.Logger.Error("File processing error", "file", filePath, "output_dir", outputDir, "error", wrappedErr)
		return Result{FilePath: filePath, Status: StatusError, Reason: "Output directory creation failed", Error: wrappedErr}
	}

	// --- Cancellation Check ---
	select {
	case <-ctx.Done():
		return Result{FilePath: filePath, Status: StatusError, Reason: "Processing cancelled", Error: ctx.Err()}
	default:
	}

	if err := w.FS.WriteFile(outputFilePath, []byte(finalContent), 0644); err != nil {
		wrappedErr := fmt.Errorf("failed to write output file '%s': %w", outputFilePath, err)
		w.Logger.Error("File processing error", "file", filePath, "output_file", outputFilePath, "error", wrappedErr)
		return Result{FilePath: filePath, Status: StatusError, Reason: "Output file write failed", Error: wrappedErr}
	}

	// --- Update Cache ---
	// Don't cache binary placeholders (isBinary check remains relevant)
	if w.Opts.UseCache && w.CacheManager != nil && !isBinary {
		entry := cache.CacheEntry{
			ModTime:      fileInfo.ModTime(),
			Size:         fileInfo.Size(),
			ConfigHash:   w.ConfigHash,
			TemplateHash: w.TemplateHash,
			OutputPath:   outputFilePath,
			// SourceHash: // Calculate if needed
		}
		if err := w.CacheManager.Update(filePath, entry); err != nil {
			w.Logger.Warn("Failed to update cache", "file", filePath, "error", err)
			// Don't return error here, just log the warning
		}
	}

	w.Logger.Debug("Successfully processed file", "file", filePath, "output", outputFilePath)
	return Result{FilePath: filePath, Status: StatusProcessed}
}

// DetectLanguageByExtension attempts to detect the language based on file extension.
func DetectLanguageByExtension(filePath string, overrides map[string]string) string {
	ext := filepath.Ext(filePath)
	if ext == "" {
		return ""
	}
	ext = strings.ToLower(ext)

	if lang, ok := overrides[ext]; ok {
		return lang
	}

	// Built-in map (expand as needed)
	switch ext {
	case ".go":
		return "go"
	case ".py":
		return "python"
	case ".js":
		return "javascript"
	case ".ts":
		return "typescript"
	case ".java":
		return "java"
	case ".c":
		return "c"
	case ".cpp", ".cxx", ".h", ".hpp":
		return "cpp"
	case ".cs":
		return "csharp"
	case ".rb":
		return "ruby"
	case ".php":
		return "php"
	case ".swift":
		return "swift"
	case ".kt", ".kts":
		return "kotlin"
	case ".rs":
		return "rust"
	case ".html", ".htm":
		return "html"
	case ".css":
		return "css"
	case ".json":
		return "json"
	case ".yaml", ".yml":
		return "yaml"
	case ".toml":
		return "toml"
	case ".md", ".markdown":
		return "markdown"
	case ".sh", ".bash":
		return "bash"
	case ".ps1":
		return "powershell"
	case ".sql":
		return "sql"
	case ".xml":
		return "xml"
	case ".dockerfile", "dockerfile":
		return "dockerfile"
	case ".tf":
		return "terraform"
	case ".hcl":
		return "hcl"
	case ".lua":
		return "lua"
	case ".pl":
		return "perl"
	case ".scala":
		return "scala"
	default:
		return strings.TrimPrefix(ext, ".")
	}
}
