// ── Chat ──────────────────────────────────────────────────────────────────
// Per-device chat panel — opened via the 💬 button on each paired device.

let chatPeer = null;      // currently open chat peer ID
let chatPeerName = "";
let chatLastTs = 0;       // last message timestamp we rendered
let chatRenderedIds = new Set();

function openChat(peerId, peerName) {
  chatPeer = peerId;
  chatPeerName = peerName;
  chatLastTs = 0;
  chatRenderedIds.clear();
  $("#chatName").textContent = peerName;
  $("#chatMsgs").innerHTML = '<div class="chat-empty">No messages yet</div>';
  $("#chatPanel").classList.add("open");
  $("#chatInput").value = "";
  $("#chatSendBtn").disabled = true;
  pollChatMessages();
  // Ack notification if this peer triggered it
  apiFetch("/api/chat/notify/ack", { method: "POST" }).catch(() => {});
}

function closeChat() {
  chatPeer = null;
  $("#chatPanel").classList.remove("open");
}

$("#chatClose").onclick = closeChat;

// Send button + Enter key
$("#chatSendBtn").onclick = sendChatMsg;
$("#chatInput").addEventListener("input", () => {
  $("#chatSendBtn").disabled = !$("#chatInput").value.trim();
});
$("#chatInput").addEventListener("keydown", e => {
  if (e.key === "Enter" && !e.shiftKey) { e.preventDefault(); sendChatMsg(); }
});

async function sendChatMsg() {
  const text = $("#chatInput").value.trim();
  if (!text || !chatPeer) return;
  $("#chatInput").value = "";
  $("#chatSendBtn").disabled = true;
  try {
    await apiFetch("/api/chat/send", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ peer: chatPeer, text })
    });
    pollChatMessages();
  } catch { toast("Failed to send", true); }
}

function fmtTime(ts) {
  const d = new Date(ts);
  return d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}

function renderMsg(m) {
  const wrap = document.createElement("div");
  wrap.className = "chat-bubble-wrap " + m.dir;
  const bubble = document.createElement("div");
  bubble.className = "chat-bubble";
  bubble.textContent = m.text;
  wrap.appendChild(bubble);
  const meta = document.createElement("div");
  meta.className = "chat-meta";
  const time = document.createElement("span");
  time.className = "chat-time";
  time.textContent = fmtTime(m.ts);
  meta.appendChild(time);
  if (m.dir === "recv") {
    const cp = document.createElement("span");
    cp.className = "chat-copy";
    cp.textContent = "Copy";
    cp.onclick = async () => {
      try { await navigator.clipboard.writeText(m.text); } catch {}
      cp.textContent = "Copied!";
      setTimeout(() => { cp.textContent = "Copy"; }, 1200);
    };
    meta.appendChild(cp);
  }
  wrap.appendChild(meta);
  return wrap;
}

async function pollChatMessages() {
  if (!chatPeer) return;
  try {
    const res = await apiFetch("/api/chat/messages?peer=" + encodeURIComponent(chatPeer) + "&since=" + chatLastTs);
    if (!res.ok) return;
    const msgs = await res.json();
    if (!msgs.length) return;
    const container = $("#chatMsgs");
    const empty = container.querySelector(".chat-empty");
    if (empty) empty.remove();
    let added = false;
    for (const m of msgs) {
      if (chatRenderedIds.has(m.id)) continue;
      chatRenderedIds.add(m.id);
      container.appendChild(renderMsg(m));
      if (m.ts > chatLastTs) chatLastTs = m.ts;
      added = true;
    }
    if (added) container.scrollTop = container.scrollHeight;
  } catch {}
}
setInterval(pollChatMessages, 800);

// Auto-open chat when notification is clicked (window gets focus)
async function checkChatNotify() {
  try {
    const res = await apiFetch("/api/chat/notify");
    if (!res.ok) return;
    const data = await res.json();
    if (data.peer && !chatPeer) {
      // Find the device name
      const dev = state.devices.find(d => d.id === data.peer);
      const name = dev ? dev.name : data.name || "Device";
      openChat(data.peer, name);
    }
  } catch {}
}
setInterval(checkChatNotify, 2000);
