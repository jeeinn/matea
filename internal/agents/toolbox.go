package agents

import (
	"context"
	"fmt"
	"strings"
	"sync"

	agentpkg "github.com/jeeinn/matea/internal/agent"
	"github.com/jeeinn/matea/internal/sandbox"
)

// ToolBox is the unified tool exposure layer between Matea and harnesses (D11).
// It implements the three-layer exposure strategy:
//
//  1. Sandbox tools (read_file, write_file, ...): builtin gets direct Go calls;
//     remote harnesses do NOT receive these (they have their own native tools
//     and direct workspace access is faster — routing through ToolBox adds a
//     round-trip for zero benefit, and duplicate names cause model confusion).
//
//  2. Gitea tools (gitea_read_issue, gitea_read_pr_diff): remote harnesses may
//     ONLY access Gitea through these. Phase 2 is read-only; write-side tools
//     (gitea_create_comment, gitea_create_pr) are Phase 3.
//
//  3. Skill tools (gateway-level): exposed for remote harnesses that cannot
//     see Matea's filesystem; workspace-level skills travel with the workspace.
//
// Invariant (D11, frozen): ToolBox can only APPEND to a成品 harness's tool set,
// never REPLACE.成品 harnesses (OpenCode/Hermes) have built-in tools that cannot
// be disabled; adding tools with conflicting names causes model indecision.

// ToolCategory classifies tools by their exposure policy.
type ToolCategory string

const (
	// ToolCatSandbox = file/command tools bound to *sandbox.Sandbox.
	// Exposure: builtin ONLY (default); remote never (D11 constraint 2).
	ToolCatSandbox ToolCategory = "sandbox"

	// ToolCatGitea = read-only Gitea query tools.
	// Exposure: builtin AND remote (D11: remote harness's ONLY Gitea channel).
	// Phase 2 scope: read-only. Write-side tools deferred to Phase 3.
	ToolCatGitea ToolCategory = "gitea"

	// ToolCatSkill = gateway-level skill script tools.
	// Exposure: builtin AND remote (D11: Matea's ops assets, remote FS can't see).
	ToolCatSkill ToolCategory = "skill"
)

// ToolExposure declares who may see a tool category.
type ToolExposure string

const (
	// ExposureBuiltinOnly = only the in-process builtin harness.
	ExposureBuiltinOnly ToolExposure = "builtin_only"

	// ExposureAll = both builtin and remote harnesses.
	ExposureAll ToolExposure = "all"
)

// ToolDecl describes one tool exposed through ToolBox. It is a serializable
// description — the actual implementation is either a Go function (builtin) or
// an MCP tool call (remote, Phase 3).
type ToolDecl struct {
	// Name is the tool name visible to the LLM.
	Name string `json:"name"`

	// Description is the tool description for the LLM.
	Description string `json:"description"`

	// Category classifies the tool for exposure policy.
	Category ToolCategory `json:"category"`

	// Exposure declares who may see this tool.
	Exposure ToolExposure `json:"exposure"`

	// Parameters is the JSON Schema for the tool's input.
	Parameters map[string]interface{} `json:"parameters,omitempty"`

	// BuiltinFn is the Go implementation (builtin harness only).
	// nil for tools that only have an MCP/remote implementation.
	BuiltinFn ToolImplFn `json:"-"`
}

// ToolImplFn is the signature for builtin (in-process) tool implementations.
type ToolImplFn func(ctx context.Context, params map[string]interface{}) (string, error)

// ToolBox holds the policy-driven tool catalog and decides which tools are
// visible to a given harness.
type ToolBox struct {
	mu    sync.RWMutex
	tools map[string]ToolDecl

	// sandbox is used by sandbox-category tools (builtin only).
	sandbox *sandbox.Sandbox

	// giteaReadOnly provides Gitea read access for gitea-category tools.
	giteaReadOnly GiteaReadOnlyAccessor
}

// GiteaReadOnlyAccessor is the minimal read-only Gitea interface that
// ToolBox needs. Implemented by a wrapper around gitea.Client so ToolBox
// does not pull in the full client dependency.
type GiteaReadOnlyAccessor interface {
	// GetIssue returns the issue title and body for a repo issue.
	GetIssue(ctx context.Context, repo string, issueID int) (title, body string, err error)

	// GetPRDiff returns the unified diff for a PR.
	GetPRDiff(ctx context.Context, repo string, prID int) (diff string, err error)
}

// NewToolBox creates an empty ToolBox. Use Register to populate.
func NewToolBox(sb *sandbox.Sandbox, gitea GiteaReadOnlyAccessor) *ToolBox {
	return &ToolBox{
		tools:          make(map[string]ToolDecl),
		sandbox:        sb,
		giteaReadOnly:  gitea,
	}
}

// Register adds a tool declaration to the box. Re-registration overwrites.
func (tb *ToolBox) Register(td ToolDecl) {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	tb.tools[td.Name] = td
}

