// SPDX-License-Identifier: MIT
//
// The overlay runtime: the stateless renderer the overlay server serves
// to OBS browser sources (ADR-0019, spec overlays.md). It connects to
// the endpoint's WebSocket, renders the state the server sends (items
// and widgets), and reports the end of a playback (B3-B14). Bundled by
// go generate with the pinned esbuild (Code-ADR-0016) into ../web.

/// <reference lib="dom" />

interface OverlayConfig {
  id: string;
  name: string;
}

declare global {
  interface Window {
    __OVERLAY__: OverlayConfig;
  }
}

// ---------- protocol (ADR-0019 section 3) ----------

interface Message {
  type: string;
  data: any;
}

interface Position {
  model: string;
  point?: string;
  x?: number;
  y?: number;
}

interface ItemData {
  id: string;
  kind: string;
  position: Position;
  layer: number;
  animation?: string;
  animationDuration?: number;
  duration?: number;
  content?: any;
}

interface WidgetData {
  id: string;
  kind: string;
  name: string;
  position: Position;
  state?: any;
}

const cfg: OverlayConfig = window.__OVERLAY__;
const send = (type: string, data?: any): void => {
  if (ws && ws.readyState === WebSocket.OPEN) {
    ws.send(JSON.stringify({ type, data: data ?? null }));
  }
};

// ---------- style ----------

const style = document.createElement("style");
style.textContent = `
  html, body { margin: 0; padding: 0; width: 100%; height: 100%; overflow: hidden; background: transparent; }
  .sc-item, .sc-widget { position: absolute; box-sizing: border-box; }
  .sc-item { font-family: system-ui, sans-serif; color: #fff; }
  .sc-item.sc-text { font-size: 2.2rem; font-weight: 600; white-space: pre-wrap; text-shadow: 0 2px 8px rgba(0,0,0,.8); }
  .sc-item.sc-text span { display: inline-block; }
  .sc-item.sc-image img, .sc-item.sc-video video { display: block; max-width: 100%; max-height: 100vh; }
  .sc-item.sc-html iframe { border: 0; display: block; }
  .sc-item.sc-placeholder { padding: .5rem 1rem; background: rgba(0,0,0,.6); border-radius: .5rem; font-size: 1rem; }
  .sc-widget { font-family: system-ui, sans-serif; color: #fff; background: rgba(0,0,0,.6); border-radius: .5rem; padding: .4rem .8rem; font-size: 1.1rem; }
  .sc-widget small { opacity: .7; }
  @keyframes sc-fade { from { opacity: 0; } to { opacity: 1; } }
  @keyframes sc-slide-left { from { transform: translateX(-40px); opacity: 0; } to { transform: translateX(0); opacity: 1; } }
  @keyframes sc-slide-right { from { transform: translateX(40px); opacity: 0; } to { transform: translateX(0); opacity: 1; } }
  @keyframes sc-slide-up { from { transform: translateY(40px); opacity: 0; } to { transform: translateY(0); opacity: 1; } }
  @keyframes sc-slide-down { from { transform: translateY(-40px); opacity: 0; } to { transform: translateY(0); opacity: 1; } }
  @keyframes sc-pop { from { transform: scale(.5); opacity: 0; } to { transform: scale(1); opacity: 1; } }
  @keyframes sc-fade-out { from { opacity: 1; } to { opacity: 0; } }
`;
document.head.appendChild(style);

// ---------- positioning (B9) ----------

// The nine named screen points of the model "simple": CSS for the
// element that places it at the point.
const SIMPLE: Record<string, string> = {
  "top-left": "left:0; top:0;",
  "top": "left:50%; top:0; transform:translateX(-50%);",
  "top-right": "right:0; top:0;",
  "center-left": "left:0; top:50%; transform:translateY(-50%);",
  "center": "left:50%; top:50%; transform:translate(-50%,-50%);",
  "center-right": "right:0; top:50%; transform:translateY(-50%);",
  "bottom-left": "left:0; bottom:0; transform:translateY(-100%);",
  "bottom": "left:50%; bottom:0; transform:translate(-50%,-100%);",
  "bottom-right": "right:0; bottom:0; transform:translateY(-100%);",
};

function place(el: HTMLElement, pos: Position, layer: number): void {
  el.style.zIndex = String(layer);
  const s = el.style;
  s.left = "auto";
  s.right = "auto";
  s.top = "auto";
  s.bottom = "auto";
  s.transform = "";
  if (pos.model === "simple") {
    for (const decl of (SIMPLE[pos.point ?? "center"] ?? SIMPLE.center).split(";")) {
      const i = decl.indexOf(":");
      if (i > 0) s[decl.slice(0, i).trim() as keyof CSSStyleDeclaration] = decl.slice(i + 1).trim();
    }
  } else if (pos.model === "percent") {
    s.left = `${pos.x ?? 0}%`;
    s.top = `${pos.y ?? 0}%`;
  } else {
    // pixels (and the random point the server rolls into percent)
    s.left = `${pos.x ?? 0}px`;
    s.top = `${pos.y ?? 0}px`;
  }
}

