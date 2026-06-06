<script lang="ts">
  import { staged, targetDevice, targetPaired, type StagedFile } from "../lib/state";
  import { apiFetch } from "../lib/api";

  let over = $state(false);

  // Hint text based on current state
  let hint = $derived.by(() => {
    const dev = $targetDevice;
    const paired = $targetPaired;
    if (!dev) return "Pick a device above first";
    if (!paired) return "Pair with " + dev.name + " before sending";
    return "Sending to " + dev.name;
  });

  function stageFiles(infos: StagedFile[]) {
    if (!infos?.length) return;
    staged.update(cur => {
      const have = new Set(cur.map(f => f.path));
      const added = infos.filter(f => !have.has(f.path));
      return [...cur, ...added];
    });
  }

  async function pickFiles() {
    try {
      const res = await apiFetch("/api/pick", { method: "POST" });
      if (res.ok) stageFiles(await res.json());
    } catch {}
  }

  // On drop, read native file paths from the macOS drag pasteboard via Go.
  // This is instant (no upload) — Go reads the pasteboard for real paths.
  // Falls back to multipart upload if resolve-drop returns nothing.
  async function handleDrop(e: DragEvent) {
    e.preventDefault();
    over = false;
    try {
      // Try native pasteboard first (instant, zero-copy)
      const res = await apiFetch("/api/resolve-drop", { method: "POST" });
      if (res.ok) {
        const infos = await res.json();
        if (infos?.length) { stageFiles(infos); return; }
      }
    } catch {}
    // Fallback: upload via multipart (slower, for non-native platforms)
    const files = e.dataTransfer?.files;
    if (!files?.length) return;
    const form = new FormData();
    for (const f of files) form.append("files", f);
    try {
      const res = await apiFetch("/api/stage-upload", { method: "POST", body: form });
      if (res.ok) stageFiles(await res.json());
    } catch {}
  }

  function handleDragOver(e: DragEvent) { e.preventDefault(); over = true; }
  function handleDragEnter(e: DragEvent) { e.preventDefault(); over = true; }
  function handleDragLeave(e: DragEvent) { e.preventDefault(); over = false; }
</script>

<div class="stitle">Send files</div>
<!-- svelte-ignore a11y_click_events_have_key_events -->
<!-- svelte-ignore a11y_no_static_element_interactions -->
<div class="drop" class:over
  onclick={pickFiles}
  ondragover={handleDragOver}
  ondragenter={handleDragEnter}
  ondragleave={handleDragLeave}
  ondrop={handleDrop}
>
  <div class="big">📤</div>
  <p><b>Drop files or folders</b> or click to choose</p>
  <p class="hint">{hint}</p>
</div>

<style>
  .drop {
    display: block; padding: 14px 10px; text-align: center; margin-bottom: 8px;
    border: 1.5px dashed var(--border); border-radius: 10px;
    background: var(--panel); cursor: pointer; transition: .18s;
  }
  .drop:hover { border-color: var(--accent); }
  .drop.over { border-color: var(--accent); background: rgba(91,141,239,.10); }
  .drop .big { font-size: 18px; }
  .drop p { margin: 3px 0 0; color: var(--muted); font-size: 11px; }
  .drop b { color: var(--text); }
  .hint { color: var(--muted); font-size: 11px; }
</style>