// ToolsFor returns the tool declarations visible to a harness, filtered by
// the harness's transport (in-process vs out-of-process).
//
// Policy (D11):
//   - In-process (builtin): ALL categories (sandbox + gitea + skill).
//   - Out-of-process (remote): gitea + skill ONLY (sandbox tools excluded).
func (tb *ToolBox) ToolsFor(h Harness) []ToolDecl {
	tb.mu.RLock()
	defer tb.mu.RUnlock()

	isRemote := h.Profile().ToolTransport != ToolDirect

	result := make([]ToolDecl, 0, len(tb.tools))
	for _, td := range tb.tools {
		switch td.Exposure {
		case ExposureBuiltinOnly:
			if isRemote {
				continue
			}
		case ExposureAll:
			// visible to all
		}
		result = append(result, td)
	}
	return result
}

// Execute runs a tool by name (builtin harness only). Remote harnesses do NOT
// call this — they return actions and Matea executes them, or they use their
// own native tools.
func (tb *ToolBox) Execute(ctx context.Context, toolName string, params map[string]interface{}) (string, error) {
	tb.mu.RLock()
	td, ok := tb.tools[toolName]
	tb.mu.RUnlock()

	if !ok {
		return "", fmt.Errorf("tool %q not found in ToolBox", toolName)
	}

	// Sandbox-category tools require a sandbox (only available to builtin).
	if td.Category == ToolCatSandbox {
		if tb.sandbox == nil {
			return "", fmt.Errorf("tool %q requires sandbox, none configured", toolName)
		}
		if td.BuiltinFn == nil {
			return "", fmt.Errorf("tool %q has no builtin implementation", toolName)
		}
		return td.BuiltinFn(ctx, params)
	}

	// Gitea/skill tools: route to their implementation.
	if td.BuiltinFn == nil {
		return "", fmt.Errorf("tool %q has no implementation", toolName)
	}
	return td.BuiltinFn(ctx, params)
}

// HasTool reports whether a tool is registered (regardless of exposure).
func (tb *ToolBox) HasTool(name string) bool {
	tb.mu.RLock()
	defer tb.mu.RUnlock()
	_, ok := tb.tools[name]
	return ok
}

// ToolCount returns the total number of registered tools.
func (tb *ToolBox) ToolCount() int {
	tb.mu.RLock()
	defer tb.mu.RUnlock()
	return len(tb.tools)
}

// ----------------------------------------------------------------------------
// Built-in Gitea read-only tool constructors (Phase 2, D11)
// ----------------------------------------------------------------------------

// NewGiteaReadIssueTool creates the gitea_read_issue tool declaration.
func NewGiteaReadIssueTool(gitea GiteaReadOnlyAccessor) ToolDecl {
	return ToolDecl{
		Name:        "gitea_read_issue",
		Description: "Read the title and body of a Gitea issue by number. Returns the issue content as formatted text.",
		Category:    ToolCatGitea,
		Exposure:    ExposureAll, // builtin AND remote
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"repo": map[string]interface{}{
					"type":        "string",
					"description": "The repository in owner/repo format.",
				},
				"issue_id": map[string]interface{}{
					"type":        "number",
					"description": "The issue number to read.",
				},
			},
			"required": []string{"repo", "issue_id"},
		},
		BuiltinFn: func(ctx context.Context, params map[string]interface{}) (string, error) {
			repo, _ := params["repo"].(string)
			issueID := 0
			if v, ok := params["issue_id"].(float64); ok {
				issueID = int(v)
			}
			if repo == "" || issueID == 0 {
				return "", fmt.Errorf("repo and issue_id are required")
			}
			title, body, err := gitea.GetIssue(ctx, repo, issueID)
			if err != nil {
				return "", fmt.Errorf("get issue: %w", err)
			}
			return fmt.Sprintf("Issue #%d: %s\n\n%s", issueID, title, body), nil
		},
	}
}

// NewGiteaReadPRDiffTool creates the gitea_read_pr_diff tool declaration.
func NewGiteaReadPRDiffTool(gitea GiteaReadOnlyAccessor) ToolDecl {
	return ToolDecl{
		Name:        "gitea_read_pr_diff",
		Description: "Read the unified diff of a pull request by number. Returns the full diff text.",
		Category:    ToolCatGitea,
		Exposure:    ExposureAll, // builtin AND remote
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"repo": map[string]interface{}{
					"type":        "string",
					"description": "The repository in owner/repo format.",
				},
				"pr_id": map[string]interface{}{
					"type":        "number",
					"description": "The PR number to read.",
				},
			},
			"required": []string{"repo", "pr_id"},
		},
		BuiltinFn: func(ctx context.Context, params map[string]interface{}) (string, error) {
			repo, _ := params["repo"].(string)
			prID := 0
			if v, ok := params["pr_id"].(float64); ok {
				prID = int(v)
			}
			if repo == "" || prID == 0 {
				return "", fmt.Errorf("repo and pr_id are required")
			}
			diff, err := gitea.GetPRDiff(ctx, repo, prID)
			if err != nil {
				return "", fmt.Errorf("get PR diff: %w", err)
			}
			return diff, nil
		},
	}
}

