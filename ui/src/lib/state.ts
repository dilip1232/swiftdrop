// ── Reactive stores ─────────────────────────────────────────────────────────
import { writable, derived } from "svelte/store";

export interface DeviceIdentity {
  id: string;
  name: string;
  platform: string;
  ip?: string;
}

export interface Peer {
  id: string;
  name: string;
  platform: string;
  host: string;
  manual?: boolean;
}

export interface StagedFile {
  name: string;
  path: string;
  size: number;
  is_folder?: boolean;
  file_count?: number;
}

export interface Transfer {
  id: string;
  name: string;
  size: number;
  sent: number;
  peer: string;
  status: string;
  dir: string;
  err?: string;
  retryable?: boolean;
  rate?: number;
}

export const me = writable<DeviceIdentity | null>(null);
export const devices = writable<Peer[]>([]);
export const target = writable<string | null>(null);
export const staged = writable<StagedFile[]>([]);
export const transfers = writable<Transfer[]>([]);
export const pairedIds = writable<Set<string>>(new Set());

// Derived: the currently selected device object
export const targetDevice = derived(
  [devices, target],
  ([$devices, $target]) => $devices.find(d => d.id === $target) ?? null
);

// Derived: is the selected device paired?
export const targetPaired = derived(
  [targetDevice, pairedIds],
  ([$dev, $paired]) => $dev ? $paired.has($dev.id) : false
);

// Derived: can we send?
export const canSend = derived(
  [targetDevice, targetPaired, staged],
  ([$dev, $paired, $staged]) => !!$dev && $paired && $staged.length > 0
);
