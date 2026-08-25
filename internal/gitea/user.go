package gitea

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// CurrentUser represents the authenticated Gitea user.
type CurrentUser struct {
	ID       int    `json:"id"`
	Login    string `json:"login"`
	IsAdmin  bool   `json:"is_admin"`
	FullName string `json:"full_name"`
}

// GetCurrentUser returns the user associated with the API token.
func (c *Client) GetCurrentUser() (*CurrentUser, error) {
	body, err := c.do("GET", "/user", nil)
	if err != nil {
		return nil, fmt.Errorf("get current user: %w", err)
	}

	var user CurrentUser
	if err := json.Unmarshal(body, &user); err != nil {
		return nil, fmt.Errorf("unmarshal user: %w", err)
	}
	return &user, nil
}

// TokenScope documents one Gitea token scope Matea relies on. Gitea ≥1.22
// fine-grained scopes are per-category independent: write:admin does NOT
// include read:user. write:X implies read:X within the same category, and
// "all" covers everything.
type TokenScope struct {
	Scope    string `json:"scope"`
	Required bool   `json:"required"`
	Purpose  string `json:"purpose"`
}

// RequiredTokenScopes is the canonical checklist for the admin token, shown
// in the setup wizard / system config UI and mirrored in the docs (README,
// docs/DEPLOYMENT.md) — keep all three in sync.
var RequiredTokenScopes = []TokenScope{
	{Scope: "read:user", Required: true, Purpose: "验证 Token 身份、查询用户"},
	{Scope: "write:repository", Required: true, Purpose: "仓库/分支/PR/部署密钥（含读权限）"},
	{Scope: "write:issue", Required: true, Purpose: "Issue 读取、评论与标签（含读权限）"},
	{Scope: "write:admin", Required: false, Purpose: "自动创建 Agent 账号、站点级 Webhook（需站点管理员账号；缺失则降级手动管理）"},
}

// PermissionCheck is one capability probe of a connection test.
type PermissionCheck struct {
	Key      string `json:"key"`   // identity | repo | issue | admin
	Label    string `json:"label"` // human-readable capability name
	Scope    string `json:"scope"` // scope the user should grant for this capability
	OK       bool   `json:"ok"`
	Required bool   `json:"required"`          // false = degraded mode available without it
	Skipped  bool   `json:"skipped,omitempty"` // probe could not run (e.g. no visible repo)
	Detail   string `json:"detail,omitempty"`
}

// ConnectionTestResult summarizes a Gitea connectivity check.
type ConnectionTestResult struct {
	OK             bool              `json:"ok"`
	Username       string            `json:"username,omitempty"`
	IsAdmin        bool              `json:"is_admin,omitempty"`
	RepoCount      int               `json:"repo_count,omitempty"`
	Message        string            `json:"message"`
	Checks         []PermissionCheck `json:"checks,omitempty"`
	RequiredScopes []TokenScope      `json:"required_scopes,omitempty"`
}

// Gitea ≥1.22 scope-denied errors look like:
//
//	token does not have at least one of required scope(s), required=[read:user], token scope=write:admin,write:repository
//
// The granted-scope list makes the guidance precise instead of guessy.
var (
	scopeRequiredRe = regexp.MustCompile(`required=\[([^\]]+)\]`)
	scopeGrantedRe  = regexp.MustCompile(`token scope=([\w:, -]+)`)
)

// ParseScopeError extracts the required and granted scope lists from a Gitea
// 403 scope error message. ok=false when the message is not a scope error.
func ParseScopeError(msg string) (required, granted []string, ok bool) {
	m := scopeRequiredRe.FindStringSubmatch(msg)
	if m == nil {
		return nil, nil, false
	}
	required = splitScopes(m[1])
	if g := scopeGrantedRe.FindStringSubmatch(msg); g != nil {
		granted = splitScopes(g[1])
	}
	return required, granted, true
}

