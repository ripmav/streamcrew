// SPDX-License-Identifier: MIT
//
// The behavior test of the overlay runtime bundle. The CI has no
// headless browser, so the harness runs the checked-in bundle in a
// node vm context with a stubbed DOM, a virtual clock and a fake
// WebSocket, and drives the spec behavior through the protocol:
// positions (B9), animations (B10), the resync (B4), the timer
// format (B17) and the clip embed (B15). Every scenario boots a
// fresh context, so the scenarios do not share state.
//
// The Go test TestRuntimeBundleBehavior (internal/overlay) runs this
// file and fails when a scenario does not pass.

import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import vm from "node:vm";

const here = dirname(fileURLToPath(import.meta.url));
const bundle = readFileSync(join(here, "../../web/bundle.js"), "utf8");

// ---------- minimal DOM stub ----------

const VOID_TAGS = new Set(["img", "meta", "br", "input", "source", "link", "hr"]);

class ClassList {
  constructor(el) {
    this.set = new Set();
  }
  add(...cs) {
    for (const c of cs) this.set.add(c);
  }
  contains(c) {
    return this.set.has(c);
  }
}

class El {
  constructor(tag) {
    this.tagName = tag.toUpperCase();
    this.children = [];
    this.parent = null;
    this.style = {};
    this.dataset = {};
    this.attrs = {};
    this.listeners = {};
    this.classList = new ClassList(this);
    this.textContent = "";
  }
  get firstChild() {
    return this.children[0] ?? null;
  }
  get className() {
    return this.attrs.class ?? "";
  }
  set className(v) {
    this.attrs.class = v;
  }
  get src() {
    return this.attrs.src ?? "";
  }
  set src(v) {
    this.attrs.src = v;
  }
  get srcdoc() {
    return this.attrs.srcdoc ?? "";
  }
  set srcdoc(v) {
    this.attrs.srcdoc = v;
  }
  appendChild(c) {
    c.parent = this;
    this.children.push(c);
    return c;
  }
  insertBefore(c, ref) {
    c.parent = this;
    const i = ref ? this.children.indexOf(ref) : -1;
    if (i < 0) this.children.push(c);
    else this.children.splice(i, 0, c);
    return c;
  }
  remove() {
    if (!this.parent) return;
    const i = this.parent.children.indexOf(this);
    if (i >= 0) this.parent.children.splice(i, 1);
    this.parent = null;
  }
  setAttribute(k, v) {
    this.attrs[k] = String(v);
  }
  getAttribute(k) {
    return this.attrs[k] ?? null;
  }
  addEventListener(t, fn) {
    (this.listeners[t] ??= []).push(fn);
  }
  removeEventListener(t, fn) {
    const l = this.listeners[t];
    if (l) this.listeners[t] = l.filter((f) => f !== fn);
  }
  querySelector() {
    return null;
  }
  get outerHTML() {
    const name = this.tagName.toLowerCase();
    const attrs = Object.entries(this.attrs)
      .map(([k, v]) => ` ${k}="${v}"`)
      .join("");
    if (VOID_TAGS.has(name)) return `<${name}${attrs} />`;
    const inner = this.children.map((c) => c.outerHTML).join("") + this.textContent;
    return `<${name}${attrs}>${inner}</${name}>`;
  }
}

// parseHTML is the DOMParser stub: it parses the small subset of HTML
// the runtime rewrites (tags, double/single-quoted attributes, text)
// into an El tree under <html><head></head><body>…</body></html>.
function parseHTML(html) {
  const root = new El("html");
  const head = new El("head");
  const body = new El("body");
  root.children.push(head, body);
  const stack = [body];
  const re = /<\s*(\/)?\s*([a-zA-Z][a-zA-Z0-9-]*)((?:\s+[^<>]*?)?)\s*(\/?)>|([^<]+)/g;
  let m;
  while ((m = re.exec(html))) {
    if (m[5] !== undefined) {
      const last = stack[stack.length - 1];
      last.textContent += m[5];
      continue;
    }
    const [, close, tag, attrStr, selfClose] = m;
    if (close) {
      stack.pop();
      continue;
    }
    const el = new El(tag.toLowerCase());
    const ar = /([a-zA-Z-]+)\s*=\s*("([^"]*)"|'([^']*)')/g;
    let am;
    while ((am = ar.exec(attrStr ?? ""))) el.attrs[am[1]] = am[3] ?? am[4];
    stack[stack.length - 1].children.push(el);
    if (!selfClose && !VOID_TAGS.has(tag.toLowerCase())) stack.push(el);
  }
  const all = (el, acc = []) => {
    for (const c of el.children) {
      if (c instanceof El) acc.push(c, ...all(c));
    }
    return acc;
  };
  return {
    documentElement: root,
    head,
    body,
    createElement: (t) => new El(t.toLowerCase()),
    querySelectorAll: (sel) =>
      sel === "*" ? [head, ...all(head), ...all(body)] : [],
  };
}

