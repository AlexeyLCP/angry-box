import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { authApi } from '@/api/client'
import { useApi } from '@/composables/useApi'

export const useAuthStore = defineStore('auth', () => {
  const token = ref<string | null>(localStorage.getItem('lucx_token'))
  const loading = ref(false)
  const error = ref<string | null>(null)

  const isAuthenticated = computed(() => !!token.value)

  const login = async (password: string) => {
    loading.value = true
    error.value = null
    try {
      const res = await authApi.login(password)
      const api = useApi()
      api.setToken(res.token)
      token.value = res.token
    } catch (e) {
      error.value = e instanceof Error ? e.message : 'Login failed'
      throw e
    } finally {
      loading.value = false
    }
  }

  const logout = () => {
    const api = useApi()
    api.setToken(null)
    token.value = null
  }

  return { token, loading, error, isAuthenticated, login, logout }
})