// ----------------------------------------------------------------------------
// Gateway-level skill integration (D11, task 2.0.4)
// ----------------------------------------------------------------------------

// skillToolPrefix prefixes exported gateway-level skill tools so they don't
// collide with the harness's built-in tools.
const skillToolPrefix = "matea_skill_"

// RegisterGatewaySkills scans the gateway directory for skills and registers
// their script tools into ToolBox. Only gateway-level skills are registered
// here; workspace-level skills travel with the workspace and the harness
// reads them directly (OpenCode also reads AGENTS.md).
//
// This implements D11's "gateway-level skill → ToolBox" path:
//   - skill body (正文) is injected as system prompt by the caller (any harness)
//   - skill script tools are registered here, executed in Matea's sandbox
//
// Security note: skill scripts are arbitrary shell. Phase 3 MCP exposure
// requires sandbox-dir execution + API Key + single-workflow scope.
func (tb *ToolBox) RegisterGatewaySkills(gatewayDir string) error {
	if gatewayDir == "" {
		return nil
	}
	if tb.sandbox == nil {
		return fmt.Errorf("ToolBox: sandbox required for skill registration")
	}

	reg := agentpkg.NewSkillRegistry(tb.sandbox, gatewayDir)
	if err := reg.ScanSkills(); err != nil {
		return fmt.Errorf("scan gateway skills: %w", err)
	}

	// Register each skill's script tools into ToolBox.
	for _, skill := range reg.ListSkills() {
		for _, st := range skill.Tools {
			if strings.TrimSpace(st.Script) == "" {
				continue
			}
			decl := skillToolToDecl(st, tb.sandbox, skill.Name)
			tb.Register(decl)
		}
	}
	return nil
}

// GetGatewaySkillBody returns the body (instructions) of a gateway-level skill
// by name. The caller injects this as a system prompt snippet — any harness
// can consume plain-text instructions, zero cost (D11).
func (tb *ToolBox) GetGatewaySkillBody(gatewayDir, skillName string) (string, error) {
	if gatewayDir == "" {
		return "", fmt.Errorf("no gateway dir")
	}

	reg := agentpkg.NewSkillRegistry(tb.sandbox, gatewayDir)
	if err := reg.ScanSkills(); err != nil {
		return "", fmt.Errorf("scan skills: %w", err)
	}

	skill, ok := reg.GetSkill(skillName)
	if !ok {
		return "", fmt.Errorf("skill %q not found", skillName)
	}
	return skill.Body, nil
}

// ListGatewaySkillNames returns all gateway-level skill names.
func (tb *ToolBox) ListGatewaySkillNames(gatewayDir string) ([]string, error) {
	if gatewayDir == "" {
		return nil, nil
	}

	reg := agentpkg.NewSkillRegistry(tb.sandbox, gatewayDir)
	if err := reg.ScanSkills(); err != nil {
		return nil, fmt.Errorf("scan skills: %w", err)
	}

	names := make([]string, 0)
	for _, s := range reg.ListSkills() {
		names = append(names, s.Name)
	}
	return names, nil
}

// skillToolToDecl converts a SkillTool to a ToolDecl with proper prefix and
// sandbox-bound implementation.
func skillToolToDecl(st agentpkg.SkillTool, sb *sandbox.Sandbox, skillName string) ToolDecl {
	// Build parameters schema
	props := make(map[string]interface{})
	for _, p := range st.Parameters {
		props[p.Name] = map[string]interface{}{
			"type":        p.Type,
			"description": p.Description,
		}
	}

	// Capture script in closure
	script := st.Script
	params := st.Parameters
	required := st.Required

	return ToolDecl{
		Name:        skillToolPrefix + st.Name,
		Description: fmt.Sprintf("[Skill: %s] %s", skillName, st.Description),
		Category:    ToolCatSkill,
		Exposure:    ExposureAll, // builtin AND remote
		Parameters: map[string]interface{}{
			"type":       "object",
			"properties": props,
			"required":   required,
		},
		BuiltinFn: func(ctx context.Context, callParams map[string]interface{}) (string, error) {
			// Substitute {{param}} placeholders in script
			finalScript := script
			for _, p := range params {
				val := ""
				if v, ok := callParams[p.Name]; ok {
					val = fmt.Sprintf("%v", v)
				}
				finalScript = strings.ReplaceAll(finalScript, "{{"+p.Name+"}}", val)
			}

			result := sb.ExecuteShell(finalScript)
			output := result.Stdout
			if result.Stderr != "" {
				output += "\n" + result.Stderr
			}
			if result.Error != nil {
				output += fmt.Sprintf("\nError: %v", result.Error)
			}
			output += fmt.Sprintf("\nExit code: %d", result.ExitCode)
			return output, nil
		},
	}
}
