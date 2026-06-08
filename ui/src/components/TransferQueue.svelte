<script lang="ts">
  import { staged, transfers, canSend, target, devices, pairedIds, type StagedFile } from "../lib/state";
  import { apiFetch, apiPost } from "../lib/api";
  import { pollTransfers, refreshPairedStatus } from "../lib/polling";
  import { fmtSize, escapeHtml } from "../lib/utils";
  import { get } from "svelte/store";

  let { toast }: { toast: (msg: string, err?: boolean) => void } = $props();

  async function respondTransfer(id: string, accept: boolean) {
    const ep = accept ? "/api/transfers/accept" : "/api/transfers/reject";
    try { await apiFetch(ep + "?id=" + encodeURIComponent(id), { method: "POST" }); pollTransfers(); } catch {}
  }

  async function cancelTransfer(id: string) {
    try { await apiFetch("/api/transfers/cancel?id=" + encodeURIComponent(id), { method: "POST" }); pollTransfers(); } catch {}
  }

  async function pauseTransfer(id: string) {
    try { await apiFetch("/api/transfers/pause?id=" + encodeURIComponent(id), { method: "POST" }); pollTransfers(); } catch {}
  }

  async function resumeTransfer(id: string) {
    try { await apiFetch("/api/transfers/resume?id=" + encodeURIComponent(id), { method: "POST" }); pollTransfers(); } catch {}
  }

  async function retryTransfer(id: string) {
    try {
      const res = await apiFetch("/api/transfers/retry?id=" + encodeURIComponent(id), { method: "POST" });
      if (!res.ok) { toast(await res.text() || "Retry failed", true); return; }
      toast("Retrying…");
      pollTransfers();
    } catch { toast("Retry failed", true); }
  }

  function openFolder() {
    apiFetch("/api/open-folder", { method: "POST" }).catch(() => {});
  }

  async function sendAll() {
    const tgt = get(devices).find(d => d.id === get(target));
    const files = get(staged);
    if (!tgt || !files.length) return;
    await refreshPairedStatus();
    if (!get(pairedIds).has(tgt.id)) { toast("Pair with " + tgt.name + " first", true); return; }
    const paths = files.map(f => f.path);
    try {
      const res = await apiPost("/api/send-path", { to: get(target), paths });
      if (!res.ok) { toast(await res.text() || "Send failed", true); return; }
      const folderCount = files.filter(f => f.is_folder).length;
      const fileCount = files.length - folderCount;
      const parts: string[] = [];
      if (fileCount > 0) parts.push(`${fileCount} file${fileCount > 1 ? "s" : ""}`);
      if (folderCount > 0) parts.push(`${folderCount} folder${folderCount > 1 ? "s" : ""}`);
      staged.set([]);
      toast(`Sending ${parts.join(" and ")} to ${tgt.name}`);
      pollTransfers();
    } catch (e: any) { toast("Send failed: " + (e.message || ""), true); }
  }

  async function clearAll() {
    staged.set([]);
    try { await apiFetch("/api/transfers/clear", { method: "POST" }); } catch {}
    await pollTransfers();
  }

  function transferLabel(t: any): string {
    const pct = t.size > 0 ? Math.round(t.sent / t.size * 100) : (t.status === "done" ? 100 : 0);
    if (t.status === "preparing") return "Preparing…";
    if (t.status === "pending") return "Pending";
    if (t.status === "paused") return "Paused";
    if (t.status === "done" && t.err) return "Partial";
    if (t.status === "done") return t.dir === "recv" ? "Received" : "Sent";
    if (t.status === "error") return "Failed";
    if (t.status === "canceled") return "Canceled";
    return pct + "%";
  }

  function transferSub(t: any): string {
    const recv = t.dir === "recv";
    const active = t.status === "sending" || t.status === "paused";
    let sub = (recv ? "↓ from " : "↑ to ") + (t.peer || "");
    if (t.status === "preparing") sub += " · preparing…";
    else if (t.status === "pending") sub += " · " + fmtSize(t.size) + " · waiting for approval";
    else if (active) sub += " · " + fmtSize(t.sent) + " / " + fmtSize(t.size);
    else sub += " · " + fmtSize(t.size);
    if (t.status === "sending" && t.rate) sub += " · " + fmtSize(t.rate) + "/s";
    if (t.status === "paused") sub += " · paused";
    if (t.status === "done" && t.err) sub += " · " + t.err;
    return sub;
  }

  function transferPct(t: any): number {
    return t.size > 0 ? Math.round(t.sent / t.size * 100) : (t.status === "done" ? 100 : 0);
  }

  function statusClass(t: any): string {
    if (t.status === "error" || t.status === "canceled") return "err";
    if (t.status === "done" && !t.err) return "done";
    return "";
  }
</script>

