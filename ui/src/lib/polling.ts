// ── Polling logic ───────────────────────────────────────────────────────────
// Centralized polling for devices, transfers, identity, and pairing status.

import { get } from "svelte/store";
import { apiFetch, apiJson, initToken } from "./api";
import { me, devices, transfers, pairedIds, type DeviceIdentity, type Peer, type Transfer } from "./state";

let lastDevicesJson = "";
const rates: Record<string, { sent: number; t: number }> = {};

export async function loadMe(): Promise<void> {
  try {
    await initToken();
    const data = await apiJson<DeviceIdentity>("/api/me");
    me.set(data);
  } catch {
    me.set(null);
  }
}

export async function pollDevices(): Promise<void> {
  try {
    const data = await apiJson<Peer[]>("/api/devices");
    const j = JSON.stringify(data);
    if (j === lastDevicesJson) return;
    lastDevicesJson = j;
    devices.set(data);
  } catch {}
}

export async function pollTransfers(): Promise<void> {
  try {
    const data = await apiJson<Transfer[]>("/api/transfers");
    const now = performance.now();
    for (const t of data) {
      const prev = rates[t.id];
      if (t.status === "sending" && prev && now > prev.t) {
        t.rate = (t.sent - prev.sent) / ((now - prev.t) / 1000);
      }
      rates[t.id] = { sent: t.sent, t: now };
    }
    transfers.set(data);
  } catch {}
}

export async function refreshPairedStatus(): Promise<void> {
  const devs = get(devices);
  const newPaired = new Set<string>();
  await Promise.all(devs.map(async d => {
    try {
      const r = await apiJson<{ paired: boolean }>("/api/pair/status?id=" + encodeURIComponent(d.id));
      if (r.paired) newPaired.add(d.id);
    } catch {}
  }));
  pairedIds.set(newPaired);
}

// Adaptive transfer polling: fast during active transfers, slow when idle.
let transferTimer: ReturnType<typeof setInterval>;
let currentInterval = 3000;

function scheduleTransferPoll(): void {
  const trs = get(transfers);
  const hasActive = trs.some(t => t.status === "sending" || t.status === "paused");
  const interval = hasActive ? 700 : 3000;
  if (interval !== currentInterval) {
    clearInterval(transferTimer);
    transferTimer = setInterval(async () => { await pollTransfers(); scheduleTransferPoll(); }, interval);
    currentInterval = interval;
  }
}

export async function startPolling(): Promise<() => void> {
  await loadMe();
  await pollDevices();
  pollTransfers();
  setTimeout(refreshPairedStatus, 2000);

  const meTimer = setInterval(loadMe, 4000);
  const devTimer = setInterval(pollDevices, 2000);
  transferTimer = setInterval(async () => { await pollTransfers(); scheduleTransferPoll(); }, 3000);
  const pairTimer = setInterval(refreshPairedStatus, 10000);

  return () => {
    clearInterval(meTimer);
    clearInterval(devTimer);
    clearInterval(transferTimer);
    clearInterval(pairTimer);
  };
}
