<script lang="ts">
  import { devices, target, pairedIds, type Peer } from "../lib/state";
  import { apiFetch, apiPost } from "../lib/api";
  import { pollDevices } from "../lib/polling";
  import { platformIcon, escapeHtml } from "../lib/utils";

  let { onPair, onUnpair, onChat, toast }: {
    onPair: (id: string, name: string) => void;
    onUnpair: (id: string) => void;
    onChat: (id: string, name: string) => void;
    toast: (msg: string, err?: boolean) => void;
  } = $props();

  let hostInput = $state("");
  let adding = $state(false);

  function selectDevice(id: string) {
    target.set(id);
  }

  async function addPeer() {
    const host = hostInput.trim();
    if (!host) return;
    adding = true;
    try {
      const res = await apiPost("/api/peers/add", { host });
      if (!res.ok) { toast(await res.text() || "Could not add device", true); return; }
      const peer: Peer = await res.json();
      hostInput = "";
      toast("Added " + peer.name);
      await pollDevices();
      target.set(peer.id);
    } catch { toast("Could not add device", true); }
    finally { adding = false; }
  }

  async function removePeer(id: string) {
    try {
      await apiPost("/api/peers/remove", { id });
      target.update(t => t === id ? null : t);
      await pollDevices();
    } catch {}
  }

  function handleKeydown(e: KeyboardEvent) {
    if (e.key === "Enter") addPeer();
  }
</script>

<div class="stitle">Devices</div>
<div class="devices">
  {#if $devices.length === 0}
    <div class="empty"><span class="spin"></span>Looking for devices…</div>
  {:else}
    {#each $devices as d (d.id)}
      {@const paired = $pairedIds.has(d.id)}
      <!-- svelte-ignore a11y_click_events_have_key_events -->
      <!-- svelte-ignore a11y_no_static_element_interactions -->
      <div class="device" class:active={d.id === $target} onclick={() => selectDevice(d.id)}>
        <div class="ic">{platformIcon(d.platform)}</div>
        <div class="info">
          <div class="nm">{d.name}</div>
          <div class="pf">{(d.host || "").replace(/:\d+$/, "") || d.platform || "device"}</div>
        </div>
        {#if paired}
          <!-- svelte-ignore a11y_click_events_have_key_events -->
          <!-- svelte-ignore a11y_no_static_element_interactions -->
          <span class="chat-btn" title="Chat" onclick={(e) => { e.stopPropagation(); onChat(d.id, d.name); }}>
            <svg viewBox="0 0 24 24"><path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"/></svg>
          </span>
        {/if}
        <!-- svelte-ignore a11y_click_events_have_key_events -->
        <!-- svelte-ignore a11y_no_static_element_interactions -->
        <span class="pair-btn" class:paired onclick={(e) => { e.stopPropagation(); paired ? onUnpair(d.id) : onPair(d.id, d.name); }}>
          {paired ? "Paired" : "Pair"}
        </span>
        <!-- svelte-ignore a11y_click_events_have_key_events -->
        <!-- svelte-ignore a11y_no_static_element_interactions -->
        <span class="rm" title="Remove" onclick={(e) => { e.stopPropagation(); removePeer(d.id); }}>×</span>
      </div>
    {/each}
  {/if}
</div>
<div class="addrow">
  <input type="text" inputmode="decimal" placeholder="Add by IP" bind:value={hostInput} onkeydown={handleKeydown} />
  <button class="ghost addbtn" disabled={adding} onclick={addPeer} title="Add device">＋</button>
</div>

<style>
  .devices { display: flex; flex-direction: column; gap: 4px; margin-bottom: 10px; }
  .device {
    display: flex; align-items: center; gap: 8px; padding: 7px 9px;
    border-radius: 9px; cursor: pointer; border: 1px solid var(--border);
    background: var(--panel); transition: .12s;
  }
  .device:hover { border-color: var(--muted); }
  .device.active { background: rgba(91,141,239,.10); border-color: var(--accent); }
  .device .ic { width: 24px; height: 24px; border-radius: 7px; flex: none; display: grid; place-items: center; background: var(--panel-2); font-size: 13px; }
  .device .info { flex: 1; min-width: 0; }
  .device .nm { font-weight: 600; font-size: 12px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .device .pf { font-size: 10px; color: var(--muted); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .device .rm { flex: none; color: var(--muted); opacity: .3; font-size: 14px; line-height: 1; padding: 0 2px; cursor: pointer; }
  .device:hover .rm { opacity: .7; }
  .device .rm:hover { color: var(--err); opacity: 1; }
  .empty { padding: 18px 10px; text-align: center; color: var(--muted); font-size: 11.5px; }
  .addrow { display: flex; gap: 5px; margin-bottom: 12px; }
  .addrow input { flex: 1; min-width: 0; font: inherit; font-size: 11px; padding: 6px 8px; border-radius: 8px; border: 1px solid var(--border); background: var(--panel); color: var(--text); }
  .addrow input::placeholder { color: var(--muted); }
  .addrow input:focus { outline: none; border-color: var(--accent); }
  .addbtn { flex: none; padding: 6px 10px; font-size: 13px; line-height: 1; }

  .pair-btn { background: var(--panel-2); border: 1px solid var(--border); border-radius: 7px; font-size: 10px; font-weight: 600; padding: 3px 8px; cursor: pointer; color: var(--muted); flex: none; line-height: 1.3; transition: .12s; white-space: nowrap; }
  .pair-btn:hover { border-color: var(--accent); color: var(--accent); }
  .pair-btn.paired { color: var(--ok); border-color: var(--ok); }
  .pair-btn.paired:hover { color: var(--err); border-color: var(--err); }

  .chat-btn {
    position: relative; flex: none; width: 26px; height: 26px;
    display: grid; place-items: center;
    border: 1px solid var(--border); border-radius: 7px; background: transparent;
    color: var(--muted); cursor: pointer; transition: .15s; line-height: 0;
  }
  .chat-btn svg { width: 14px; height: 14px; stroke: currentColor; fill: none; stroke-width: 1.8; stroke-linecap: round; stroke-linejoin: round; }
  .chat-btn:hover { border-color: var(--accent); color: var(--accent); background: rgba(79,140,255,.06); }
</style>