// ---------- animation (B10) ----------

const DEFAULT_ANIMATION_MS = 300;

// The show animation for a name; "none" and unknown names animate none.
function animationName(name?: string): { in: string; out: string } {
  switch (name) {
    case "fade":
      return { in: "sc-fade", out: "sc-fade-out" };
    case "slide-left":
      return { in: "sc-slide-left", out: "sc-fade-out" };
    case "slide-right":
      return { in: "sc-slide-right", out: "sc-fade-out" };
    case "slide-up":
      return { in: "sc-slide-up", out: "sc-fade-out" };
    case "slide-down":
      return { in: "sc-slide-down", out: "sc-fade-out" };
    case "pop":
      return { in: "sc-pop", out: "sc-fade-out" };
    default:
      return { in: "", out: "" };
  }
}

function showAnimation(el: HTMLElement, name: string | undefined, ms: number | undefined): void {
  const a = animationName(name);
  if (!a.in) return;
  el.style.animation = `${a.in} ${ms && ms > 0 ? ms : DEFAULT_ANIMATION_MS}ms ease-out both`;
}

// hide runs the out animation and resolves when it is done; the caller
// removes the element.
function hideAnimation(el: HTMLElement, name: string | undefined, ms: number | undefined): Promise<void> {
  const a = animationName(name);
  const dur = ms && ms > 0 ? ms : DEFAULT_ANIMATION_MS;
  if (!a.out) return Promise.resolve();
  return new Promise((resolve) => {
    el.style.animation = `${a.out} ${dur}ms ease-in both`;
    el.addEventListener("animationend", () => resolve(), { once: true });
    // The animation may not fire if the element is not rendered; the
    // removal must not wait forever.
    setTimeout(resolve, dur + 50);
  });
}

// ---------- state ----------

const items = new Map<string, { el: HTMLElement; data: ItemData }>();
const widgets = new Map<string, { el: HTMLElement; data: WidgetData }>();
let serverTimeOffset = 0; // serverTime - localTime (B4, B26)

// The batching of consecutive items (B8): an item shown within 500 ms
// after the previous one starts 300 ms after that one.
const BATCH_WINDOW_MS = 500;
const BATCH_STAGGER_MS = 300;
let lastItemStart = 0;

function itemStartAt(now: number): number {
  if (now - lastItemStart < BATCH_WINDOW_MS) {
    return Math.max(now, lastItemStart + BATCH_STAGGER_MS);
  }
  return now;
}

// ---------- item rendering (B11-B15) ----------

function renderContent(el: HTMLElement, data: ItemData): void {
  const c = data.content ?? {};
  const src = c.source ?? c.url ?? "";
  switch (data.kind) {
    case "text": {
      el.classList.add("sc-text");
      const span = document.createElement("span");
      span.textContent = c.text ?? "";
      if (c.maxWidth) span.style.maxWidth = `${c.maxWidth}px`;
      el.appendChild(span);
      break;
    }
    case "image": {
      el.classList.add("sc-image");
      const img = document.createElement("img");
      img.src = src;
      img.alt = "";
      if (c.width) img.style.width = `${c.width}px`;
      if (c.height) img.style.height = `${c.height}px`;
      // One dimension only: the browser scales proportionally (B13).
      el.appendChild(img);
      break;
    }
    case "video": {
      el.classList.add("sc-video");
      const v = document.createElement("video");
      v.src = src;
      v.autoplay = true;
      v.loop = Boolean(c.loop);
      if (c.startTime) v.currentTime = c.startTime;
      if (c.volume !== undefined) v.volume = Math.min(1, Math.max(0, c.volume / 100));
      // The end of the playback tells the server (B14).
      v.addEventListener("ended", () => send("item.ended", { id: data.id }));
      el.appendChild(v);
      break;
    }
    case "audio": {
      const a = document.createElement("audio");
      a.src = src;
      a.autoplay = true;
      if (c.volume !== undefined) a.volume = Math.min(1, Math.max(0, c.volume / 100));
      a.addEventListener("ended", () => send("item.ended", { id: data.id }));
      el.appendChild(a);
      break;
    }
    case "html": {
      el.classList.add("sc-html");
      // Sandboxed without scripts and without same origin; the HTML may
      // only reference relative /media/ paths and absolute https URLs
      // (B12). The srcdoc base is the page URL, so relative paths
      // resolve.
      const f = document.createElement("iframe");
      f.setAttribute("sandbox", "");
      f.srcdoc = c.html ?? "";
      if (c.width) f.style.width = `${c.width}px`;
      if (c.height) f.style.height = `${c.height}px`;
      el.appendChild(f);
      break;
    }
    case "youtube":
    case "clip":
    case "timer":
    case "emote_effect":
    default: {
      // Rendered from roadmap 7.2/7.5; the placeholder keeps the item
      // visible in the meantime.
      el.classList.add("sc-placeholder");
      el.textContent = data.kind;
      break;
    }
  }
}

