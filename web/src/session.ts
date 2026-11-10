// The admin token lives in sessionStorage only, and is sent only in the
// Authorization header (plan 1.5).
import { ref } from 'vue'
import { createApi } from './api/client'

const key = 'scc.token'

function load(): string | null {
  try {
    return sessionStorage.getItem(key)
  } catch {
    return null
  }
}

export const token = ref<string | null>(load())

export function setToken(t: string) {
  token.value = t
  try {
    sessionStorage.setItem(key, t)
  } catch {
    // storage blocked: the token lasts for this page only
  }
}

export function clearToken() {
  token.value = null
  try {
    sessionStorage.removeItem(key)
  } catch {
    // nothing stored
  }
}

// api is the app's client. A 401 anywhere returns to the token gate.
export const api = createApi({ token: () => token.value, onUnauthorized: clearToken })