// ---------- virtual clock ----------

function makeClock() {
  let now = 0;
  const timers = new Set();
  const later = (fn, ms, period = 0) => {
    const t = { at: now + ms, fn, period };
    timers.add(t);
    return t;
  };
  return {
    now: () => now,
    setTimeout: (fn, ms) => later(fn, ms ?? 0),
    setInterval: (fn, ms) => later(fn, ms ?? 0, ms),
    clearTimeout: (t) => timers.delete(t),
    clearInterval: (t) => timers.delete(t),
    // tick advances the clock and fires every timer that comes due.
    tick(ms) {
      const end = now + ms;
      for (;;) {
        let next = null;
        for (const t of timers)
          if (t.at <= end && (!next || t.at < next.at)) next = t;
        if (!next) break;
        now = next.at;
        timers.delete(next);
        next.fn();
        if (next.period) timers.add({ at: now + next.period, fn: next.fn, period: next.period });
      }
      now = end;
    },
    // renderOnce runs the registered per-second interval without
    // moving the clock: the per-second renders at the current time.
    renderOnce() {
      for (const t of [...timers]) if (t.period) t.fn();
    },
  };
}

// ---------- fake WebSocket ----------

class FakeWS {
  static OPEN = 1;
  constructor(url) {
    this.url = url;
    this.readyState = 0;
    this.sent = [];
    FakeWS.current = this;
  }
  send(d) {
    this.sent.push(JSON.parse(d));
  }
  close() {
    this.readyState = 3;
  }
  // the harness drives the socket:
  _open() {
    this.readyState = 1;
    this.onopen?.();
  }
  _message(obj) {
    this.onmessage?.({ data: JSON.stringify(obj) });
  }
  _close() {
    this.readyState = 3;
    this.onclose?.();
  }
}

// ---------- boot ----------

function boot({ search = "" } = {}) {
  const clock = makeClock();
  const created = [];
  const document = {
    head: new El("head"),
    body: new El("body"),
    createElement: (t) => {
      const el = new El(t.toLowerCase());
      created.push(el);
      return el;
    },
    querySelector: () => null,
  };
  const ctx = vm.createContext({
    document,
    location: {
      protocol: "http:",
      host: "127.0.0.1:8742",
      hostname: "127.0.0.1",
      origin: "http://127.0.0.1:8742",
      search,
    },
    performance: { now: () => clock.now() },
    Date: class extends Date {
      static now() {
        return clock.now();
      }
    },
    URLSearchParams,
    WebSocket: FakeWS,
    setTimeout: (fn, ms) => clock.setTimeout(fn, ms),
    setInterval: (fn, ms) => clock.setInterval(fn, ms),
    clearTimeout: (t) => clock.clearTimeout(t),
    clearInterval: (t) => clock.clearInterval(t),
    DOMParser: class {
      parseFromString(html) {
        return parseHTML(html);
      }
    },
    console: { log() {} },
  });
  ctx.window = ctx;
  ctx.__OVERLAY__ = { id: "default", name: "Default" };
  vm.runInContext(bundle, ctx);
  return {
    clock,
    document,
    // every element the runtime created, also the ones still pending
    // their batched start:
    created,
    ws: () => FakeWS.current,
    // flush drains the batched item starts (B8): the first item shows
    // 300 ms after the virtual clock starts, the next 300 ms after
    // that.
    flush: (ms = 300) => clock.tick(ms),
    // the protocol, from the server side:
    hello(time = clock.now()) {
      FakeWS.current._message({
        type: "hello",
        data: { version: "test", endpoint: "default", name: "Default", time },
      });
    },
    widgets(widgets) {
      FakeWS.current._message({ type: "state.widgets", data: { widgets } });
    },
    show(item) {
      FakeWS.current._message({ type: "item.show", data: item });
    },
    remove(id) {
      FakeWS.current._message({ type: "item.remove", data: { id } });
    },
    // find a rendered body child by class or text:
    byClass(cls) {
      return document.body.children.find((c) => c.className.includes(cls));
    },
    byText(text) {
      return document.body.children.find(
        (c) =>
          c.textContent === text ||
          c.children.some((ch) => ch.textContent === text)
      );
    },
  };
}

