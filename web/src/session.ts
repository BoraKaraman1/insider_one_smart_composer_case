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
    // Depolama kapalıysa anahtar yalnız bu sayfada kalır.
  }
}

export function clearToken() {
  token.value = null
  try {
    sessionStorage.removeItem(key)
  } catch {
  }
}

export const api = createApi({ token: () => token.value, onUnauthorized: clearToken })
