<script lang="ts">
  import { onMount } from "svelte";
  import { me, pairedIds } from "./lib/state";
  import { apiFetch } from "./lib/api";
  import { startPolling, refreshPairedStatus } from "./lib/polling";
  import DeviceList from "./components/DeviceList.svelte";
  import DropZone from "./components/DropZone.svelte";
  import TransferQueue from "./components/TransferQueue.svelte";
  import PairModal from "./components/PairModal.svelte";
  import ConsentOverlay from "./components/ConsentOverlay.svelte";
  import Chat from "./components/Chat.svelte";
  import Toast from "./components/Toast.svelte";
  import SettingsModal from "./components/SettingsModal.svelte";

  let toastRef: Toast;
  let pairModalRef: PairModal;
  let chatRef: Chat;
  let settingsRef: SettingsModal;

  function toast(msg: string, err = false) {
    toastRef?.show(msg, err);
  }

  function handlePair(id: string, name: string) {
    pairModalRef?.show(id, name);
  }

  async function handleUnpair(id: string) {
    try {
      await apiFetch("/api/pair/unpair?id=" + encodeURIComponent(id), { method: "POST" });
      pairedIds.update(s => { s.delete(id); return new Set(s); });
      toast("Device unpaired");
    } catch { toast("Unpair failed", true); }
  }

  function handleChat(id: string, name: string) {
    chatRef?.openChat(id, name);
  }

  function quit() {
    apiFetch("/api/quit", { method: "POST" });
  }

  onMount(() => {
    let stopFn: (() => void) | undefined;
    startPolling().then(fn => { stopFn = fn; });
    return () => stopFn?.();
  });
</script>

<div class="wrap">
  <header>
    <div class="logo">⇅</div>
    <div>
      <h1>SwiftDrop</h1>
      <div class="me">
        <span class="dot" class:live={$me !== null}></span>
        <span>{$me ? "This device: " + $me.name : "connecting…"}</span>
        {#if $me?.ip}
          <span class="ip"> · {$me.ip}</span>
        {/if}
      </div>
    </div>
    <button class="ghost settings" onclick={() => settingsRef?.show()} title="Settings">⚙</button>
    <button class="ghost quit" onclick={quit} title="Quit SwiftDrop">Quit</button>
  </header>

  <div class="content">
    <DeviceList onPair={handlePair} onUnpair={handleUnpair} onChat={handleChat} {toast} />
    <DropZone />
    <TransferQueue {toast} />
    <Chat bind:this={chatRef} />
  </div>
</div>

<Toast bind:this={toastRef} />
<ConsentOverlay />
<PairModal bind:this={pairModalRef} {toast} />
<SettingsModal bind:this={settingsRef} {toast} />

<style>
  .wrap {
    height: 100%; display: flex; flex-direction: column;
    padding: 12px; overflow: hidden;
  }
  header { display: flex; align-items: center; gap: 8px; margin-bottom: 10px; flex: none; }
  .logo {
    width: 26px; height: 26px; border-radius: 7px; flex: none;
    background: linear-gradient(135deg, var(--accent), var(--accent-2));
    display: grid; place-items: center; color: #fff; font-size: 13px;
  }
  header h1 { font-size: 14px; margin: 0; letter-spacing: .2px; }
  .settings { margin-left: auto; flex: none; padding: 4px 9px; font-size: 13px; line-height: 1; }
  .quit { flex: none; padding: 4px 10px; font-size: 11px; }
  .me { font-size: 10.5px; color: var(--muted); margin-top: 1px; }
  .ip { color: var(--muted); }
  .dot { width: 6px; height: 6px; border-radius: 50%; background: var(--muted); display: inline-block; margin-right: 4px; vertical-align: middle; }
  .dot.live { background: var(--ok); box-shadow: 0 0 0 3px rgba(70,211,154,.18); }
  .content { flex: 1; overflow-y: auto; overflow-x: hidden; min-height: 0; padding-right: 2px; }
</style>