<div class="queue">
  {#each $transfers as t (t.id)}
    {@const isFolder = t.name.startsWith("📁 ")}
    {@const name = isFolder ? t.name.slice(3) : t.name}
    {@const recv = t.dir === "recv"}
    {@const sending = t.status === "sending"}
    {@const pending = t.status === "pending"}
    {@const paused = t.status === "paused"}
    {@const active = sending || paused}
    <div class="file {statusClass(t)}">
      <div class="meta">
        <div class="name">
          {#if isFolder}
            <svg class="ficon" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="#7EB6FF" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"/></svg>
          {:else}
            <svg class="ficon" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="#7EB6FF" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/></svg>
          {/if}
          {name}
        </div>
        <div class="sub">{transferSub(t)}</div>
        <div class="bar"><i style="width:{transferPct(t)}%"></i></div>
      </div>
      <div class="pct">{transferLabel(t)}</div>
      {#if pending}
        <!-- svelte-ignore a11y_click_events_have_key_events -->
        <!-- svelte-ignore a11y_no_static_element_interactions -->
        <div class="accept" onclick={() => respondTransfer(t.id, true)}>Accept</div>
        <!-- svelte-ignore a11y_click_events_have_key_events -->
        <!-- svelte-ignore a11y_no_static_element_interactions -->
        <div class="reject" onclick={() => respondTransfer(t.id, false)}>Reject</div>
      {/if}
      {#if t.retryable}
        <!-- svelte-ignore a11y_click_events_have_key_events -->
        <!-- svelte-ignore a11y_no_static_element_interactions -->
        <div class="retry" onclick={() => retryTransfer(t.id)}>Retry</div>
      {/if}
      {#if sending && !recv}
        <!-- svelte-ignore a11y_click_events_have_key_events -->
        <!-- svelte-ignore a11y_no_static_element_interactions -->
        <div class="pause" onclick={() => pauseTransfer(t.id)}>Pause</div>
      {/if}
      {#if paused && !recv}
        <!-- svelte-ignore a11y_click_events_have_key_events -->
        <!-- svelte-ignore a11y_no_static_element_interactions -->
        <div class="pause" onclick={() => resumeTransfer(t.id)}>Resume</div>
      {/if}
      {#if t.status === "done"}
        <!-- svelte-ignore a11y_click_events_have_key_events -->
        <!-- svelte-ignore a11y_no_static_element_interactions -->
        <span class="openfolder" title="Open folder" onclick={openFolder}>
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"/></svg>
        </span>
      {/if}
      {#if active && !recv}
        <!-- svelte-ignore a11y_click_events_have_key_events -->
        <!-- svelte-ignore a11y_no_static_element_interactions -->
        <div class="cancel" title="Cancel" onclick={() => cancelTransfer(t.id)}>×</div>
      {/if}
    </div>
  {/each}

  {#each $staged as f (f.path)}
    <div class="file">
      <div class="meta">
        <div class="name">
          {#if f.is_folder}
            <svg class="ficon" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="#7EB6FF" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"/></svg>
          {:else}
            <svg class="ficon" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="#7EB6FF" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/></svg>
          {/if}
          {f.name}
        </div>
        <div class="sub">{f.is_folder ? `${f.file_count} file${f.file_count !== 1 ? "s" : ""} · ${fmtSize(f.size)}` : fmtSize(f.size)}</div>
        <div class="bar"><i style="width:0%"></i></div>
      </div>
      <div class="pct">Ready</div>
    </div>
  {/each}
</div>

<div class="sendbar">
  <button disabled={!$canSend} onclick={sendAll}>Send</button>
  <button class="ghost" onclick={clearAll}>Clear</button>
</div>

<style>
  .queue { margin-bottom: 8px; }
  .file {
    display: flex; align-items: center; gap: 9px; padding: 7px 0;
    border-top: 1px solid var(--border);
  }
  .file:first-child { border-top: none; }
  .file .meta { flex: 1; min-width: 0; }
  .file .name { font-weight: 600; font-size: 12px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; display: flex; align-items: center; gap: 6px; }
  :global(.ficon) { flex-shrink: 0; }
  .file .sub { font-size: 10px; color: var(--muted); }
  .bar { height: 5px; border-radius: 5px; background: var(--border); overflow: hidden; margin-top: 5px; }
  .bar > i { display: block; height: 100%; width: 0; background: linear-gradient(90deg, var(--accent), var(--accent-2)); transition: width .12s; }
  .file .pct { font-variant-numeric: tabular-nums; font-size: 11.5px; color: var(--muted); width: 50px; text-align: right; }
  .file.done .pct { color: var(--ok); }
  .file.err .pct { color: var(--err); }
  .file .cancel { color: var(--muted); font-size: 17px; line-height: 1; padding: 2px 6px; cursor: pointer; }
  .file .cancel:hover { color: var(--err); }
  .file .retry { color: var(--accent); font-size: 10px; font-weight: 600; padding: 2px 7px; border: 1px solid var(--accent); border-radius: 5px; cursor: pointer; white-space: nowrap; }
  .file .retry:hover { background: var(--accent); color: #fff; }
  .file .pause { color: var(--muted); font-size: 10px; font-weight: 600; padding: 2px 7px; border: 1px solid var(--border); border-radius: 5px; cursor: pointer; white-space: nowrap; }
  .file .pause:hover { border-color: var(--muted); color: var(--text); }
  .file .accept { color: var(--ok); font-size: 10px; font-weight: 600; padding: 2px 7px; border: 1px solid var(--ok); border-radius: 5px; cursor: pointer; white-space: nowrap; }
  .file .accept:hover { background: var(--ok); color: #fff; }
  .file .reject { color: var(--err); font-size: 10px; font-weight: 600; padding: 2px 7px; border: 1px solid var(--err); border-radius: 5px; cursor: pointer; white-space: nowrap; }
  .file .reject:hover { background: var(--err); color: #fff; }
  .file .openfolder { color: var(--muted); font-size: 13px; padding: 2px 5px; cursor: pointer; opacity: .5; text-decoration: none; }
  .file .openfolder:hover { color: var(--accent); opacity: 1; }
  .sendbar { display: flex; gap: 7px; margin-bottom: 10px; }
</style>