function showItem(data: ItemData): void {
  if (items.has(data.id)) return;
  const el = document.createElement("div");
  el.className = "sc-item sc-kind-" + data.kind;
  renderContent(el, data);
  place(el, data.position, data.layer ?? 0);
  const now = performance.now();
  const startAt = itemStartAt(now);
  lastItemStart = startAt;
  const start = (): void => {
    document.body.appendChild(el);
    showAnimation(el, data.animation, data.animationDuration);
    items.set(data.id, { el, data });
  };
  if (startAt > now) setTimeout(start, startAt - now);
  else start();
}

function removeItem(id: string): void {
  const entry = items.get(id);
  if (!entry) return;
  items.delete(id);
  void hideAnimation(entry.el, entry.data.animation, entry.data.animationDuration).then(() => {
    entry.el.remove();
  });
}

// ---------- widgets (B4, B19, B21, B22) ----------

function renderWidget(el: HTMLElement, data: WidgetData): void {
  el.textContent = "";
  const label = document.createElement("span");
  label.textContent = data.name || data.id;
  el.appendChild(label);
  const kind = document.createElement("small");
  kind.textContent = ` (${data.kind})`;
  el.appendChild(kind);
}

function setWidget(data: WidgetData, visible: boolean): void {
  let entry = widgets.get(data.id);
  if (!entry) {
    const el = document.createElement("div");
    el.className = "sc-widget";
    entry = { el, data };
    widgets.set(data.id, entry);
    place(el, data.position, 0);
    renderWidget(el, data);
    document.body.appendChild(el);
  }
  entry.data = data;
  renderWidget(entry.el, data);
  entry.el.style.display = visible ? "" : "none";
}

// ---------- TTS (B41, provider browser) ----------

function speak(d: any): void {
  if (typeof speechSynthesis === "undefined" || !d || !d.text) return;
  const u = new SpeechSynthesisUtterance(d.text);
  if (d.voice) u.voice = speechSynthesis.getVoices().find((v) => v.name === d.voice) ?? null;
  if (d.volume !== undefined) u.volume = d.volume;
  if (d.rate !== undefined) u.rate = d.rate;
  if (d.pitch !== undefined) u.pitch = d.pitch;
  u.onend = () => send("tts.ended", null);
  u.onerror = () => send("tts.ended", null);
  speechSynthesis.cancel();
  speechSynthesis.speak(u);
}

// ---------- connection (B3, B4, B7) ----------

let ws: WebSocket | null = null;
// The reconnect backoff: 1 s doubling up to 30 s (B7).
const MIN_BACKOFF_MS = 1000;
const MAX_BACKOFF_MS = 30000;
let backoff = MIN_BACKOFF_MS;
let closedByUs = false;

function connect(): void {
  const proto = location.protocol === "https:" ? "wss:" : "ws:";
  const url = `${proto}//${location.host}/overlay/${encodeURIComponent(cfg.id)}/ws`;
  const socket = new WebSocket(url);
  ws = socket;
  socket.onopen = () => {
    backoff = MIN_BACKOFF_MS;
    send("ready", null);
  };
  socket.onmessage = (ev) => {
    let msg: Message;
    try {
      msg = JSON.parse(String(ev.data));
    } catch {
      return;
    }
    handle(msg);
  };
  socket.onclose = () => {
    if (ws !== socket) return;
    ws = null;
    if (!closedByUs) setTimeout(connect, backoff);
    backoff = Math.min(backoff * 2, MAX_BACKOFF_MS);
  };
  socket.onerror = () => socket.close();
}

function handle(msg: Message): void {
  const d = msg.data ?? {};
  switch (msg.type) {
    case "hello":
      serverTimeOffset = new Date(d.time ?? 0).getTime() - Date.now();
      break;
    case "state.widgets":
      for (const w of d.widgets ?? []) setWidget(w, true);
      break;
    case "item.show":
      showItem(d);
      break;
    case "item.remove":
      removeItem(d.id);
      break;
    case "widget.show":
      syncWidget(d.id, true);
      break;
    case "widget.hide":
      syncWidget(d.id, false);
      break;
    case "widget.update":
      syncWidget(d.id, undefined, d.state);
      break;
    case "tts.speak":
      speak(d);
      break;
    default:
      break;
  }
}

// syncWidget applies a widget message to the runtime state; visible
// undefined keeps the visibility, state undefined keeps the state.
function syncWidget(id: string, visible?: boolean, state?: any): void {
  const entry = widgets.get(id);
  if (!entry) return;
  if (state !== undefined) entry.data.state = state;
  if (visible !== undefined) entry.el.style.display = visible ? "" : "none";
  renderWidget(entry.el, entry.data);
}

connect();