// ---------- assertions ----------

const failures = [];
function eq(actual, expected, what) {
  if (actual !== expected)
    failures.push(`${what}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`);
}
function ok(cond, what) {
  if (!cond) failures.push(`${what}: expected true, got false`);
}

const scenarios = [];
function scenario(name, fn) {
  scenarios.push({ name, fn });
}

// 342-4: bottom positions sit at the bottom edge; the old code lifted
// them by their own height (translateY(-100%)).
scenario("342-4: bottom positions have no Y offset", (env) => {
  const ws = env.ws();
  ws._open();
  env.hello();
  env.show({ id: "b", kind: "text", position: { model: "simple", point: "bottom" }, layer: 1, content: { text: "bottom" } });
  env.show({ id: "bl", kind: "text", position: { model: "simple", point: "bottom-left" }, layer: 1, content: { text: "bottom-left" } });
  env.flush(600);
  const b = env.byText("bottom");
  eq(b.style.left, "50%", "bottom: left");
  eq(b.style.bottom, "0", "bottom: bottom");
  eq(b.style.translate, "-50% 0", "bottom: X centering via the translate property");
  eq(b.style.transform ?? "", "", "bottom: no transform");
  const bl = env.byText("bottom-left");
  eq(bl.style.left, "0", "bottom-left: left");
  eq(bl.style.bottom, "0", "bottom-left: bottom");
  eq(bl.style.translate, "", "bottom-left: no centering");
  eq(bl.style.transform ?? "", "", "bottom-left: no transform");
});

// 342-5: the show animations animate transform with the fill mode
// both; the positioning must therefore not sit in transform. A
// centered item with slide-left keeps its translate centering.
scenario("342-5: animations do not override the centering", (env) => {
  const ws = env.ws();
  ws._open();
  env.hello();
  env.show({
    id: "c",
    kind: "text",
    position: { model: "simple", point: "center" },
    layer: 1,
    animation: "slide-left",
    content: { text: "center" },
  });
  env.flush();
  const c = env.byText("center");
  eq(c.style.translate, "-50% -50%", "center: centering via the translate property");
  ok(c.style.animation.includes("sc-slide-left"), "center: the show animation runs");
  ok(c.style.animation.includes("both"), "center: fill mode both");
  eq(c.style.transform ?? "", "", "center: transform is the animations' property");
});

// 342-2: the batched start is registered synchronously (pending): a
// remove before the start cancels it, and a duplicate show before the
// start cannot render the item twice.
scenario("342-2: a remove before the batched start cancels it", (env) => {
  const ws = env.ws();
  ws._open();
  env.hello();
  env.show({ id: "a", kind: "text", position: { model: "simple", point: "top-left" }, layer: 1, content: { text: "first" } });
  env.clock.tick(200);
  env.show({ id: "b", kind: "text", position: { model: "simple", point: "top-right" }, layer: 1, content: { text: "second" } });
  env.show({ id: "b2", kind: "text", position: { model: "simple", point: "center-left" }, layer: 1, content: { text: "third" } });
  env.show({ id: "b2", kind: "text", position: { model: "simple", point: "center-left" }, layer: 1, content: { text: "third" } });
  ok(!env.byText("second"), "b: not shown before its start");
  env.remove("b");
  env.clock.tick(1000);
  ok(env.byText("first"), "a: shown");
  ok(!env.byText("second"), "b: the removed item never shows");
  const third = env.document.body.children.filter((c) =>
    c.children.some((ch) => ch.textContent === "third")
  );
  eq(third.length, 1, "b2: the duplicate show renders exactly once");
});

