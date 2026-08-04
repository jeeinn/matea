package workflow

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestHasSlashCommand verifies slash command detection with proper anchoring and code block exclusion.
func TestHasSlashCommand(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		command  string
		expected bool
	}{
		// Valid cases - command at line start
		{
			name:     "CommandAtStart",
			body:     "/dev implement feature",
			command:  "dev",
			expected: true,
		},
		{
			name:     "CommandAtStartMultiline",
			body:     "Some text\n/dev implement\nMore text",
			command:  "dev",
			expected: true,
		},
		{
			name:     "ForceCommand",
			body:     "/force\n@agent please continue",
			command:  "force",
			expected: true,
		},
		{
			name:     "ReplyCommand",
			body:     "/reply to this comment",
			command:  "reply",
			expected: true,
		},

		// Invalid cases - command in middle of line
		{
			name:     "CommandInMiddle",
			body:     "Check out /dev branch",
			command:  "dev",
			expected: false,
		},
		{
			name:     "CommandInURL",
			body:     "Visit https://example.com/dev/api",
			command:  "dev",
			expected: false,
		},
		{
			name:     "CommandInPath",
			body:     "The file is in /development/src/",
			command:  "dev",
			expected: false,
		},

		// Code block exclusion
		{
			name:     "CommandInCodeBlock",
			body:     "```\n/dev this is code\n```",
			command:  "dev",
			expected: false,
		},
		{
			name:     "CommandInInlineCode",
			body:     "Use `git commit -m \"/dev feature\"`",
			command:  "dev",
			expected: false,
		},
		{
			name:     "CommandInTripleBacktick",
			body:     "```bash\n/force reset\n```",
			command:  "force",
			expected: false,
		},
		{
			name:     "MixedValidAndCodeBlock",
			body:     "/dev real command\n```\n/reply fake command\n```",
			command:  "dev",
			expected: true,
		},
		{
			name:     "MixedValidAndCodeBlock_Reply",
			body:     "/dev real command\n```\n/reply fake command\n```",
			command:  "reply",
			expected: false,
		},

		// Edge cases
		{
			name:     "EmptyBody",
			body:     "",
			command:  "dev",
			expected: false,
		},
		{
			name:     "OnlyWhitespace",
			body:     "   \n\t\n",
			command:  "dev",
			expected: false,
		},
		{
			name:     "CommandWithLeadingSpace",
			body:     " /dev not at start",
			command:  "dev",
			expected: false,
		},
		{
			name:     "MultipleCommands",
			body:     "/dev first\n/reply second",
			command:  "dev",
			expected: true,
		},
		{
			name:     "MultipleCommands_Reply",
			body:     "/dev first\n/reply second",
			command:  "reply",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := hasSlashCommand(tt.body, tt.command)
			assert.Equal(t, tt.expected, result, "hasSlashCommand(%q, %q)", tt.body, tt.command)
		})
	}
}

// TestStripCodeBlocks verifies code block removal.
func TestStripCodeBlocks(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "NoCodeBlocks",
			input:    "Plain text with /dev command",
			expected: "Plain text with /dev command",
		},
		{
			name:     "TripleBacktick",
			input:    "Text\n```\ncode /dev here\n```\nMore text",
			expected: "Text\n\nMore text",
		},
		{
			name:     "InlineCode",
			input:    "Use `git commit -m \"/dev\"`",
			expected: "Use ",
		},
		{
			name:     "MixedCodeBlocks",
			input:    "Text `inline` and\n```\nblock\n```\nend",
			expected: "Text  and\n\nend",
		},
		{
			name:     "NestedBackticks",
			input:    "```\ncode with `nested`\n```",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := stripCodeBlocks(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestHasSlashCommand_RealWorldExamples tests with realistic comment bodies.
func TestHasSlashCommand_RealWorldExamples(t *testing.T) {
	t.Run("ValidDevCommand", func(t *testing.T) {
		body := `@matea-coder please help with this
/dev implement the authentication feature`
		assert.True(t, hasSlashCommand(body, "dev"))
		assert.False(t, hasSlashCommand(body, "reply"))
	})

	t.Run("URLNotDetected", func(t *testing.T) {
		body := `Check the deployment at https://staging.example.com/dev/api
@matea-analyst what do you think?`
		assert.False(t, hasSlashCommand(body, "dev"))
	})

	t.Run("CodeSnippetNotDetected", func(t *testing.T) {
		body := `Here's the command to run:
` + "```bash\n/dev/scripts/deploy.sh\n```" + `
@matea-coder can you review?`
		assert.False(t, hasSlashCommand(body, "dev"))
	})

	t.Run("ForceWithMention", func(t *testing.T) {
		body := `/force
@matea-coder please continue despite warnings`
		assert.True(t, hasSlashCommand(body, "force"))
		assert.False(t, hasSlashCommand(body, "dev"))
	})

	t.Run("DevelopmentNotDetected", func(t *testing.T) {
		body := `The /development branch needs review
@matea-review please check`
		assert.False(t, hasSlashCommand(body, "dev"))
	})
}
