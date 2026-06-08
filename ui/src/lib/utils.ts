// ── Shared utility functions ────────────────────────────────────────────────

export function platformIcon(p: string): string {
  return ({ darwin: "🖥️", windows: "🪟", linux: "🐧", android: "📱", ios: "📱" } as Record<string, string>)[p] || "💻";
}

export function fmtSize(n: number): string {
  if (n < 1024) return n + " B";
  const u = ["KB", "MB", "GB", "TB"];
  let i = -1;
  do { n /= 1024; i++; } while (n >= 1024 && i < u.length - 1);
  return n.toFixed(1) + " " + u[i];
}

export function escapeHtml(s: string): string {
  return (s || "").replace(/[&<>"]/g, c => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" } as Record<string, string>)[c]);
}

export function fmtTime(ts: number): string {
  return new Date(ts).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}
