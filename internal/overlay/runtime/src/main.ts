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

// render is the re-render of an element that runs every second: the
// label kinds date and time (A5), the timer widget (B26) and the timer
// item (B17). It is removed with the element. stale is set on hello
// (resync, B4) until the server re-announces the entry.
type Entry = { el: HTMLElement; data: any; render?: () => void; stale?: boolean };

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
  .sc-item.sc-html iframe, .sc-item.sc-youtube iframe, .sc-item.sc-clip iframe { border: 0; display: block; }
  .sc-item.sc-timer { font-size: 2.6rem; font-weight: 700; font-variant-numeric: tabular-nums; text-shadow: 0 2px 8px rgba(0,0,0,.8); }
  .sc-item.sc-placeholder { padding: .5rem 1rem; background: rgba(0,0,0,.6); border-radius: .5rem; font-size: 1rem; }
  .sc-widget { font-family: system-ui, sans-serif; color: #fff; background: rgba(0,0,0,.6); border-radius: .5rem; padding: .4rem .8rem; font-size: 1.1rem; }
  .sc-widget small { opacity: .7; }
  .sc-widget.sc-label { background: transparent; padding: 0; font-size: 1.6rem; font-weight: 600; text-shadow: 0 2px 8px rgba(0,0,0,.8); }
  .sc-widget .sc-goal-bar { height: 8px; background: rgba(255,255,255,.25); border-radius: 4px; margin: .25rem 0; overflow: hidden; }
  .sc-widget .sc-goal-fill { height: 100%; background: #4caf50; }
  .sc-widget ul { list-style: none; margin: 0; padding: 0; }
  .sc-widget li { white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .sc-widget .sc-boss-bar { height: 10px; background: rgba(255,255,255,.25); border-radius: 5px; margin: .25rem 0; overflow: hidden; }
  .sc-widget .sc-boss-fill { height: 100%; background: #e53935; }
  .sc-widget.sc-disabled { display: none; }
  .sc-widget .sc-poll-winner { font-weight: 700; }
  .sc-widget .sc-wheel-spin { margin-top: .25rem; font-weight: 700; }
  .sc-widget .sc-wheel-landed { outline: 2px solid #fff; border-radius: .25rem; }
  .sc-item.sc-emote-effect { display: flex; flex-wrap: wrap; gap: 2px; }
  .sc-item.sc-emote-effect img, .sc-widget .sc-emote { display: inline-block; }
  .sc-item.sc-emote-effect.sc-effect-rain img, .sc-widget .sc-emote.rain { animation: sc-rain 4s linear infinite; }
  .sc-item.sc-emote-effect.sc-effect-burst img, .sc-widget .sc-emote.burst { animation: sc-burst .8s ease-out; }
  @keyframes sc-rain { from { transform: translateY(-30%); opacity: 0; } 10% { opacity: 1; } to { transform: translateY(120vh); } }
  @keyframes sc-burst { 0% { transform: scale(.2); opacity: 0; } 30% { transform: scale(1.3); opacity: 1; } 100% { transform: scale(1); } }
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
// element that places it at the point. The centering uses the CSS
// property translate (not transform), so the show animations, which
// animate transform with the fill mode both, cannot override it.
const SIMPLE: Record<string, string> = {
  "top-left": "left:0; top:0;",
  "top": "left:50%; top:0; translate:-50% 0;",
  "top-right": "right:0; top:0;",
  "center-left": "left:0; top:50%; translate:0 -50%;",
  "center": "left:50%; top:50%; translate:-50% -50%;",
  "center-right": "right:0; top:50%; translate:0 -50%;",
  "bottom-left": "left:0; bottom:0;",
  "bottom": "left:50%; bottom:0; translate:-50% 0;",
  "bottom-right": "right:0; bottom:0;",
};

function place(el: HTMLElement, pos: Position, layer: number): void {
  el.style.zIndex = String(layer);
  const s = el.style;
  s.left = "auto";
  s.right = "auto";
  s.top = "auto";
  s.bottom = "auto";
  // transform stays the animations' property; positioning is translate.
  s.translate = "";
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

const items = new Map<string, Entry>();
const widgets = new Map<string, Entry>();
// During a resync (B4) the locally held items and widgets are stale
// until the server re-announces them.
let resyncPending = false;
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

// serverNow is the local clock corrected to the server time of hello
// (B4, B26); the clocks of the runtime run from it.
function serverNow(): number {
  return Date.now() + serverTimeOffset;
}

// The per-second re-renders of the clocks (A5, B17, B26). Each render
// runs in its own try/catch: one render that throws (e.g. an unknown
// time zone) must not stop the others.
const perSecond = new Set<() => void>();
setInterval(() => {
  for (const render of perSecond) {
    try {
      render();
    } catch {
      // keep the other renders running
    }
  }
}, 1000);

// ---------- durations and formats (B17) ----------

// parseGoDuration parses a Go duration text, e.g. "1m30s" or "90s"
// (Code-ADR-0009), into milliseconds.
function parseGoDuration(text: string): number {
  const mult: Record<string, number> = { ns: 1e-6, us: 1e-3, ms: 1, s: 1000, m: 60000, h: 3600000 };
  let total = 0;
  const re = /(\d+(?:\.\d+)?)(ns|us|µs|ms|s|m|h)/g;
  let m: RegExpExecArray | null;
  while ((m = re.exec(text))) {
    total += parseFloat(m[1]) * mult[m[2]];
  }
  return total;
}

// formatTimer renders a timer into the format (B17): the tokens DD, HH,
// MM and SS carry the cumulative value, hh, mm and ss the rest, each
// padded to two digits. A count-down counts the rest of the duration, a
// count-up the elapsed time; for a count-up both are the same.
function formatTimer(format: string, totalMs: number, restMs: number): string {
  const parts = (ms: number): { d: number; h: number; m: number; s: number } => {
    const t = Math.max(0, Math.floor(ms / 1000));
    return { d: Math.floor(t / 86400), h: Math.floor((t % 86400) / 3600), m: Math.floor((t % 3600) / 60), s: t % 60 };
  };
  const cum = parts(totalMs);
  const rest = parts(restMs);
  const pad = (n: number): string => String(n).padStart(2, "0");
  return format
    .replace("DD", pad(cum.d))
    .replace("HH", pad(cum.h))
    .replace("MM", pad(cum.m))
    .replace("SS", pad(cum.s))
    .replace("hh", pad(rest.h))
    .replace("mm", pad(rest.m))
    .replace("ss", pad(rest.s));
}

// ---------- item rendering (B11-B17) ----------

// youtubeId is the video ID of a YouTube URL, for the embed frame (B15).
function youtubeId(url: string): string {
  const m = url.match(/(?:youtu\.be\/|[?&]v=|\/embed\/|\/shorts\/)([A-Za-z0-9_-]{11})/);
  return m ? m[1] : "";
}

// clipEmbed is the embed URL of a Twitch clip page (B15).
function clipEmbed(url: string): string {
  const m = url.match(/clips\.twitch\.tv\/([A-Za-z0-9]+)/);
  return m ? `https://clips.twitch.tv/embed?clip=${m[1]}` : url;
}

function renderContent(el: HTMLElement, entry: Entry): void {
  const data = entry.data as ItemData;
  const c = data.content ?? {};
  const src = c.src ?? c.source ?? c.url ?? "";
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
      if (c.start) v.currentTime = c.start;
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
    case "youtube": {
      // The embed frame in the browser source (B15).
      // shortcut: the volume of the embed is not applied; it needs the
      // YouTube IFrame API, which the runtime does not load yet.
      el.classList.add("sc-youtube");
      const f = document.createElement("iframe");
      const id = youtubeId(c.url ?? "");
      f.src = `https://www.youtube.com/embed/${id}?autoplay=1${c.start ? `&start=${c.start}` : ""}`;
      f.allow = "autoplay";
      if (c.width) f.style.width = `${c.width}px`;
      if (c.height) f.style.height = `${c.height}px`;
      el.appendChild(f);
      break;
    }
    case "clip": {
      // The clip page in a frame (B15).
      el.classList.add("sc-clip");
      const f = document.createElement("iframe");
      f.src = clipEmbed(c.url ?? "");
      f.allow = "autoplay";
      if (c.width) f.style.width = `${c.width}px`;
      if (c.height) f.style.height = `${c.height}px`;
      el.appendChild(f);
      break;
    }
    case "timer": {
      // The item counts the duration down (default) or up and shows the
      // format (B17); a count-down tells the server the end (B14).
      el.classList.add("sc-timer");
      const span = document.createElement("span");
      el.appendChild(span);
      const totalMs = parseGoDuration(c.duration ?? "");
      const startAt = serverNow();
      const countUp = c.direction === "up";
      let ended = false;
      entry.render = () => {
        const now = serverNow();
        if (countUp) {
          const elapsed = now - startAt;
          span.textContent = formatTimer(c.format ?? "MM:SS", elapsed, elapsed);
        } else {
          const rest = startAt + totalMs - now;
          span.textContent = formatTimer(c.format ?? "MM:SS", totalMs, rest);
          if (rest <= 0 && !ended) {
            ended = true;
            perSecond.delete(entry.render!);
            entry.render = undefined;
            send("item.ended", { id: data.id });
          }
        }
      };
      perSecond.add(entry.render);
      break;
    }
    case "emote_effect": {
      // The emote effect shows the emotes with the effect (B35); an
      // empty image is a name the core kept as text, e.g. an emoji.
      // shortcut: rain, burst and random share the layout; the effects
      // differ only by the CSS class.
      el.classList.add("sc-emote-effect", "sc-effect-" + (c.effect ?? "random"));
      const size = Number(c.width ?? 64);
      for (const e of c.emotes ?? []) {
        for (let i = 0; i < Number(e.shown ?? 1); i++) {
          if (e.image) {
            const img = document.createElement("img");
            img.src = e.image;
            img.alt = e.code;
            if (size > 0) img.style.width = `${size}px`;
            el.appendChild(img);
          } else {
            const span = document.createElement("span");
            span.textContent = e.code;
            if (size > 0) span.style.fontSize = `${size}px`;
            el.appendChild(span);
          }
        }
      }
      break;
    }
    default: {
      // The kinds without a renderer yet; the placeholder keeps the item
      // visible.
      el.classList.add("sc-placeholder");
      el.textContent = data.kind;
      break;
    }
  }
}

// The batched item start is delayed (B8). The ID is registered
// synchronously in pending, so that a remove before the start cancels
// the start, and a second show before the start cannot render the item
// twice (B11).
const pending = new Map<string, { entry: Entry; timer: ReturnType<typeof setTimeout> }>();

function showItem(data: ItemData): void {
  const existing = items.get(data.id);
  if (existing) {
    existing.stale = false;
    return;
  }
  const pnd = pending.get(data.id);
  if (pnd) {
    pnd.entry.stale = false;
    return;
  }
  const el = document.createElement("div");
  el.className = "sc-item sc-kind-" + data.kind;
  const entry: Entry = { el, data };
  renderContent(el, entry);
  place(el, data.position, data.layer ?? 0);
  const now = performance.now();
  const startAt = itemStartAt(now);
  lastItemStart = startAt;
  const start = (): void => {
    pending.delete(data.id);
    document.body.appendChild(el);
    showAnimation(el, data.animation, data.animationDuration);
    items.set(data.id, entry);
  };
  if (startAt > now) pending.set(data.id, { entry, timer: setTimeout(start, startAt - now) });
  else start();
}

function removeItem(id: string): void {
  const p = pending.get(id);
  if (p) {
    pending.delete(id);
    clearTimeout(p.timer);
    return;
  }
  const entry = items.get(id);
  if (!entry) return;
  items.delete(id);
  if (entry.render) perSecond.delete(entry.render);
  void hideAnimation(entry.el, entry.data.animation, entry.data.animationDuration).then(() => {
    entry.el.remove();
  });
}

// ---------- widgets (B4, B19, B21, B22, B24-B28) ----------

// clock renders the text of the label kinds date and time (B24, A5) in
// the locale and the time zone of the profile, per second.
function renderClock(el: HTMLElement, st: any, entry: Entry): void {
  const span = document.createElement("span");
  el.appendChild(span);
  const locale = st.locale ?? "en-US";
  const options: Intl.DateTimeFormatOptions =
    st.kind === "date" ? { dateStyle: "long" } : { timeStyle: "medium" };
  if (st.timeZone) options.timeZone = st.timeZone;
  entry.render = () => {
    span.textContent = new Date(serverNow()).toLocaleString(locale, options);
  };
  perSecond.add(entry.render);
}

function renderWidget(el: HTMLElement, entry: Entry): void {
  const data = entry.data as WidgetData;
  el.textContent = "";
  const name = data.name || data.id;
  const st = data.state ?? {};
  switch (data.kind) {
    case "label": {
      // The label shows its text; the kinds date and time the runtime
      // renders per second in the locale of the profile (B24, A5).
      el.classList.add("sc-label");
      if (st.kind === "date" || st.kind === "time") {
        renderClock(el, st, entry);
      } else {
        el.textContent = st.text ?? "";
      }
      break;
    }
    case "goal": {
      // The goal shows the reached amount of the total and its segments
      // (B25).
      const total = Number(st.total ?? 0);
      const current = Number(st.current ?? 0);
      const title = document.createElement("div");
      title.textContent = `${name}: ${current}/${total}`;
      el.appendChild(title);
      const bar = document.createElement("div");
      bar.className = "sc-goal-bar";
      const fill = document.createElement("div");
      fill.className = "sc-goal-fill";
      fill.style.width = total > 0 ? `${Math.min(100, (current / total) * 100)}%` : "0%";
      bar.appendChild(fill);
      el.appendChild(bar);
      const segs = document.createElement("small");
      segs.textContent = (st.segments ?? []).map((s: any) => `${s.name} (${s.amount})`).join(" · ");
      el.appendChild(segs);
      break;
    }
    case "timer": {
      // The timer widget counts the set duration down, from the server
      // time of hello (B26); paused it shows the remaining time.
      const span = document.createElement("span");
      span.style.fontVariantNumeric = "tabular-nums";
      el.appendChild(span);
      const format = st.format ?? "MM:SS";
      entry.render = () => {
        const now = serverNow();
        const rest = st.paused ? Number(st.pausedMs ?? 0) : Number(st.deadline ?? 0) - now;
        const r = Math.max(0, rest);
        // The cumulative and the rest coincide while the timer runs;
        // while it is paused the timer has no set total to show.
        span.textContent = formatTimer(format, r, r);
      };
      perSecond.add(entry.render);
      break;
    }
    case "event_list": {
      // The event list shows the recent feed events and the dynamic
      // entries, newest first (B27).
      // shortcut: the time of an entry is not shown; the list is
      // dominated by the user and the text.
      const ul = document.createElement("ul");
      for (const e of st.entries ?? []) {
        const li = document.createElement("li");
        li.textContent = e.user ? `${e.user}: ${e.text ?? ""}` : e.text ?? "";
        ul.appendChild(li);
      }
      el.appendChild(ul);
      break;
    }
    case "chat": {
      // The chat widget shows the messages, newest last (B28).
      const ul = document.createElement("ul");
      for (const m of st.messages ?? []) {
        const li = document.createElement("li");
        li.textContent = m.user ? `${m.user}: ${m.text}` : m.text;
        ul.appendChild(li);
      }
      el.appendChild(ul);
      break;
    }
    case "persistent_timer": {
      // The persistent timer counts down permanently and is persisted
      // (B29); completed and disable_on_completion it is hidden.
      if (st.disabled) {
        el.classList.add("sc-disabled");
        break;
      }
      const span = document.createElement("span");
      span.style.fontVariantNumeric = "tabular-nums";
      el.appendChild(span);
      const format = st.format ?? "MM:SS";
      entry.render = () => {
        const now = serverNow();
        const rest = st.paused ? Number(st.pausedMs ?? 0) : Number(st.deadline ?? 0) - now;
        const r = Math.max(0, rest);
        span.textContent = st.completed ? "done" : formatTimer(format, r, r);
      };
      perSecond.add(entry.render);
      break;
    }
    case "stream_boss": {
      // The stream boss shows the boss and its health of the max (B30).
      const boss = document.createElement("div");
      boss.textContent = st.boss || "Boss";
      el.appendChild(boss);
      const max = Number(st.max_health ?? 0);
      const health = Number(st.health ?? 0);
      const bar = document.createElement("div");
      bar.className = "sc-boss-bar";
      const fill = document.createElement("div");
      fill.className = "sc-boss-fill";
      fill.style.width = max > 0 ? `${Math.max(0, Math.min(100, (health / max) * 100))}%` : "0%";
      bar.appendChild(fill);
      el.appendChild(bar);
      const hp = document.createElement("small");
      hp.textContent = `${health}/${max}`;
      el.appendChild(hp);
      break;
    }
    case "end_credits": {
      // The end credits show the collected sections (B31); playing the
      // runtime rolls them.
      // shortcut: the credits show the sections as a list; the rolling
      // animation is a simple fade of the whole widget.
      const sections = (st.sections ?? {}) as Record<string, any[]>;
      for (const [section, entries] of Object.entries(sections)) {
        if (!entries || entries.length === 0) continue;
        const div = document.createElement("div");
        const title = document.createElement("div");
        title.textContent = section;
        div.appendChild(title);
        const ul = document.createElement("ul");
        for (const e of entries) {
          const li = document.createElement("li");
          li.textContent = e.user ? e.user : e.text ?? "";
          ul.appendChild(li);
        }
        div.appendChild(ul);
        el.appendChild(div);
      }
      if (st.playing) el.classList.add("sc-credits-playing");
      break;
    }
    case "leaderboard": {
      // The leaderboard shows the ranked entries (B32).
      const ol = document.createElement("ol");
      for (const [i, e] of (st.entries ?? []).entries()) {
        const li = document.createElement("li");
        li.textContent = `${e.name}: ${e.value}`;
        ol.appendChild(li);
      }
      el.appendChild(ol);
      break;
    }
    case "wheel": {
      // The wheel shows the outcomes and the last spin (B33).
      const ul = document.createElement("ul");
      for (const o of st.outcomes ?? []) {
        const li = document.createElement("li");
        li.textContent = `${o.name} (${o.probability})`;
        ul.appendChild(li);
      }
      el.appendChild(ul);
      const spin = st.spin;
      if (spin) {
        const div = document.createElement("div");
        div.className = "sc-wheel-spin";
        div.textContent = spin.name;
        el.appendChild(div);
        // The spin highlights the outcome for the duration, then the
        // state update clears it.
        setTimeout(() => {
          const n = el.querySelector(".sc-wheel-spin");
          if (n) n.classList.add("sc-wheel-landed");
        }, Number(spin.duration ?? 4) * 1000);
      }
      break;
    }
    case "poll": {
      // The poll shows the active vote: the name and the outcomes with
      // their votes or points (B34).
      const kind = st.kind;
      if (st.name) {
        const title = document.createElement("div");
        title.textContent = st.name;
        el.appendChild(title);
      }
      const ul = document.createElement("ul");
      for (const o of st.outcomes ?? []) {
        const li = document.createElement("li");
        const value = kind === "prediction" ? o.points ?? 0 : o.votes ?? 0;
        li.textContent = `${o.name}: ${value}${o.color ? ` (${o.color})` : ""}`;
        if (st.winner && o.name === st.winner) li.classList.add("sc-poll-winner");
        ul.appendChild(li);
      }
      el.appendChild(ul);
      break;
    }
    case "persistent_emote_effect": {
      // The persistent emote effect shows the emotes of the triggered
      // combo (B35); rain, burst and random are shown as the emote image.
      // The effect is a one-shot push without a sync state, so the
      // runtime clears it after its duration; a newer push invalidates
      // the pending clear by token.
      // shortcut: the effect shows the emote image; the rain/burst/random
      // animation is a simple repeat of the image.
      if (st.image) {
        const count = Number(st.per_emote_shown ?? 1);
        for (let i = 0; i < count; i++) {
          const img = document.createElement("img");
          img.className = "sc-emote " + (st.effect ?? "random");
          img.src = st.image;
          img.alt = st.code ?? "";
          el.appendChild(img);
        }
        const token = Math.random().toString(36);
        el.dataset.scEmoteToken = token;
        setTimeout(() => {
          if (el.dataset.scEmoteToken === token) el.textContent = "";
        }, Number(st.duration_ms ?? 0));
      }
      break;
    }
    case "game_queue": {
      // The game queue shows the entries with their position (B36).
      const ol = document.createElement("ol");
      for (const e of st.entries ?? []) {
        const li = document.createElement("li");
        li.textContent = e.user;
        ol.appendChild(li);
      }
      el.appendChild(ol);
      break;
    }
    case "discord_voice": {
      // The discord voice lists the occupants of the channel (B37).
      const ul = document.createElement("ul");
      for (const o of st.occupants ?? []) {
        const li = document.createElement("li");
        li.textContent = o.name;
        ul.appendChild(li);
      }
      el.appendChild(ul);
      break;
    }
    default: {
      // The kinds without a state yet; the placeholder keeps the widget
      // visible.
      const label = document.createElement("span");
      label.textContent = name;
      el.appendChild(label);
      const kind = document.createElement("small");
      kind.textContent = ` (${data.kind})`;
      el.appendChild(kind);
      break;
    }
  }
}

function setWidget(data: WidgetData, visible: boolean): void {
  let entry = widgets.get(data.id);
  if (entry) entry.stale = false;
  if (!entry) {
    const el = document.createElement("div");
    el.className = "sc-widget";
    entry = { el, data };
    widgets.set(data.id, entry);
    place(el, data.position, 0);
    document.body.appendChild(el);
  }
  entry.data = data;
  if (entry.render) {
    perSecond.delete(entry.render);
    entry.render = undefined;
  }
  renderWidget(entry.el, entry);
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
  // The seq echoes the speech, so the tts.ended of a canceled speech
  // cannot end the next one (B49).
  u.onend = () => send("tts.ended", { seq: d.seq });
  u.onerror = () => send("tts.ended", { seq: d.seq });
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

// withToken appends the token the page was loaded with (B5): the
// WebSocket and the media URLs need the same query parameter.
function withToken(url: string): string {
  const token = new URLSearchParams(location.search).get("token");
  if (!token) return url;
  return url + (url.includes("?") ? "&" : "?") + "token=" + encodeURIComponent(token);
}

function connect(): void {
  const proto = location.protocol === "https:" ? "wss:" : "ws:";
  const url = withToken(`${proto}//${location.host}/overlay/${encodeURIComponent(cfg.id)}/ws`);
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
      beginResync();
      break;
    case "state.widgets":
      for (const w of d.widgets ?? []) setWidget(w, true);
      finishResync();
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

// beginResync marks everything the runtime holds as stale (B4): after
// a disconnect the server re-sends the full state — hello,
// state.widgets, then the active items as item.show. What is not
// re-announced did not survive the disconnect and is removed.
function beginResync(): void {
  resyncPending = true;
  for (const entry of items.values()) entry.stale = true;
  for (const p of pending.values()) p.entry.stale = true;
  for (const entry of widgets.values()) entry.stale = true;
}

// finishResync schedules the removal of the stale entries. The sweep
// runs in a later task, so every item.show of the sync batch has
// already marked its entry fresh.
function finishResync(): void {
  setTimeout(() => {
    if (!resyncPending) return;
    resyncPending = false;
    for (const [id, p] of pending)
      if (p.entry.stale) {
        pending.delete(id);
        clearTimeout(p.timer);
      }
    for (const [id, entry] of items)
      if (entry.stale) {
        items.delete(id);
        drop(entry);
      }
    for (const [id, entry] of widgets)
      if (entry.stale) {
        widgets.delete(id);
        drop(entry);
      }
  }, 0);
}

// drop removes an entry without the out animation: it is gone from
// the server state, so there is nothing to fade out.
function drop(entry: Entry): void {
  if (entry.render) perSecond.delete(entry.render);
  entry.el.remove();
}

// syncWidget applies a widget message to the runtime state; visible
// undefined keeps the visibility, state undefined keeps the state.
function syncWidget(id: string, visible?: boolean, state?: any): void {
  const entry = widgets.get(id);
  if (!entry) return;
  if (state !== undefined) entry.data.state = state;
  if (visible !== undefined) entry.el.style.display = visible ? "" : "none";
  if (entry.render) {
    perSecond.delete(entry.render);
    entry.render = undefined;
  }
  renderWidget(entry.el, entry);
}

connect();
