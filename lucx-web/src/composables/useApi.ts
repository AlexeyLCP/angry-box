let token: string | null = localStorage.getItem('lucx_token')

export function useApi() {
  const setToken = (t: string | null) => {
    token = t
    if (t) localStorage.setItem('lucx_token', t)
    else localStorage.removeItem('lucx_token')
  }

  const headers = (): Record<string, string> => {
    const h: Record<string, string> = { 'Content-Type': 'application/json' }
    if (token) h['Authorization'] = `Bearer ${token}`
    return h
  }

  const handleResponse = async (res: Response) => {
    if (res.status === 401) {
      setToken(null)
      window.location.hash = '#/login'
      throw new Error('Unauthorized')
    }
    if (res.status === 204) return null as unknown

    const text = await res.text()

    if (!res.ok) {
      // Try to parse server error message
      let msg = `Server error (${res.status})`
      try {
        const err = JSON.parse(text)
        if (err.error) msg = err.error
      } catch {
        // Response is not JSON — use first 100 chars of text
        if (text) msg = text.slice(0, 100)
      }
      throw new Error(msg)
    }

    if (!text) return null as unknown
    return JSON.parse(text)
  }

  const request = async <T>(method: string, url: string, body?: unknown): Promise<T> => {
    let res: Response
    try {
      res = await fetch(url, {
        method,
        headers: headers(),
        body: body !== undefined ? JSON.stringify(body) : undefined,
      })
    } catch {
      throw new Error('Network error — is LucX Core running on :8744?')
    }
    return handleResponse(res) as T
  }

  const get = <T>(url: string) => request<T>('GET', url)
  const post = <T>(url: string, body?: unknown) => request<T>('POST', url, body)
  const put = <T>(url: string, body?: unknown) => request<T>('PUT', url, body)
  const del = <T>(url: string) => request<T>('DELETE', url)

  return { get, post, put, del, setToken, token: () => token }
}
