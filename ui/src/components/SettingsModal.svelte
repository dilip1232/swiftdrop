<script lang="ts">
  import { apiFetch, apiPost } from "../lib/api";

  let { toast }: { toast: (msg: string, err?: boolean) => void } = $props();

  let open = $state(false);
  let dirInput = $state("");
  let defaultDir = $state("");
  let currentDir = $state("");
  let error = $state("");
  let saving = $state(false);

  export async function show() {
    error = "";
    open = true;
    try {
      const s = await (await apiFetch("/api/settings")).json();
      defaultDir = s.downloadDirDefault ?? "";
      currentDir = s.downloadDir ?? "";
      dirInput = s.downloadDirCustom ?? "";
    } catch {
      toast("Failed to load settings", true);
      open = false;
    }
  }

  function close() {
    open = false;
  }

  async function save(dir: string) {
    error = "";
    saving = true;
    try {
      const res = await apiPost("/api/settings", { downloadDir: dir });
      if (!res.ok) {
        error = (await res.text()) || "Could not save";
        return;
      }
      const body = await res.json();
      currentDir = body.downloadDir ?? currentDir;
      toast("Download folder updated");
      close();
    } catch {
      error = "Could not save";
    } finally {
      saving = false;
    }
  }

  function handleOverlayClick(e: MouseEvent) {
    if ((e.target as HTMLElement).classList.contains("modal-overlay")) close();
  }

  function handleKeydown(e: KeyboardEvent) {
    if (e.key === "Enter") save(dirInput.trim());
  }
</script>

{#if open}
  <!-- svelte-ignore a11y_click_events_have_key_events -->
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <div class="modal-overlay" onclick={handleOverlayClick}>
    <div class="modal">
      <div class="modal-header">
        <h3>Settings</h3>
        <!-- svelte-ignore a11y_click_events_have_key_events -->
        <!-- svelte-ignore a11y_no_static_element_interactions -->
        <span class="modal-close" onclick={close}>×</span>
      </div>

      <p class="label">Download folder</p>
      <p class="desc">Where received files are saved. Leave blank to use the default.</p>
      <input
        type="text"
        class="dir-input"
        placeholder={defaultDir}
        autocomplete="off"
        spellcheck="false"
        bind:value={dirInput}
        onkeydown={handleKeydown}
      />
      <p class="current">Currently saving to: <span>{currentDir || defaultDir}</span></p>

      {#if error}
        <p class="settings-error">{error}</p>
      {/if}

      <div class="actions">
        <button class="ghost" onclick={() => save("")} disabled={saving}>Reset to default</button>
        <button class="save" onclick={() => save(dirInput.trim())} disabled={saving}>
          {saving ? "Saving…" : "Save"}
        </button>
      </div>
    </div>
  </div>
{/if}

<style>
  .modal-overlay { position: fixed; inset: 0; background: rgba(0,0,0,.55); display: flex; justify-content: center; align-items: center; z-index: 999; }
  .modal { background: var(--bg); border: 1px solid var(--border); border-radius: 14px; padding: 16px 18px; width: 340px; max-width: 92vw; box-shadow: 0 12px 40px rgba(0,0,0,.5); }
  .modal-header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 14px; }
  .modal-header h3 { margin: 0; font-size: 15px; }
  .modal-close { cursor: pointer; font-size: 20px; color: var(--muted); padding: 2px 6px; }
  .modal-close:hover { color: var(--err); }
  .label { margin: 0 0 2px; font-size: 12px; font-weight: 600; }
  .desc { margin: 0 0 10px; color: var(--muted); font-size: 11.5px; }
  .dir-input { width: 100%; box-sizing: border-box; font-size: 12px; padding: 8px 10px; border-radius: 9px; border: 1.5px solid var(--border); background: var(--panel-2); color: var(--text); outline: none; }
  .dir-input:focus { border-color: var(--accent); }
  .current { margin: 8px 0 0; color: var(--muted); font-size: 11px; word-break: break-all; }
  .current span { color: var(--text); }
  .settings-error { margin: 8px 0 0; color: var(--err); font-size: 12px; word-break: break-all; }
  .actions { display: flex; gap: 8px; margin-top: 14px; }
  .actions .ghost { flex: 1; padding: 8px 10px; font-size: 11px; }
  .actions .save { flex: 1; padding: 8px 10px; font-size: 11px; }
</style>
