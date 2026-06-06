<script lang="ts">
  import { apiFetch } from "../lib/api";
  import { pollTransfers } from "../lib/polling";
  import { transfers } from "../lib/state";

  let open = $state(false);
  let tid = $state("");
  let title = $state("");
  let sub = $state("");

  // Called from Go via ExecJS when an incoming transfer needs consent.
  $effect(() => {
    (window as any).showConsentDialog = function(id: string, from: string, name: string, size: string) {
      tid = id;
      title = "Incoming from " + from;
      sub = name + " · " + size;
      open = true;
    };
  });

  // Auto-dismiss when the transfer resolves
  $effect(() => {
    if (open && tid) {
      const tr = $transfers.find(t => t.id === tid);
      if (tr && tr.status !== "pending") open = false;
    }
  });

  async function respond(accept: boolean) {
    const ep = accept ? "/api/transfers/accept" : "/api/transfers/reject";
    try { await apiFetch(ep + "?id=" + encodeURIComponent(tid), { method: "POST" }); pollTransfers(); } catch {}
    open = false;
  }
</script>

{#if open}
  <div class="consent-overlay">
    <div class="consent-box">
      <h3>{title}</h3>
      <div class="csub">{sub}</div>
      <div class="cbtn">
        <button class="rej" onclick={() => respond(false)}>Reject</button>
        <button onclick={() => respond(true)}>Accept</button>
      </div>
    </div>
  </div>
{/if}

<style>
  .consent-overlay { position: fixed; inset: 0; background: rgba(0,0,0,.45); display: flex; justify-content: center; align-items: flex-start; padding-top: 40px; z-index: 1000; }
  .consent-box { background: var(--bg); border: 1px solid var(--border); border-radius: 14px; padding: 18px 20px; width: 290px; max-width: 92vw; box-shadow: 0 12px 40px rgba(0,0,0,.5); text-align: center; }
  .consent-box h3 { margin: 0 0 4px; font-size: 14px; }
  .csub { color: var(--muted); font-size: 12px; margin-bottom: 14px; }
  .cbtn { display: flex; gap: 8px; }
  .cbtn button { flex: 1; }
  .rej { background: var(--panel-2); color: var(--err); border: 1px solid var(--err); }
</style>
