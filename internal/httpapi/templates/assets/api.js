const TOKEN_KEY = "ledgerly_access_token";

export function getToken() {
  return sessionStorage.getItem(TOKEN_KEY);
}

export function setToken(token) {
  if (token) sessionStorage.setItem(TOKEN_KEY, token);
  else sessionStorage.removeItem(TOKEN_KEY);
}

export async function api(path, options = {}) {
  const headers = new Headers(options.headers || {});
  if (options.body !== undefined) headers.set("Content-Type", "application/json");
  const token = getToken();
  if (token) headers.set("Authorization", `Bearer ${token}`);

  const response = await fetch(path, { ...options, headers });
  if (response.status === 204) return null;

  const payload = await response.json().catch(() => null);
  if (!response.ok) {
    if (response.status === 401) {
      setToken(null);
      window.dispatchEvent(new CustomEvent("ledgerly:unauthorized"));
    }
    throw new Error(payload?.error || `Request failed (${response.status})`);
  }
  return payload;
}

export function jsonRequest(method, value) {
  return { method, body: JSON.stringify(value) };
}
