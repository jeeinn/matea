package agents

import (
	"log"
	"strings"

	giteaingress "github.com/jeeinn/matea/internal/ingress/gitea"
)

// InteractionHandler processes @Mention interactions.
type InteractionHandler struct {
	registry *Registry
	callback func(agentID int64, evt *giteaingress.WebhookEvent)
}

// NewInteractionHandler creates a new InteractionHandler.
func NewInteractionHandler(registry *Registry, callback func(agentID int64, evt *giteaingress.WebhookEvent)) *InteractionHandler {
	return &InteractionHandler{
		registry: registry,
		callback: callback,
	}
}

// HandleComment checks if the comment mentions any agent and triggers interaction.
func (h *InteractionHandler) HandleComment(evt *giteaingress.WebhookEvent) {
	if evt.Comment == nil {
		return
	}

	// Extract all @mentions from the comment body
	mentions := extractMentions(evt.Comment.Body)

	for _, username := range mentions {
		agent := h.registry.GetByGiteaUsername(username)
		if agent == nil {
			continue // Not our agent
		}

		log.Printf("[INFO] @Mention detected: agent=%s in repo=%s issue=#%d",
			agent.GiteaUsername, evt.Repo.FullName, evt.Issue.Number)

		if h.callback != nil {
			go h.callback(agent.ID, evt)
		}
	}
}

// extractMentions extracts all @username patterns from text.
func extractMentions(text string) []string {
	var mentions []string
	words := strings.Fields(text)
	for _, w := range words {
		w = strings.TrimRight(w, ".,;:!?")
		if strings.HasPrefix(w, "@") && len(w) > 1 {
			mentions = append(mentions, w[1:])
		}
	}
	return mentions
}