// 342-3: on a reconnect the server re-sends the full state (B4); what
// was removed during the disconnect must not stay in the runtime.
scenario("342-3: the resync removes items and widgets lost in the disconnect", (env) => {
  const ws = env.ws();
  ws._open();
  env.hello();
  env.widgets([
    { id: "w1", kind: "label", name: "gone", position: { model: "simple", point: "top-left" }, state: { text: "gone-widget" } },
    { id: "w2", kind: "label", name: "stays", position: { model: "simple", point: "top-right" }, state: { text: "stays-widget" } },
  ]);
  env.show({ id: "i1", kind: "text", position: { model: "simple", point: "bottom-left" }, layer: 1, content: { text: "gone-item" } });
  env.show({ id: "i2", kind: "text", position: { model: "simple", point: "bottom-right" }, layer: 1, content: { text: "stays-item" } });
  env.flush(1000);
  ok(env.byText("gone-widget"), "w1: shown before the disconnect");
  ok(env.byText("gone-item"), "i1: shown before the disconnect");

  // The connection drops; the server removed w1 and i1 in the
  // meantime. The reconnect re-sends the state without them.
  ws._close();
  env.clock.tick(1000); // the backoff reconnect
  const ws2 = env.ws();
  ws2._open(); // the runtime sends ready
  env.hello();
  env.widgets([
    { id: "w2", kind: "label", name: "stays", position: { model: "simple", point: "top-right" }, state: { text: "stays-widget" } },
  ]);
  env.show({ id: "i2", kind: "text", position: { model: "simple", point: "bottom-right" }, layer: 1, content: { text: "stays-item" } });
  env.clock.tick(20); // the resync sweep

  ok(!env.byText("gone-widget"), "w1: removed after the resync");
  ok(!env.byText("gone-item"), "i1: removed after the resync");
  ok(env.byText("stays-widget"), "w2: re-announced, still shown");
  ok(env.byText("stays-item"), "i2: re-announced, still shown");
});

// 344-2: the timer item counts the rest of the duration down (B17).
scenario("344-2: the timer item counts down", (env) => {
  const ws = env.ws();
  ws._open();
  env.hello();
  env.show({
    id: "t",
    kind: "timer",
    position: { model: "simple", point: "center" },
    layer: 1,
    content: { duration: "90s", format: "MM:SS" },
  });
  const span = env.created.find((c) => c.className.includes("sc-kind-timer")).children[0];
  env.clock.tick(1000);
  eq(span.textContent, "01:29", "t=1 s: shows the rest, not the total");
  env.clock.tick(29000);
  eq(span.textContent, "01:00", "t=30 s: the 90 s countdown after 30 s");
});

// 344-2: the cumulative (uppercase) tokens carry the value without
// modulo: 5400 s as MM:SS is 90:00, not 30:00.
scenario("344-2: the cumulative tokens are not modulo", (env) => {
  const ws = env.ws();
  ws._open();
  env.hello();
  env.show({
    id: "t",
    kind: "timer",
    position: { model: "simple", point: "center" },
    layer: 1,
    content: { duration: "5400s", format: "MM:SS" },
  });
  env.clock.renderOnce(); // the per-second render at the current time
  const span = env.created.find((c) => c.className.includes("sc-kind-timer")).children[0];
  eq(span.textContent, "90:00", "5400 s in MM:SS without modulo");
});

// 344-6: the clip embed carries the parent parameter Twitch requires,
// and the slug keeps dashes and underscores (B15).
scenario("344-6: the clip embed carries parent and the full slug", (env) => {
  const ws = env.ws();
  ws._open();
  env.hello();
  env.show({
    id: "cl",
    kind: "clip",
    position: { model: "simple", point: "center" },
    layer: 1,
    content: { url: "https://clips.twitch.tv/Foo-Bar_9" },
  });
  env.flush();
  const el = env.byClass("sc-kind-clip");
  const f = el.children.find((c) => c.tagName === "IFRAME");
  eq(f.src, "https://clips.twitch.tv/embed?clip=Foo-Bar_9&parent=127.0.0.1", "clip: embed with parent");
});

// 344-7: an URL that is no clip page (it can come from the chat as a
// template, e.g. as javascript:) renders no iframe.
scenario("344-7: a non-clip URL renders no iframe", (env) => {
  const ws = env.ws();
  ws._open();
  env.hello();
  env.show({
    id: "x",
    kind: "clip",
    position: { model: "simple", point: "center" },
    layer: 1,
    content: { url: "javascript:alert(1)" },
  });
  env.flush();
  const el = env.byClass("sc-kind-clip");
  eq(el.children.length, 0, "no iframe for a non-clip URL");
});

// ---------- run ----------

let passed = 0;
for (const s of scenarios) {
  const env = boot();
  const before = failures.length;
  try {
    s.fn(env);
  } catch (e) {
    failures.push(e.stack ?? String(e));
  }
  for (let i = before; i < failures.length; i++) failures[i] = `${s.name}: ${failures[i]}`;
  if (failures.length === before) {
    passed++;
    console.log(`ok   ${s.name}`);
  }
}

if (failures.length > 0) {
  for (const f of failures) console.error(`FAIL ${f}`);
  console.error(`harness: ${scenarios.length - passed}/${scenarios.length} scenarios failed`);
  process.exit(1);
}
console.log(`harness: all ${scenarios.length} scenarios passed`);
