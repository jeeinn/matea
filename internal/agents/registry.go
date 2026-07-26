package agents

import (
	"log"
	"sync"

	"github.com/jeeinn/matea/internal/store"
)

// Registry holds active agents in memory for fast lookup.
type Registry struct {
	mu     sync.RWMutex
	agents map[int64]*store.Agent  // by ID
	byUser map[string]*store.Agent // by Gitea username
}

// NewRegistry creates a new empty Registry.
func NewRegistry() *Registry {
	return &Registry{
		agents: make(map[int64]*store.Agent),
		byUser: make(map[string]*store.Agent),
	}
}

// LoadFromDB populates the registry from the database.
func (r *Registry) LoadFromDB(db *store.DB) error {
	agents, err := db.ListAgents()
	if err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	for _, a := range agents {
		if a.Status == "active" {
			r.agents[a.ID] = a
			r.byUser[a.GiteaUsername] = a
		}
	}

	log.Printf("[INFO] Loaded %d active agents into registry", len(r.agents))
	return nil
}

// GetByID returns an agent by ID.
func (r *Registry) GetByID(id int64) *store.Agent {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.agents[id]
}

// GetByGiteaUsername returns an agent by their Gitea username.
func (r *Registry) GetByGiteaUsername(username string) *store.Agent {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.byUser[username]
}

// GiteaUsernamesByRole returns Gitea usernames of active agents with the given role.
func (r *Registry) GiteaUsernamesByRole(role string) []string {
	if r == nil || role == "" {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []string
	for _, a := range r.agents {
		if a != nil && a.Role == role && a.GiteaUsername != "" {
			out = append(out, a.GiteaUsername)
		}
	}
	return out
}

// Refresh reloads a single agent into the registry.
func (r *Registry) Refresh(agent *store.Agent) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if agent.Status == "active" {
		r.agents[agent.ID] = agent
		r.byUser[agent.GiteaUsername] = agent
	} else {
		delete(r.agents, agent.ID)
		delete(r.byUser, agent.GiteaUsername)
	}
}

// Remove removes an agent from the registry.
func (r *Registry) Remove(id int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if a, ok := r.agents[id]; ok {
		delete(r.byUser, a.GiteaUsername)
		delete(r.agents, id)
	}
}
