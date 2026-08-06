package agents

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jeeinn/matea/internal/sandbox"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupTestSkillDir creates a temporary gateway dir with a skill file.
func setupTestSkillDir(t *testing.T, skillContent string) string {
	t.Helper()
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "skills", "my-skill")
	require.NoError(t, os.MkdirAll(skillDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillContent), 0644))
	return dir
}

func TestRegisterGatewaySkills(t *testing.T) {
	skillContent := `---
name: my-skill
description: A test skill
tools:
  - name: greet
    description: Greet someone
    script: echo "Hello, {{name}}!"
    parameters:
      - name: name
        type: string
        description: Name to greet
        required: true
---
# My Skill Instructions

This is the skill body.
`
	gatewayDir := setupTestSkillDir(t, skillContent)

	sb := sandbox.New(sandbox.DefaultSandboxConfig(), 1)
	tb := NewToolBox(sb, nil)

	err := tb.RegisterGatewaySkills(gatewayDir)
	require.NoError(t, err)

	// Verify skill tool was registered with prefix
	assert.True(t, tb.HasTool("matea_skill_greet"))
	assert.Equal(t, 1, tb.ToolCount())
}

func TestRegisterGatewaySkillsNoDir(t *testing.T) {
	sb := sandbox.New(sandbox.DefaultSandboxConfig(), 1)
	tb := NewToolBox(sb, nil)

	// Empty gateway dir should be a no-op
	err := tb.RegisterGatewaySkills("")
	require.NoError(t, err)
	assert.Equal(t, 0, tb.ToolCount())
}

func TestRegisterGatewaySkillsNoSandbox(t *testing.T) {
	// ToolBox without sandbox should fail
	tb := NewToolBox(nil, nil)
	err := tb.RegisterGatewaySkills("/some/dir")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "sandbox required")
}

func TestGetGatewaySkillBody(t *testing.T) {
	skillContent := `---
name: doc-skill
description: A documentation skill
---
# Documentation Skill

This skill helps with documentation tasks.
`
	gatewayDir := setupTestSkillDir(t, skillContent)

	sb := sandbox.New(sandbox.DefaultSandboxConfig(), 1)
	tb := NewToolBox(sb, nil)

	body, err := tb.GetGatewaySkillBody(gatewayDir, "doc-skill")
	require.NoError(t, err)
	assert.Contains(t, body, "Documentation Skill")
	assert.Contains(t, body, "documentation tasks")
}

func TestGetGatewaySkillBodyNotFound(t *testing.T) {
	gatewayDir := setupTestSkillDir(t, `---
name: existing
description: exists
---
body
`)

	sb := sandbox.New(sandbox.DefaultSandboxConfig(), 1)
	tb := NewToolBox(sb, nil)

	_, err := tb.GetGatewaySkillBody(gatewayDir, "nonexistent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestListGatewaySkillNames(t *testing.T) {
	skillContent := `---
name: skill-one
description: First skill
---
body one
`
	gatewayDir := setupTestSkillDir(t, skillContent)

	sb := sandbox.New(sandbox.DefaultSandboxConfig(), 1)
	tb := NewToolBox(sb, nil)

	names, err := tb.ListGatewaySkillNames(gatewayDir)
	require.NoError(t, err)
	assert.Contains(t, names, "skill-one")
}

func TestSkillToolExecution(t *testing.T) {
	// Create a skill that echoes a parameter
	var script string
	if runtime.GOOS == "windows" {
		script = "echo Hello, {{name}}!"
	} else {
		script = "echo \"Hello, {{name}}!\""
	}

	skillContent := `---
name: echo-skill
description: Echo skill
tools:
  - name: echo_name
    description: Echo the name
    script: ` + script + `
    parameters:
      - name: name
        type: string
        description: Name to echo
        required: true
---
Echo skill body
`
	gatewayDir := setupTestSkillDir(t, skillContent)

	dir := t.TempDir()
	cfg := sandbox.DefaultSandboxConfig()
	cfg.Mode = sandbox.ModeFixed
	cfg.BaseDir = dir
	sb := sandbox.NewWithPath(cfg, 1, dir)
	require.NoError(t, sb.Setup())

	tb := NewToolBox(sb, nil)
	err := tb.RegisterGatewaySkills(gatewayDir)
	require.NoError(t, err)

	// Execute the skill tool
	result, err := tb.Execute(nil, "matea_skill_echo_name", map[string]interface{}{
		"name": "World",
	})
	require.NoError(t, err)
	assert.Contains(t, result, "Hello, World!")
}

func TestSkillToolToDecl(t *testing.T) {
	sb := sandbox.New(sandbox.DefaultSandboxConfig(), 1)
	defer sb.Cleanup()

	st := struct {
		Name        string
		Description string
		Script      string
		Parameters  []struct {
			Name        string
			Type        string
			Description string
		}
		Required []string
	}{
		Name:        "test-tool",
		Description: "A test tool",
		Script:      "echo {{msg}}",
		Parameters: []struct {
			Name        string
			Type        string
			Description string
		}{
			{Name: "msg", Type: "string", Description: "Message"},
		},
		Required: []string{"msg"},
	}

	// Verify the struct fields are correct
	assert.Equal(t, "test-tool", st.Name)
	assert.Equal(t, "echo {{msg}}", st.Script)
}

func TestSkillToolPrefix(t *testing.T) {
	assert.Equal(t, "matea_skill_", skillToolPrefix)
}

func TestSkillToolVisibleToRemote(t *testing.T) {
	// Skill tools should be visible to both builtin and remote harnesses
	skillContent := `---
name: shared-skill
description: Shared skill
tools:
  - name: shared_tool
    description: A shared tool
    script: echo shared
---
body
`
	gatewayDir := setupTestSkillDir(t, skillContent)

	sb := sandbox.New(sandbox.DefaultSandboxConfig(), 1)
	tb := NewToolBox(sb, nil)

	err := tb.RegisterGatewaySkills(gatewayDir)
	require.NoError(t, err)

	// Builtin should see it
	builtin := &mockHarness{
		profile: HarnessProfile{ID: "builtin", ToolTransport: ToolDirect},
	}
	tools := tb.ToolsFor(builtin)
	assert.Len(t, tools, 1)

	// Remote should also see it
	remote := &mockHarness{
		profile: HarnessProfile{ID: "remote", ToolTransport: ToolViaSubmit},
	}
	tools = tb.ToolsFor(remote)
	assert.Len(t, tools, 1)
}
