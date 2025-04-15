package template

import (
	"bytes"
	"fmt"
	"text/template" // Using text/template as per proposal.md

	"github.com/stackvity/convi/internal/filesystem" // Depends on filesystem abstraction
)

// Executor handles parsing and executing custom Go templates.
// This is used when a user specifies a template file via configuration.
// Aligns with Capability CO.8.1: Apply Custom Go Template.
type Executor struct {
	template *template.Template
	fs       filesystem.FileSystem // Filesystem abstraction for reading templates
	filePath string                // Path to the template file for error reporting and cache invalidation context
}

// NewExecutor creates a new template executor by parsing the specified template file.
// Returns nil, nil if templateFilePath is empty, allowing the caller to handle default logic.
// Returns an error if the file cannot be read or parsed.
// Corresponds to the initialization part of TASK-CONVI-070.
func NewExecutor(templateFilePath string, fs filesystem.FileSystem) (*Executor, error) {
	// If no template path is provided, it indicates default logic should be used by the caller.
	if templateFilePath == "" {
		return nil, nil // No custom executor needed, not an error state.
	}

	// Read the template file content using the filesystem abstraction
	templateContent, err := fs.ReadFile(templateFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read template file '%s': %w", templateFilePath, err)
	}

	// Parse the template
	// Using the template file path as the name for better error messages.
	tmpl, err := template.New(templateFilePath).Parse(string(templateContent))
	if err != nil {
		return nil, fmt.Errorf("failed to parse template file '%s': %w", templateFilePath, err)
	}

	return &Executor{
		template: tmpl,
		fs:       fs,
		filePath: templateFilePath,
	}, nil
}

// Execute applies the parsed custom template to the provided data context.
// The data map should contain keys expected by the user's template (e.g., FilePath, Content, DetectedLanguage).
// Corresponds to the execution part of TASK-CONVI-070 and Capability CO.8.1.
func (e *Executor) Execute(data map[string]interface{}) (string, error) {
	var rendered bytes.Buffer
	// Execute the specific template associated with this executor.
	err := e.template.Execute(&rendered, data)
	if err != nil {
		// Provide context about which template failed during execution.
		return "", fmt.Errorf("failed to execute template '%s': %w", e.filePath, err)
	}
	return rendered.String(), nil
}

// Note: DefaultTemplateContent constant and ExecuteDefaultTemplate function have been removed
// as per recommendation B.1. The calling code (e.g., in internal/worker) is now responsible
// for checking if NewExecutor returned nil and applying default generation logic directly
// if no custom template executor was created.
