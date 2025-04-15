package template

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	// Added missing import used in TestExecutor_Execute
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require" // Using require for essential setup checks

	"github.com/stackvity/convi/internal/filesystem" // Import shared filesystem, assuming mock is defined there
)

// --- Test Cases ---

// TestNewExecutor tests the creation of a template executor.
// Covers parsing valid templates, handling non-existent files, and invalid template syntax.
// Relates to TASK-CONVI-070 (initialization part) and TASK-CONVI-081.
func TestNewExecutor(t *testing.T) {
	// Assuming NewMockFileSystem is now available via the filesystem package
	// *NOTE: This requires NewMockFileSystem to be defined and exported in
	//        internal/filesystem/mock_filesystem.go (or similar), NOT internal/filesystem/filesystem_test.go*
	mockFS := filesystem.NewMockFileSystem()
	validTemplatePath := filepath.Join("templates", "valid.tmpl")
	invalidSyntaxPath := filepath.Join("templates", "invalid_syntax.tmpl")
	nonExistentPath := filepath.Join("templates", "nonexistent.tmpl")

	validTemplateContent := `FilePath: {{ .FilePath }} | Content: {{ .Content }}`
	invalidSyntaxContent := `{{ .FilePath` // Missing closing braces

	// Setup mock filesystem data - assumes AddFile is a method on the shared mock
	// AddFile needs to be defined on the filesystem.MockFileSystem for this to work.
	// If the shared mock doesn't have AddFile, we rely purely on .On() expectations.
	// Let's proceed assuming we set expectations directly.

	t.Run("ValidTemplate", func(t *testing.T) {
		// Mock ReadFile for the valid path
		mockFS.On("ReadFile", validTemplatePath).Return([]byte(validTemplateContent), nil).Once()

		executor, err := NewExecutor(validTemplatePath, mockFS)

		require.NoError(t, err) // Use require for essential setup check
		require.NotNil(t, executor)
		assert.NotNil(t, executor.template)
		assert.Equal(t, validTemplatePath, executor.filePath) // Check correct path stored
		mockFS.AssertExpectations(t)                          // Verify ReadFile was called
	})

	t.Run("NonExistentTemplateFile", func(t *testing.T) {
		// Mock ReadFile for the non-existent path to return ErrNotExist
		mockFS.On("ReadFile", nonExistentPath).Return(nil, os.ErrNotExist).Once()

		executor, err := NewExecutor(nonExistentPath, mockFS)

		assert.Error(t, err)
		assert.Nil(t, executor)
		assert.ErrorIs(t, err, os.ErrNotExist) // Check for wrapped error
		mockFS.AssertExpectations(t)           // Verify ReadFile was called
	})

	t.Run("TemplateReadError", func(t *testing.T) {
		readErrPath := filepath.Join("templates", "read_error.tmpl")
		readErr := errors.New("permission denied")

		// Mock ReadFile to return a generic read error
		mockFS.On("ReadFile", readErrPath).Return(nil, readErr).Once()

		executor, err := NewExecutor(readErrPath, mockFS)

		assert.Error(t, err)
		assert.Nil(t, executor)
		assert.ErrorIs(t, err, readErr) // Check for wrapped generic error
		mockFS.AssertExpectations(t)
	})

	t.Run("InvalidTemplateSyntax", func(t *testing.T) {
		// Mock ReadFile for the invalid syntax path
		mockFS.On("ReadFile", invalidSyntaxPath).Return([]byte(invalidSyntaxContent), nil).Once()

		executor, err := NewExecutor(invalidSyntaxPath, mockFS)

		assert.Error(t, err) // Expect a parse error
		assert.Nil(t, executor)
		// Check if the error message contains expected text for parse errors
		// A more robust check might involve text/template specific error types if possible
		assert.Contains(t, err.Error(), "template:") // text/template errors usually start like this
		// assert.Contains(t, err.Error(), "parsing error") // This check is fragile, relying on specific error text
		mockFS.AssertExpectations(t) // Verify ReadFile was called
	})

	t.Run("EmptyTemplatePath", func(t *testing.T) {
		// Calling with empty path should return nil, nil without calling ReadFile
		executor, err := NewExecutor("", mockFS)

		assert.NoError(t, err)
		assert.Nil(t, executor)
		mockFS.AssertNotCalled(t, "ReadFile", "") // Ensure ReadFile wasn't called
	})
}

// TestExecutor_Execute tests the execution of a parsed template.
// Covers successful execution and handling of template execution errors.
// Relates to TASK-CONVI-070 (execution part) and TASK-CONVI-081.
func TestExecutor_Execute(t *testing.T) {
	// *NOTE: This requires NewMockFileSystem to be defined and exported in
	//        internal/filesystem/mock_filesystem.go (or similar), NOT internal/filesystem/filesystem_test.go*
	mockFS := filesystem.NewMockFileSystem() // Fresh mock for this test

	t.Run("SuccessfulExecution", func(t *testing.T) {
		templatePath := "success.tmpl"
		templateContent := `Path: {{ .FilePath }} | Lang: {{ .DetectedLanguage }} | Data: {{ .Content }}`
		mockFS.On("ReadFile", templatePath).Return([]byte(templateContent), nil).Once()

		executor, err := NewExecutor(templatePath, mockFS)
		require.NoError(t, err)
		require.NotNil(t, executor)

		data := map[string]interface{}{
			"FilePath":         "/path/to/file.go",
			"Content":          "package main",
			"DetectedLanguage": "go",
		}

		rendered, err := executor.Execute(data)

		assert.NoError(t, err)
		expectedOutput := "Path: /path/to/file.go | Lang: go | Data: package main"
		assert.Equal(t, expectedOutput, rendered)
		mockFS.AssertExpectations(t)
	})

	t.Run("ExecutionError_MissingKey", func(t *testing.T) {
		templatePath := "missing_key.tmpl"
		// Template refers to 'MissingKey' which is not in the data map
		templateContent := `Path: {{ .FilePath }} | Missing: {{ .MissingKey }}`
		mockFS.On("ReadFile", templatePath).Return([]byte(templateContent), nil).Once()

		executor, err := NewExecutor(templatePath, mockFS)
		require.NoError(t, err)
		require.NotNil(t, executor)

		data := map[string]interface{}{
			"FilePath": "/path/to/another.txt",
			"Content":  "some data",
			// Missing "MissingKey" intentionally
		}

		rendered, err := executor.Execute(data)

		assert.Error(t, err) // Expect an execution error
		assert.Empty(t, rendered)
		// Check if the error message indicates the missing key problem
		assert.Contains(t, err.Error(), `map has no entry for key "MissingKey"`)
		mockFS.AssertExpectations(t)
	})

	t.Run("ExecutionError_FunctionCallError", func(t *testing.T) {
		// Example with a function that might error (using built-in 'printf' inappropriately)
		templatePath := "func_error.tmpl"
		templateContent := `Result: {{ printf "%d" "not-a-number" }}` // Will cause runtime error
		mockFS.On("ReadFile", templatePath).Return([]byte(templateContent), nil).Once()

		executor, err := NewExecutor(templatePath, mockFS)
		require.NoError(t, err)
		require.NotNil(t, executor)

		data := map[string]interface{}{} // Data context doesn't matter here

		rendered, err := executor.Execute(data)

		assert.Error(t, err) // Expect an execution error
		assert.Empty(t, rendered)
		// Check if the error message indicates the function call problem
		assert.Contains(t, err.Error(), "error calling printf")
		mockFS.AssertExpectations(t)
	})

}
