<script lang="ts">
  import { apiFetch, apiPost } from "../lib/api";
  import { devices } from "../lib/state";
  import { fmtTime } from "../lib/utils";

  interface ChatMsg {
    id: string;
    text: string;
    dir: string;
    ts: number;
  }

  let open = $state(false);
  let peerId = $state("");
  let peerName = $state("");
  let messages = $state<ChatMsg[]>([]);
  let inputText = $state("");
  let lastTs = 0;
  let renderedIds = new Set<string>();
  let closedAt = 0;
  let pollTimer: ReturnType<typeof setInterval>;
  let notifyTimer: ReturnType<typeof setInterval>;
  let msgsEl: HTMLDivElement;

  export function openChat(id: string, name: string) {
    peerId = id;
    peerName = name;
    lastTs = 0;
    renderedIds.clear();
    messages = [];
    inputText = "";
    open = true;
    pollMessages();
    apiFetch("/api/chat/notify/ack", { method: "POST" }).catch(() => {});
  }

  function closeChat() {
    peerId = "";
    closedAt = Date.now();
    open = false;
    apiFetch("/api/chat/notify/ack", { method: "POST" }).catch(() => {});
  }

  async function sendMsg() {
    const text = inputText.trim();
    if (!text || !peerId) return;
    inputText = "";
    try {
      await apiPost("/api/chat/send", { peer: peerId, text });
      pollMessages();
    } catch {}
  }

  async function pollMessages() {
    if (!peerId) return;
    try {
      const res = await apiFetch("/api/chat/messages?peer=" + encodeURIComponent(peerId) + "&since=" + lastTs);
      if (!res.ok) return;
      const msgs: ChatMsg[] = await res.json();
      if (!msgs.length) return;
      let added = false;
      for (const m of msgs) {
        if (renderedIds.has(m.id)) continue;
        renderedIds.add(m.id);
        messages = [...messages, m];
        if (m.ts > lastTs) lastTs = m.ts;
        added = true;
      }
      if (added) {
        // Scroll to bottom after DOM update
        requestAnimationFrame(() => { if (msgsEl) msgsEl.scrollTop = msgsEl.scrollHeight; });
        apiFetch("/api/chat/notify/ack", { method: "POST" }).catch(() => {});
      }
    } catch {}
  }

  async function checkNotify() {
    if (Date.now() - closedAt < 3000) return;
    try {
      const res = await apiFetch("/api/chat/notify");
      if (!res.ok) return;
      const data = await res.json();
      if (data.peer && !open) {
        const dev = $devices.find(d => d.id === data.peer);
        openChat(data.peer, dev?.name || data.name || "Device");
      }
    } catch {}
  }

  async function copyText(text: string, el: HTMLElement) {
    try { await navigator.clipboard.writeText(text); } catch {}
    el.textContent = "Copied!";
    setTimeout(() => { el.textContent = "Copy"; }, 1200);
  }

  function handleKeydown(e: KeyboardEvent) {
    if (e.key === "Enter" && !e.shiftKey) { e.preventDefault(); sendMsg(); }
  }

  // Start polling on mount
  $effect(() => {
    pollTimer = setInterval(pollMessages, 800);
    notifyTimer = setInterval(checkNotify, 2000);
    const onFocus = () => checkNotify();
    window.addEventListener("focus", onFocus);
    document.addEventListener("visibilitychange", () => { if (!document.hidden) checkNotify(); });
    return () => {
      clearInterval(pollTimer);
      clearInterval(notifyTimer);
      window.removeEventListener("focus", onFocus);
    };
  });
</script>

