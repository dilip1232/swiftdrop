<script lang="ts">
  import { apiFetch, apiPost } from "../lib/api";
  import { pairedIds } from "../lib/state";
  import { refreshPairedStatus } from "../lib/polling";

  let { toast }: { toast: (msg: string, err?: boolean) => void } = $props();

  let open = $state(false);
  let step = $state<"choose" | "qr" | "pin-display" | "pin-input">("choose");
  let deviceId = $state("");
  let deviceName = $state("");
  let pin = $state("");
  let pinInput = $state("");
  let qrSrc = $state("");
  let error = $state("");
  let pollTimer: ReturnType<typeof setInterval> | null = null;

  export function show(id: string, name: string) {
    deviceId = id;
    deviceName = name;
    step = "choose";
    pin = "";
    pinInput = "";
    qrSrc = "";
    error = "";
    open = true;
  }

  function close() {
    if (pollTimer) { clearInterval(pollTimer); pollTimer = null; }
    open = false;
  }

  function startPollPaired() {
    if (pollTimer) clearInterval(pollTimer);
    pollTimer = setInterval(async () => {
      try {
        const s = await (await apiFetch("/api/pair/status?id=" + encodeURIComponent(deviceId))).json();
        if (s.paired) {
          toast(`Paired with ${deviceName}!`);
          close();
          refreshPairedStatus();
        }
      } catch {}
    }, 2000);
  }

  async function generatePin() {
    try {
      const r = await (await apiFetch("/api/pair/begin", { method: "POST" })).json();
      pin = r.pin;
      step = "pin-display";
      startPollPaired();
    } catch { toast("Failed to start pairing", true); close(); }
  }

  async function showQR() {
    try {
      const r = await (await apiFetch("/api/pair/qr-begin", { method: "POST" })).json();
      const blob = Uint8Array.from(atob(r.qr_png), c => c.charCodeAt(0));
      qrSrc = URL.createObjectURL(new Blob([blob], { type: "image/png" }));
      step = "qr";
      startPollPaired();
    } catch { toast("Failed to generate QR code", true); close(); }
  }

  function showPinInput() {
    step = "pin-input";
  }

  async function submitPin() {
    const p = pinInput.trim();
    if (!p || p.length < 4) { error = "Enter a valid PIN"; return; }
    try {
      const res = await apiPost("/api/pair/submit", { device_id: deviceId, pin: p });
      if (!res.ok) { error = await res.text() || "Pairing failed"; return; }
      toast(`Paired with ${deviceName}!`);
      close();
      refreshPairedStatus();
    } catch { error = "Pairing failed"; }
  }

  function handleKeydown(e: KeyboardEvent) {
    if (e.key === "Enter") submitPin();
  }

  function handleOverlayClick(e: MouseEvent) {
    if ((e.target as HTMLElement).classList.contains("modal-overlay")) close();
  }
</script>

{#if open}
  <!-- svelte-ignore a11y_click_events_have_key_events -->
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <div class="modal-overlay" onclick={handleOverlayClick}>
    <div class="modal">
      <div class="modal-header">
        <h3>Pair Device</h3>
        <!-- svelte-ignore a11y_click_events_have_key_events -->
        <!-- svelte-ignore a11y_no_static_element_interactions -->
        <span class="modal-close" onclick={close}>×</span>
      </div>

      {#if step === "choose"}
        <p class="desc">Choose how to pair with <b>{deviceName}</b>:</p>
        <button class="pair-option" onclick={showQR}>Show QR Code</button>
        <div class="qr-or">— or use PIN —</div>
        <button class="pair-option" onclick={generatePin}>Generate PIN on this device</button>
        <button class="pair-option ghost" onclick={showPinInput}>I have a PIN from the other device</button>
      {/if}

      {#if step === "qr"}
        <p class="desc">Scan this QR code with the other device:</p>
        <div class="qr-container"><img src={qrSrc} alt="QR Code" /></div>
        <p class="expire">Expires in 120 seconds</p>
      {/if}

      {#if step === "pin-display"}
        <p class="desc">Share this PIN with the other device:</p>
        <div class="pin-display">{pin}</div>
        <p class="expire">Expires in 60 seconds</p>
      {/if}

      {#if step === "pin-input"}
        <p class="desc">Enter the PIN shown on the other device:</p>
        <div class="pin-form">
          <input type="text" class="pin-input" maxlength="6" placeholder="000000" autocomplete="off" inputmode="numeric" bind:value={pinInput} onkeydown={handleKeydown} />
          <button class="pair-submit" onclick={submitPin}>Pair</button>
        </div>
        {#if error}
          <p class="pair-error">{error}</p>
        {/if}
      {/if}
    </div>
  </div>
{/if}

<style>
  .modal-overlay { position: fixed; inset: 0; background: rgba(0,0,0,.55); display: flex; justify-content: center; align-items: center; z-index: 999; }
  .modal { background: var(--bg); border: 1px solid var(--border); border-radius: 14px; padding: 16px 18px; width: 290px; max-width: 92vw; box-shadow: 0 12px 40px rgba(0,0,0,.5); }
  .modal-header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 14px; }
  .modal-header h3 { margin: 0; font-size: 15px; }
  .modal-close { cursor: pointer; font-size: 20px; color: var(--muted); padding: 2px 6px; }
  .modal-close:hover { color: var(--err); }
  .desc { margin: 0 0 12px; color: var(--muted); font-size: 13px; }
  .pair-option { display: block; width: 100%; margin-bottom: 8px; text-align: center; font-size: 11px; padding: 8px 10px; }
  .qr-or { color: var(--muted); font-size: 11px; margin: 10px 0 6px; text-align: center; }
  .qr-container { text-align: center; padding: 8px 0; }
  .qr-container img { border-radius: 10px; background: #fff; padding: 8px; max-width: 220px; }
  .pin-display { font-family: monospace; font-size: 26px; font-weight: 700; letter-spacing: 6px; text-align: center; padding: 12px; background: var(--panel-2); border-radius: 10px; color: var(--accent); }
  .expire { margin: 8px 0 0; color: var(--muted); font-size: 11px; text-align: center; }
  .pin-form { display: flex; flex-direction: column; gap: 8px; }
  .pin-input { font-family: monospace; font-size: 18px; letter-spacing: 5px; text-align: center; padding: 8px; border-radius: 9px; border: 1.5px solid var(--border); background: var(--panel-2); color: var(--text); outline: none; }
  .pin-input:focus { border-color: var(--accent); }
  .pair-submit { flex: none; padding: 8px 14px; width: 100%; }
  .pair-error { margin: 8px 0 0; color: var(--err); font-size: 12px; }
</style>
