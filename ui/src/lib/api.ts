// ── API layer ───────────────────────────────────────────────────────────────
// Wraps fetch with the X-API-Token header injected by the Go backend.

let apiToken = (window as any).__SWIFTDROP_TOKEN__ || "";

export async function initToken(): Promise<void> {
  if (!apiToken) {
    apiToken = await (await fetch("/api/token")).text();
  }
}

export function apiFetch(url: string, opts: RequestInit = {}): Promise<Response> {
  opts.headers = { ...(opts.headers as Record<string, string>), "X-API-Token": apiToken };
  return fetch(url, opts);
}

export function apiJson<T>(url: string, opts: RequestInit = {}): Promise<T> {
  return apiFetch(url, opts).then(r => r.json());
}

export function apiPost(url: string, body?: unknown): Promise<Response> {
  const opts: RequestInit = { method: "POST" };
  if (body !== undefined) {
    opts.headers = { "Content-Type": "application/json" };
    opts.body = JSON.stringify(body);
  }
  return apiFetch(url, opts);
}
