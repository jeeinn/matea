import { defineStore } from 'pinia'
import { fetchSetupStatus } from '../api/setup'

// Tracks first-run initialization state (C-3): the router redirects every
// page to /setup while the backend reports setup_required, and keeps /setup
// out of reach once initialization completes.
export const useSetupStore = defineStore('setup', {
  state: () => ({
    loaded: false,
    loadFailed: false,
    status: null
  }),

  getters: {
    setupRequired: (state) => !!state.status?.setup_required,
    missing: (state) => state.status?.missing || []
  },

  actions: {
    // ensureLoaded fetches once per session; call refresh() to re-poll.
    async ensureLoaded() {
      if (this.loaded || this.loadFailed) return
      await this.refresh()
    },

    async refresh() {
      try {
        this.status = await fetchSetupStatus()
        this.loaded = true
        this.loadFailed = false
      } catch (e) {
        // Backend unreachable: don't trap the UI in a redirect loop —
        // fall through to the normal auth flow.
        this.loadFailed = true
      }
    },

    // Called by the wizard after a successful /setup/complete.
    markComplete() {
      this.status = { ...(this.status || {}), setup_required: false, missing: [] }
      this.loaded = true
      this.loadFailed = false
    }
  }
})