<div class="chat-panel" class:open>
  <div class="chat-hdr">
    <div class="chat-hdr-left">
      <div class="chat-hdr-icon">
        <svg viewBox="0 0 24 24"><path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"/></svg>
      </div>
      <span class="chat-hdr-name">{peerName}</span>
    </div>
    <!-- svelte-ignore a11y_click_events_have_key_events -->
    <!-- svelte-ignore a11y_no_static_element_interactions -->
    <span class="chat-hdr-close" onclick={closeChat}>
      <svg viewBox="0 0 24 24"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
    </span>
  </div>
  <div class="chat-msgs" bind:this={msgsEl}>
    {#if messages.length === 0}
      <div class="chat-empty">
        <svg viewBox="0 0 24 24"><path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"/></svg>
        No messages yet
      </div>
    {:else}
      {#each messages as m (m.id)}
        <div class="chat-bubble-wrap {m.dir}">
          <div class="chat-bubble">{m.text}</div>
          <div class="chat-meta">
            <span class="chat-time">{fmtTime(m.ts)}</span>
            {#if m.dir === "recv"}
              <!-- svelte-ignore a11y_click_events_have_key_events -->
              <!-- svelte-ignore a11y_no_static_element_interactions -->
              <span class="chat-copy" onclick={(e) => copyText(m.text, e.currentTarget as HTMLElement)}>Copy</span>
            {/if}
          </div>
        </div>
      {/each}
    {/if}
  </div>
  <div class="chat-input">
    <textarea placeholder="Type a message…" rows="1" bind:value={inputText} onkeydown={handleKeydown}></textarea>
    <button class="chat-send" disabled={!inputText.trim()} onclick={sendMsg} title="Send">
      <svg viewBox="0 0 24 24"><line x1="22" y1="2" x2="11" y2="13"/><polygon points="22 2 15 22 11 13 2 9 22 2"/></svg>
    </button>
  </div>
</div>

<style>
  .chat-panel {
    margin-bottom: 10px; border: 1px solid var(--border); border-radius: 14px;
    background: var(--bg); overflow: hidden;
    max-height: 0; opacity: 0; transition: max-height .3s ease, opacity .2s ease;
  }
  .chat-panel.open { max-height: 500px; opacity: 1; }
  .chat-hdr {
    display: flex; align-items: center; justify-content: space-between;
    padding: 9px 12px; background: var(--panel);
    border-bottom: 1px solid var(--border);
    background-image: linear-gradient(90deg, rgba(79,140,255,.08), rgba(122,92,255,.08));
  }
  .chat-hdr-left { display: flex; align-items: center; gap: 8px; }
  .chat-hdr-icon {
    width: 22px; height: 22px; border-radius: 6px; display: grid; place-items: center;
    background: linear-gradient(135deg, var(--accent), var(--accent-2));
  }
  .chat-hdr-icon svg { width: 12px; height: 12px; stroke: #fff; fill: none; stroke-width: 2; stroke-linecap: round; stroke-linejoin: round; }
  .chat-hdr-name { font-weight: 600; font-size: 12px; }
  .chat-hdr-close {
    cursor: pointer; width: 20px; height: 20px; display: grid; place-items: center;
    border-radius: 5px; color: var(--muted); transition: .12s;
  }
  .chat-hdr-close:hover { background: rgba(248,113,113,.1); color: var(--err); }
  .chat-hdr-close svg { width: 12px; height: 12px; stroke: currentColor; fill: none; stroke-width: 2; stroke-linecap: round; }
  .chat-msgs {
    height: 230px; overflow-y: auto; padding: 12px 12px 8px;
    display: flex; flex-direction: column; gap: 6px; background: var(--bg);
  }
  .chat-msgs::-webkit-scrollbar { width: 4px; }
  .chat-msgs::-webkit-scrollbar-thumb { background: var(--border); border-radius: 4px; }
  .chat-empty {
    display: flex; flex-direction: column; align-items: center; justify-content: center;
    color: var(--muted); font-size: 11px; padding: 40px 0; gap: 8px;
  }
  .chat-empty svg { width: 28px; height: 28px; stroke: var(--border); fill: none; stroke-width: 1.2; stroke-linecap: round; stroke-linejoin: round; }
  .chat-bubble-wrap { display: flex; flex-direction: column; max-width: 82%; }
  .chat-bubble-wrap.sent { align-self: flex-end; align-items: flex-end; }
  .chat-bubble-wrap.recv { align-self: flex-start; align-items: flex-start; }
  .chat-bubble {
    padding: 7px 11px; border-radius: 14px; font-size: 12px;
    line-height: 1.45; word-break: break-word; white-space: pre-wrap;
  }
  .chat-bubble-wrap.sent .chat-bubble {
    background: linear-gradient(135deg, var(--accent), var(--accent-2));
    color: #fff; border-bottom-right-radius: 4px;
  }
  .chat-bubble-wrap.recv .chat-bubble {
    background: var(--panel-2); color: var(--text); border-bottom-left-radius: 4px;
  }
  .chat-meta { display: flex; align-items: center; gap: 6px; margin-top: 2px; }
  .chat-time { font-size: 9px; color: var(--muted); }
  .chat-copy {
    font-size: 9px; color: var(--muted); cursor: pointer; padding: 1px 5px;
    border: 1px solid transparent; border-radius: 4px; transition: .12s;
  }
  .chat-copy:hover { border-color: var(--border); color: var(--accent); }
  .chat-input {
    display: flex; align-items: flex-end; gap: 6px; padding: 8px 10px;
    border-top: 1px solid var(--border); background: var(--panel);
  }
  .chat-input textarea {
    flex: 1; min-height: 28px; max-height: 60px; resize: none;
    font: inherit; font-size: 12px; padding: 6px 9px;
    border-radius: 8px; border: 1px solid var(--border); background: var(--bg);
    color: var(--text); outline: none; transition: border-color .12s;
  }
  .chat-input textarea:focus { border-color: var(--accent); }
  .chat-input textarea::placeholder { color: var(--muted); }
  .chat-send {
    flex: none; width: 30px; height: 30px; padding: 0; display: grid; place-items: center;
    border-radius: 8px; border: none; cursor: pointer;
    background: linear-gradient(135deg, var(--accent), var(--accent-2));
    transition: opacity .12s;
  }
  .chat-send svg { width: 14px; height: 14px; stroke: #fff; fill: none; stroke-width: 2.2; stroke-linecap: round; stroke-linejoin: round; }
  .chat-send:disabled { opacity: .3; cursor: default; }
</style>