func splitScopes(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// scopeCovers reports whether the granted scopes satisfy want: an exact
// match, write:X covering read:X, or the wildcard "all".
func scopeCovers(granted []string, want string) bool {
	for _, g := range granted {
		if g == "all" || g == want {
			return true
		}
		if strings.HasPrefix(g, "write:") && "read:"+strings.TrimPrefix(g, "write:") == want {
			return true
		}
	}
	return false
}

// scopeGuidance renders an actionable note for a failed probe. want is the
// scope the user should grant (the write variant when runtime needs writes,
// even though the probe itself is read-only). Falls back to the raw error
// when it is not a recognizable scope denial.
func scopeGuidance(err error, want string) string {
	_, granted, ok := ParseScopeError(err.Error())
	if !ok || scopeCovers(granted, want) {
		return err.Error()
	}
	return fmt.Sprintf("Token 缺少 %s 权限（当前仅含：%s）——请到 Gitea → 设置 → 应用 重新生成 Token 并勾选所需 scope",
		want, strings.Join(granted, ", "))
}

// TestConnection verifies URL and token by probing each capability Matea
// needs, so scope problems surface as precise per-item results instead of a
// single opaque failure at the first call.
func (c *Client) TestConnection() (*ConnectionTestResult, error) {
	res := &ConnectionTestResult{RequiredScopes: RequiredTokenScopes}
	if c.BaseURL == "" {
		res.Message = "Gitea 地址不能为空"
		return res, nil
	}
	if c.Token == "" {
		res.Message = "管理员 Token 不能为空"
		return res, nil
	}

	// 1. identity — GET /user requires read:user. Without it nothing else
	//    can be attributed to a user, so fail fast here.
	user, err := c.GetCurrentUser()
	if err != nil {
		detail := scopeGuidance(err, "read:user")
		res.Checks = append(res.Checks, PermissionCheck{
			Key: "identity", Label: "Token 身份验证", Scope: "read:user",
			OK: false, Required: true, Detail: detail,
		})
		res.Message = detail
		return res, nil
	}
	res.Username = user.Login
	res.IsAdmin = user.IsAdmin
	res.Checks = append(res.Checks, PermissionCheck{
		Key: "identity", Label: fmt.Sprintf("Token 身份验证（用户 %s）", user.Login), Scope: "read:user",
		OK: true, Required: true,
	})

	// 2. repository read — repo search / contents / branches / PR metadata.
	repos, err := c.ListRepos()
	if err != nil {
		res.Checks = append(res.Checks, PermissionCheck{
			Key: "repo", Label: "仓库访问", Scope: "write:repository",
			OK: false, Required: true, Detail: scopeGuidance(err, "write:repository"),
		})
	} else {
		res.RepoCount = len(repos)
		res.Checks = append(res.Checks, PermissionCheck{
			Key: "repo", Label: fmt.Sprintf("仓库访问（可见 %d 个仓库）", len(repos)), Scope: "write:repository",
			OK: true, Required: true,
		})
	}

	// 3. issue read — posting comments needs write:issue at runtime; a read
	//    probe is the only side-effect-free check available. Only probed when
	//    the repo list succeeded (an empty list skips the probe with a note).
	if err == nil {
		res.Checks = append(res.Checks, c.probeIssueAccess(repos))
	}

	// 4. admin — creating agent accounts and site-level webhooks. Optional:
	//    Matea degrades to manual management without it.
	switch {
	case !user.IsAdmin:
		res.Checks = append(res.Checks, PermissionCheck{
			Key: "admin", Label: "管理员能力（自动创建 Agent 账号、站点 Webhook）", Scope: "write:admin",
			OK: false, Required: false,
			Detail: "当前用户非站点管理员——无法自动创建 Agent 账号与站点级 Webhook（可手动管理，降级可用）",
		})
	default:
		if _, err := c.ListAdminWebhooks(); err != nil {
			res.Checks = append(res.Checks, PermissionCheck{
				Key: "admin", Label: "管理员能力（自动创建 Agent 账号、站点 Webhook）", Scope: "write:admin",
				OK: false, Required: false, Detail: scopeGuidance(err, "write:admin"),
			})
		} else {
			res.Checks = append(res.Checks, PermissionCheck{
				Key: "admin", Label: "管理员能力（自动创建 Agent 账号、站点 Webhook）", Scope: "write:admin",
				OK: true, Required: false,
			})
		}
	}

	var failed, warns []string
	for _, chk := range res.Checks {
		switch {
		case chk.Skipped:
		case chk.Required && !chk.OK:
			failed = append(failed, chk.Detail)
		case !chk.Required && !chk.OK:
			warns = append(warns, chk.Detail)
		}
	}
	res.OK = len(failed) == 0
	if res.OK {
		msg := fmt.Sprintf("连接成功（用户 %s，可见 %d 个仓库）", res.Username, res.RepoCount)
		for _, w := range warns {
			msg += "；警告：" + w
		}
		res.Message = msg
	} else {
		res.Message = strings.Join(failed, "；")
	}
	return res, nil
}

// probeIssueAccess verifies the issue token scope by listing issues of the
// first few visible repos (read-only, no side effects). A scope denial is
// definitive; repo-specific failures (issues disabled, etc.) fall through to
// the next repo, and an inconclusive run is reported as skipped rather than
// failed.
func (c *Client) probeIssueAccess(repos []RepoItem) PermissionCheck {
	chk := PermissionCheck{
		Key: "issue", Label: "Issue 访问（评论/标签）", Scope: "write:issue", Required: true,
	}
	if len(repos) == 0 {
		chk.Skipped = true
		chk.Detail = "无可见仓库，跳过预检——请确认 Token 已勾选 write:issue，否则无法发表评论"
		return chk
	}
	const maxProbe = 3
	probed := 0
	var lastErr error
	for _, r := range repos {
		if probed >= maxProbe {
			break
		}
		parts := strings.SplitN(r.FullName, "/", 2)
		if len(parts) != 2 {
			continue
		}
		probed++
		err := c.CheckIssueRead(parts[0], parts[1])
		if err == nil {
			chk.OK = true
			return chk
		}
		lastErr = err
		if _, _, isScopeErr := ParseScopeError(err.Error()); isScopeErr {
			chk.Detail = scopeGuidance(err, "write:issue")
			return chk
		}
		// Repo-specific failure (issues unit disabled etc.) — try the next one.
	}
	chk.Skipped = true
	chk.Detail = fmt.Sprintf("未能完成 Issue 预检（%v）——请确认 Token 已勾选 write:issue，否则无法发表评论", lastErr)
	return chk
}
