'use strict';

/* ---------- helpers ---------- */
// The panels live in a dock, and a panel that is not on screen is taken out of the page. So a lookup
// falls back to the panels themselves: code that fills a hidden panel still finds its elements.
const PANE_EL = {};
function $(sel, root) {
  if (root) return root.querySelector(sel);
  const hit = document.querySelector(sel);
  if (hit) return hit;
  for (const el of Object.values(PANE_EL)) {
    if (el.isConnected) continue;
    const f = el.matches(sel) ? el : el.querySelector(sel);
    if (f) return f;
  }
  return null;
}
const $$ = (sel, root = document) => [...root.querySelectorAll(sel)];

function h(tag, attrs, ...kids) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs || {})) {
    if (v == null || v === false) continue;
    if (k === 'class') el.className = v;
    else if (k.startsWith('on')) el.addEventListener(k.slice(2), v);
    else el.setAttribute(k, v === true ? '' : v);
  }
  for (const kid of kids.flat(Infinity)) {
    if (kid == null || kid === false) continue;
    el.append(kid.nodeType ? kid : document.createTextNode(String(kid)));
  }
  return el;
}

// an icon from icons.svg (Lucide)
function icon(name) {
  const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
  svg.setAttribute('class', 'ic');
  svg.setAttribute('aria-hidden', 'true');
  const use = document.createElementNS('http://www.w3.org/2000/svg', 'use');
  use.setAttribute('href', `icons.svg#i-${name}`);
  svg.append(use);
  return svg;
}

async function api(path, opts = {}) {
  const init = { method: opts.method || 'GET', headers: {}, signal: opts.signal };
  if (opts.body instanceof FormData) init.body = opts.body;
  else if (opts.body !== undefined) {
    init.headers['Content-Type'] = 'application/json';
    init.body = JSON.stringify(opts.body);
  }
  const res = await fetch(path, init);
  let data = null;
  try { data = await res.json(); } catch { /* empty body */ }
  if (!res.ok) {
    const err = new Error((data && data.error) || `Error ${res.status}`);
    err.status = res.status;
    err.data = data;
    throw err;
  }
  return data;
}

let toastTimer;
function toast(msg) {
  const t = $('#toast');
  t.textContent = msg;
  t.hidden = false;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => { t.hidden = true; }, 6000);
}

// Same rules as vault.Slugify in Go, so links resolve to the same note.
function slugify(s) {
  let out = '', dash = false;
  for (const ch of s.trim().toLowerCase()) {
    const c = ch.codePointAt(0);
    if ((c >= 97 && c <= 122) || (c >= 48 && c <= 57) || c > 127) { out += ch; dash = false; }
    else if (!dash && out.length > 0) { out += '-'; dash = true; }
  }
  return out.replace(/-+$/, '');
}
const foldSlug = (s) => s.normalize('NFD').replace(/[\u0300-\u036f]/g, '');

// per-browser conveniences (open guide, depth, layout, theme); the app works the same without them
const store = {
  get(k, d) { try { const v = localStorage.getItem(`sh.${k}`); return v == null ? d : JSON.parse(v); } catch { return d; } },
  set(k, v) { try { if (v == null) localStorage.removeItem(`sh.${k}`); else localStorage.setItem(`sh.${k}`, JSON.stringify(v)); } catch { /* private mode */ } },
};

const STATUS = { new: 'Por descubrir', prepared: 'Guía lista', tested: 'Comprobado', explained: 'Explicado' };
const SECTION = { 'Guía': 'La guía', 'Conceptos base': 'Conceptos base', 'Dudas': 'Dudas', 'Prueba': 'Preguntas', 'Feynman': 'Explicación', 'Conexiones': 'Conexiones' };
const TYPE = { video: 'Video', web: 'Web', book: 'Libro', paper: 'Paper', interactive: 'Interactivo' };
const DEPTH = ['solo la guía', '1 nivel de conceptos', '2 niveles', '3 niveles', 'hasta lo básico'];
const cssVar = (n) => getComputedStyle(document.documentElement).getPropertyValue(n).trim();
const reducedMotion = () => matchMedia('(prefers-reduced-motion: reduce)').matches;
const smoothness = () => (reducedMotion() ? 'auto' : 'smooth');
const isPhone = () => matchMedia('(max-width: 880px)').matches;
// a change of view, animated when the browser can
const vt = (fn) => (document.startViewTransition && !reducedMotion() ? document.startViewTransition(fn) : fn());

// Each guide has a colour of its own (--l0 … --l7) that its concepts share. The colour comes from the
// line's key, probing forward on a clash, so it stays put as long as the others do not change.
const LINES = 8;
function assignLines(keys) {
  const taken = new Set(), out = new Map();
  for (const key of [...keys].sort()) {
    let h32 = 2166136261;
    for (const ch of key) h32 = Math.imul(h32 ^ ch.codePointAt(0), 16777619) >>> 0;
    let i = h32 % LINES;
    for (let tries = 0; tries < LINES && taken.has(i); tries++) i = (i + 1) % LINES;
    taken.add(i);
    out.set(key, i);
  }
  return out;
}
const lineColor = (group) => `var(--${S.lines.has(group) ? `l${S.lines.get(group)}` : 'l-none'})`;
const nodeOf = (slug) => ((S.graph && S.graph.nodes) || []).find((n) => n.id === slug);
// a note that exists and has been written (a pending concept is mapped, not written)
const writtenNode = (slug) => { const n = nodeOf(slug) || nodeOf(foldSlug(slug)); return n && n.kind !== 'pending' ? n : null; };
function groupTitle(key) {
  if (!key) return 'Sueltos';
  if (key.startsWith('g:')) return (nodeOf(key.slice(2)) || {}).label || key.slice(2);
  if (key.startsWith('c:')) return key.length > 2 ? `Conceptos de ${key.slice(2)}` : 'Conceptos sueltos';
  return (S.roadmaps.find((r) => r.slug === key) || {}).title || key;
}

/* ---------- markdown (+ math, wikilinks) ---------- */
DOMPurify.addHook('afterSanitizeAttributes', (node) => {
  if (node.tagName !== 'A') return;
  const href = node.getAttribute('href') || '';
  if (/^https?:\/\//i.test(href)) {
    node.setAttribute('target', '_blank');
    node.setAttribute('rel', 'noopener noreferrer');
  } else if (href.startsWith('/api/adjuntos/')) {
    node.setAttribute('download', ''); // files from the notes: served as downloads
  } else if (!href.startsWith('#note/') && !href.startsWith('#sim/')) {
    node.removeAttribute('href');
  }
});

function renderMd(src, { breaks = false } = {}) {
  const stash = [];
  const keep = (value) => { stash.push(value); return `@@S${stash.length - 1}@@`; };
  // 1. protect code, so a "$" inside it is never read as math
  let s = src
    .replace(/```[\s\S]*?```/g, (m) => keep({ raw: m }))
    .replace(/`[^`\n]+`/g, (m) => keep({ raw: m }));
  // 2. pull math out before Markdown can mangle "_" and "*"
  const math = (tex, display) => keep({ tex, display });
  s = s
    .replace(/\$\$([\s\S]+?)\$\$/g, (_, t) => math(t, true))
    .replace(/\\\[([\s\S]+?)\\\]/g, (_, t) => math(t, true))
    .replace(/\\\(([\s\S]+?)\\\)/g, (_, t) => math(t, false))
    .replace(/(?<![\\$\w])\$(?!\s)([^$\n]+?)(?<!\s)\$(?![\w$])/g, (_, t) => math(t, false));
  // 3. [[wikilinks]] and links to generated simulations
  s = s
    .replace(/\[\[([^\]|#]+)(?:\|([^\]]+))?\]\]/g, (_, slug, title) => `[${(title || slug).trim()}](#note/${slugify(slug)})`)
    .replace(/\]\(\.\.\/interactive\/([^)]+?)\.html\)/g, (_, slug) => `](#sim/${slug})`);
  // 4. give code back (still raw) and render
  s = s.replace(/@@S(\d+)@@/g, (m, i) => (stash[i].raw !== undefined ? stash[i].raw : m));
  const html = DOMPurify.sanitize(marked.parse(s, { gfm: true, breaks }));
  // 5. KaTeX output goes in after sanitizing; it is generated here from escaped source
  return html.replace(/@@S(\d+)@@/g, (m, i) => {
    const item = stash[i];
    if (!item || item.tex === undefined) return m;
    try {
      return katex.renderToString(item.tex.trim(), { displayMode: item.display, throwOnError: false, trust: false });
    } catch { return m; }
  });
}

function mdBlock(src, opts) {
  const d = h('div', { class: 'md' });
  d.innerHTML = renderMd(src, opts);
  enhanceCallouts(d);
  enhanceDiagrams(d);
  markPending(d);
  if (!(opts && opts.streaming)) Viz.enhance(d, opts && opts.viz);
  return d;
}

// Links to concepts that have no note yet are grey: one click explains them.
function markPending(root) {
  if (!S.graph) return;
  for (const a of $$('a[href^="#note/"]', root)) {
    const slug = decodeURIComponent(a.getAttribute('href').slice(6));
    if (!writtenNode(slug)) a.classList.add('pending');
  }
}

/* ---------- rich blocks inside Markdown: callouts and diagrams ---------- */
// Obsidian callout syntax ("> [!tip] Title"), so the same notes look right in both apps.
const CALLOUT = {
  info: ['info', 'Nota'], note: ['info', 'Nota'], abstract: ['info', 'Resumen'], summary: ['info', 'Resumen'],
  tip: ['tip', 'Intuición'], hint: ['tip', 'Pista'], important: ['tip', 'Importante'], success: ['tip', 'Listo'],
  example: ['example', 'Ejemplo'],
  warning: ['warn', 'Ojo'], caution: ['warn', 'Ojo'], danger: ['warn', 'Ojo'], failure: ['warn', 'Error'], bug: ['warn', 'Error'],
  question: ['question', 'Piénsalo'], todo: ['question', 'Piénsalo'], help: ['question', 'Piénsalo'],
  quote: ['quote', 'Cita'], cite: ['quote', 'Cita'],
};

function enhanceCallouts(root) {
  for (const bq of $$('blockquote', root)) {
    const p = bq.firstElementChild;
    const first = p && p.tagName === 'P' && p.firstChild;
    if (!first || first.nodeType !== Node.TEXT_NODE) continue;
    const m = /^\s*\[!([A-Za-z]+)\][+-]?[ \t]*([^\n]*)(?:\n|$)/.exec(first.nodeValue);
    const def = m && CALLOUT[m[1].toLowerCase()];
    if (!def) continue;
    first.nodeValue = first.nodeValue.slice(m[0].length);
    if (p.firstChild && p.firstChild.nodeName === 'BR') p.firstChild.remove(); // breaks:true turns the newline into <br>
    if (!p.textContent.trim() && !p.firstElementChild) p.remove();
    bq.classList.add('callout', `callout-${def[0]}`);
    bq.setAttribute('role', 'note');
    bq.prepend(h('div', { class: 'callout-title' }, m[2].trim() || def[1]));
  }
}

// Mermaid is large, so it loads the first time a note actually has a diagram.
let mermaidReady = null;
function loadMermaid() {
  mermaidReady ??= new Promise((resolve, reject) => {
    const s = document.createElement('script');
    s.src = 'vendor/mermaid.min.js';
    s.onload = () => {
      window.mermaid.initialize({
        startOnLoad: false, securityLevel: 'strict', theme: effectiveTheme() === 'dark' ? 'dark' : 'neutral',
        htmlLabels: false, flowchart: { htmlLabels: false }, fontFamily: getComputedStyle(document.body).fontFamily,
      });
      resolve(window.mermaid);
    };
    s.onerror = () => { mermaidReady = null; reject(new Error('mermaid')); };
    document.head.append(s);
  });
  return mermaidReady;
}

const diagramCache = new Map(); // a streaming bubble re-renders often; do not redraw what is already drawn
let diagramQueue = Promise.resolve();
let diagramSeq = 0;

function enhanceDiagrams(root) {
  for (const code of $$('pre > code.language-mermaid', root)) {
    const src = code.textContent;
    const body = h('div', { class: 'diagram-body', 'aria-busy': 'true' }, h('span', { class: 'hint' }, 'Dibujando el diagrama…'));
    code.parentElement.replaceWith(h('figure', { class: 'diagram', role: 'img', 'aria-label': 'Diagrama' }, body));
    drawDiagram(body, src);
  }
}

function drawDiagram(body, src) {
  const done = () => { body.removeAttribute('aria-busy'); };
  if (diagramCache.has(src)) { body.innerHTML = diagramCache.get(src); done(); return; }
  diagramQueue = diagramQueue.then(async () => {
    const id = `mmd${++diagramSeq}`;
    try {
      const { svg } = await (await loadMermaid()).render(id, src);
      diagramCache.set(src, svg);
      body.innerHTML = svg; // mermaid's strict mode has already sanitised the labels
    } catch {
      document.getElementById(`d${id}`)?.remove(); // mermaid leaves its error box in the page
      body.replaceChildren(h('p', { class: 'hint' }, 'No pude dibujar este diagrama. Este es su código:'), h('pre', {}, h('code', {}, src)));
    }
    done();
  });
}

// Drops emojis from older notes. Fenced code is kept as it is.
function stripEmoji(md) {
  return md.split(/(```[\s\S]*?```)/g).map((part, i) => (i % 2 ? part : part
    .replace(/[\p{Extended_Pictographic}\u{FE0F}\u{200D}\u{20E3}]/gu, '')
    .replace(/^([ \t]*(?:#+|>|[-*]|\d+\.)) {2,}(?=\S)/gm, '$1 ')
    .replace(/[ \t]+$/gm, ''))).join('');
}
function stripSection(body, heading) {
  const re = new RegExp(`(^|\\n)## ${heading}\\n[\\s\\S]*?(?=\\n## |$)`);
  return body.replace(re, '\n');
}
// the part of a note under "## heading", without the heading
function sectionOf(body, heading) {
  const m = new RegExp(`(?:^|\\n)## ${heading}\\s*\\n([\\s\\S]*?)(?=\\n## |$)`).exec(body || '');
  return m ? m[1].trim() : '';
}

/* ---------- state ---------- */
const S = {
  info: null, graph: null, roadmaps: [], lines: new Map(),
  guide: store.get('guide', ''), note: null, concept: null, lit: '',
  run: null, depth: store.get('depth', 1), cy: null, needsFit: false, filter: '', chats: [], dock: null,
};

/* ---------- theme ---------- */
const effectiveTheme = () => document.documentElement.dataset.theme || (matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light');
function applyTheme() {
  $('#btn-theme use').setAttribute('href', `icons.svg#i-${effectiveTheme() === 'dark' ? 'sun' : 'moon'}`);
  if (S.cy) S.cy.style(cyStyle());
  // widgets read their colours when drawn: draw what is open again
  if (S.note) renderReader(S.note);
  renderConcepts();
  renderPractice();
}
$('#btn-theme').addEventListener('click', () => {
  const next = effectiveTheme() === 'dark' ? 'light' : 'dark';
  vt(() => {
    document.documentElement.dataset.theme = next;
    store.set('theme', next);
    applyTheme();
  });
});
matchMedia('(prefers-color-scheme: dark)').addEventListener('change', () => { if (!document.documentElement.dataset.theme) applyTheme(); });

/* ---------- the dock: panels you can drag, split, stack and float ---------- */
const PANES = { chat: 'Chat', chats: 'Conversaciones', guide: 'Guía', concepts: 'Conceptos', map: 'Mapa', practice: 'Practicar', notes: 'Apuntes', vault: 'Vault', settings: 'Ajustes' };
const STUDY = ['guide', 'concepts', 'map', 'practice', 'notes']; // the study panel: closed until there is something to show
const NEAR_STUDY = [...STUDY, 'vault', 'settings']; // panels that open next to the study group

function createDock() {
  for (const el of $$('#panes > .pane')) PANE_EL[el.dataset.pane] = el;
  const DV = window['dockview-core'];
  const dock = DV.createDockview($('#dock'), {
    theme: DV.themeLightSpaced,
    createComponent: ({ name }) => ({ element: PANE_EL[name], init() {} }),
    createRightHeaderActionComponent: (group) => groupActions(group),
  });
  S.dock = dock;
  // Outside edit mode a tab can still be reordered or moved into another box, but the boxes stay
  // put: no splits, no new boxes, no moving a whole box, so a stray drag cannot rearrange the layout.
  const allowed = (e) => S.editingLayout || tabMove(e);
  dock.onWillShowOverlay((e) => { if (!allowed(e)) e.preventDefault(); });
  dock.onWillDrop((e) => { if (!allowed(e)) e.preventDefault(); });
  const saved = !isPhone() && store.get('dock', null);
  let restored = false;
  if (saved) {
    try { dock.fromJSON(saved); restored = !!dock.getPanel('chat'); } catch { restored = false; }
  }
  if (!restored) defaultLayout();
  // a saved layout carries the titles it was saved with: names come from the code, so a rename shows up
  for (const p of dock.panels) if (PANES[p.id] && p.title !== PANES[p.id]) p.api.setTitle(PANES[p.id]);
  // the study panel starts closed: it slides in when the app has something to show
  for (const id of NEAR_STUDY) dock.getPanel(id)?.api.close();
  let saveTimer;
  dock.onDidLayoutChange(() => {
    clearTimeout(saveTimer);
    saveTimer = setTimeout(() => { if (!isPhone()) store.set('dock', dock.toJSON()); }, 300);
    drawGroupActions();
  });
  dock.onDidMaximizedGroupChange?.(() => drawGroupActions());
  dock.onDidRemovePanel?.(() => syncPanelButton());
  dock.onDidAddPanel?.(() => syncPanelButton());
  dock.onDidActivePanelChange((p) => { if (p) onShown(p.id); });
}

// a tab dropped on a box's tab bar or the middle of its content: it joins that box. Taking the last
// tab out of a box would make the box disappear, which changes the layout, so that needs edit mode.
function tabMove(e) {
  const d = e.getData && e.getData();
  if (!d || !d.panelId || !e.group) return false; // a whole box being dragged, or an edge of the dock
  const onTabs = e.kind === 'tab' || e.kind === 'header_space';
  if (!onTabs && !(e.kind === 'content' && e.position === 'center')) return false;
  if (d.groupId === e.group.id) return true;
  const from = S.dock.groups.find((g) => g.id === d.groupId);
  return !!from && from.panels.length > 1;
}

// edit mode: the only time panels can move between groups, split or float; the layout is locked otherwise
function setLayoutEditing(on) {
  S.editingLayout = on;
  document.body.classList.toggle('layout-editing', on);
  $('#edit-bar').hidden = !on;
}
$('#edit-done').addEventListener('click', () => setLayoutEditing(false));
$('#edit-reset').addEventListener('click', () => { store.set('dock', null); defaultLayout(); toast('Diseño restablecido.'); });

function defaultLayout() {
  const dock = S.dock;
  dock.clear();
  dock.addPanel({ id: 'chat', component: 'chat', title: PANES.chat });
  if (isPhone()) {
    dock.addPanel({ id: 'chats', component: 'chats', title: PANES.chats, position: { referencePanel: 'chat', direction: 'within' }, inactive: true });
    dock.getPanel('chat').api.setActive();
    return;
  }
  dock.addPanel({ id: 'chats', component: 'chats', title: PANES.chats, position: { referencePanel: 'chat', direction: 'left' } });
  dock.getPanel('chat').api.setActive();
  // sizes apply once the dock has its own width
  requestAnimationFrame(() => { try { dock.getPanel('chats')?.group.api.setSize({ width: 250 }); } catch { /* a nicety */ } });
}

// room for the study panel: about half the dock, with the list of chats kept narrow
function sizeStudy(group) {
  const dock = S.dock;
  const W = $('#dock').clientWidth;
  try {
    const chats = dock.getPanel('chats');
    if (chats && chats.group !== group && !chats.group.panels.some((p) => p.id === 'chat') && chats.api.width > 300) chats.group.api.setSize({ width: 250 });
    group.api.setSize({ width: Math.round(Math.min(W - 520, Math.max(420, W * 0.48))) });
  } catch { /* sizes are a nicety */ }
}

const studyOpen = () => !!S.dock && STUDY.some((id) => S.dock.getPanel(id));

// shows a panel, adding it back if it was closed; the study panel slides in from the right
function show(id) {
  const dock = S.dock;
  if (!dock) return;
  let p = dock.getPanel(id);
  if (!p) {
    const near = NEAR_STUDY.includes(id) ? NEAR_STUDY.map((x) => dock.getPanel(x)).find(Boolean) : null;
    const chat = dock.getPanel('chat');
    const side = isPhone() ? 'within' : id === 'chats' ? 'left' : 'right';
    const position = near ? { referencePanel: near.id, direction: 'within' }
      : chat && id !== 'chat' ? { referencePanel: 'chat', direction: side } : undefined;
    p = dock.addPanel({ id, component: id, title: PANES[id], position });
    if (!near && side === 'right') {
      sizeStudy(p.group);
      slide(p.group.element, 'slide-in');
    }
  }
  p.api.setActive();
  onShown(id);
  syncPanelButton();
}
const isShown = (id) => { const p = S.dock && S.dock.getPanel(id); return !!(p && p.api.isVisible); };

function slide(el, cls) {
  if (!el || matchMedia('(prefers-reduced-motion: reduce)').matches) return;
  el.classList.remove('slide-in', 'slide-out');
  void el.offsetWidth; // restart the animation
  el.classList.add(cls);
  el.addEventListener('animationend', () => el.classList.remove(cls), { once: true });
}

// closes the panels of a group, sliding it out when it is the study panel
function closeGroup(group) {
  const panels = [...group.panels];
  const done = () => { for (const p of panels) p.api.close(); syncPanelButton(); };
  if (group.api.isMaximized()) group.api.exitMaximized();
  if (document.fullscreenElement) document.exitFullscreen().catch(() => {});
  if (matchMedia('(prefers-reduced-motion: reduce)').matches) { done(); return; }
  slide(group.element, 'slide-out');
  setTimeout(done, 170);
}
function closeStudy() {
  const groups = new Set(NEAR_STUDY.map((id) => S.dock.getPanel(id)).filter(Boolean).map((p) => p.group));
  for (const g of groups) if (!g.panels.some((p) => p.id === 'chat' || p.id === 'chats')) closeGroup(g);
  for (const id of NEAR_STUDY) { const p = S.dock.getPanel(id); if (p && p.group.panels.some((x) => x.id === 'chat' || x.id === 'chats')) p.api.close(); }
  syncPanelButton();
}
function syncPanelButton() {
  const open = studyOpen();
  const b = $('#btn-panel');
  b.setAttribute('aria-pressed', String(open));
  b.title = open ? 'Cerrar el panel de estudio' : 'Abrir el panel de estudio (guía, conceptos, mapa, apuntes)';
}
$('#btn-panel').addEventListener('click', () => (studyOpen() ? closeStudy() : show(S.lastStudy || 'guide')));

// the buttons at the right of every group's tabs: enlarge, full screen, close
const GROUP_ACTIONS = new Set();
function groupActions(group) {
  const element = h('div', { class: 'group-actions' });
  const r = {
    element,
    draw() {
      if (!S.dock || !group.api) return;
      let max = false;
      try { max = group.api.isMaximized(); } catch { return; }
      const full = !!document.fullscreenElement;
      const holdsChat = group.panels.some((p) => p.id === 'chat' || p.id === 'chats');
      if (group.panels.every((p) => p.id === 'chats')) { element.replaceChildren(); return; } // the list of chats needs none
      const btn = (name, title, onclick) => h('button', { class: 'icon-btn', type: 'button', title, 'aria-label': title, onclick }, icon(name));
      element.replaceChildren(...[
        full ? btn('minimize-2', 'Salir de pantalla completa', () => document.exitFullscreen().catch(() => {}))
          : btn('fullscreen', 'Pantalla completa', () => fullScreen(group)),
        full ? null : btn(max ? 'minimize-2' : 'maximize-2', max ? 'Volver al tamaño anterior' : 'Ampliar', () => {
          if (group.api.isMaximized()) group.api.exitMaximized(); else group.api.maximize();
          drawGroupActions();
        }),
        holdsChat || full ? null : btn('x', 'Cerrar el panel', () => closeGroup(group)),
      ].filter(Boolean));
    },
    init() { r.draw(); },
    dispose() { GROUP_ACTIONS.delete(r); },
  };
  GROUP_ACTIONS.add(r);
  return r;
}
function drawGroupActions() { for (const r of GROUP_ACTIONS) r.draw(); }

// full screen: the group fills the dock, and the dock fills the screen (popovers keep working)
async function fullScreen(group) {
  if (!group.api.isMaximized()) group.api.maximize();
  document.documentElement.classList.add('fs');
  try { await document.documentElement.requestFullscreen(); } catch { document.documentElement.classList.remove('fs'); }
  drawGroupActions();
}
document.addEventListener('fullscreenchange', () => {
  if (!document.fullscreenElement) {
    document.documentElement.classList.remove('fs');
    if (S.dock?.hasMaximizedGroup?.()) S.dock.exitMaximizedGroup();
  }
  drawGroupActions();
  fitMap();
});

// a panel came on screen: draw what it shows now
function onShown(id) {
  if (STUDY.includes(id)) S.lastStudy = id;
  if (id === 'map') fitMap();
  if (id === 'concepts') renderConcepts();
  if (id === 'practice') renderPractice();
  if (id === 'notes') renderNotes();
  if (id === 'vault') renderVault();
  if (id === 'settings') renderSettings();
}
$('#btn-settings').addEventListener('click', () => show('settings'));

/* ---------- Ajustes: which AI answers ---------- */
// A plain settings page: one dropdown for the AI, dropdowns for its models, two buttons.
const KIND = { cli: 'Con tu suscripción', api: 'Por API (clave propia)', local: 'En tu PC' };
const OTHER = '\u0000other'; // the "Otro…" option; cannot collide with a model name
const SET = { data: null, pick: '', form: {}, models: {} }; // form and models are kept per provider

async function renderSettings() {
  const root = PANE_EL.settings;
  if (!root) return;
  try {
    [SET.data] = await Promise.all([api('/api/settings'), checkUpdate()]);
  } catch (err) { root.replaceChildren(h('p', { class: 'hint', style: 'padding:24px' }, err.message)); return; }
  if (!SET.pick) SET.pick = SET.data.current.provider || 'claude-cli';
  SET.form = {};
  drawSettings();
}

// the values being edited for a provider: what is saved if it is the one in use, otherwise its defaults
function formOf(p) {
  if (!SET.form[p.id]) {
    const cur = SET.data.current;
    const same = p.id === (cur.provider || 'claude-cli');
    SET.form[p.id] = {
      model: same ? cur.model : '',
      quick: same ? cur.quickModel : '',
      url: same && cur.baseURL ? cur.baseURL : (p.baseURL || ''),
    };
  }
  return SET.form[p.id];
}

// Asks the provider for its models once; the page redraws when they arrive.
async function fetchModels(p) {
  const f = formOf(p);
  SET.models[p.id] = { loading: true, list: [] };
  try {
    const r = await api('/api/settings/models', { method: 'POST', body: { provider: p.id, baseURL: f.url } });
    SET.models[p.id] = { list: r.models || [] };
  } catch (err) {
    SET.models[p.id] = { list: [], error: err.message };
  }
  if (SET.pick === p.id && PANE_EL.settings.isConnected) drawSettings();
}

// A dropdown of models with an "Otro…" option that opens a text field for any other name.
function modelPicker({ value, list, loading, empty, label, onChange }) {
  const custom = h('input', { type: 'text', value: value || '', placeholder: 'Nombre exacto del modelo', 'aria-label': `${label}: otro` });
  const known = !value || list.includes(value);
  const select = h('select', { 'aria-label': label, disabled: loading || null },
    loading ? h('option', {}, 'Cargando modelos…') : [
      h('option', { value: '', selected: !value || null }, empty),
      list.map((m) => h('option', { value: m, selected: m === value || null }, m)),
      h('option', { value: OTHER, selected: !known || null }, 'Otro…'),
    ]);
  custom.hidden = loading || known;
  select.addEventListener('change', () => {
    const other = select.value === OTHER;
    custom.hidden = !other;
    if (other) { custom.value = ''; custom.focus(); }
    onChange(other ? '' : select.value);
  });
  custom.addEventListener('input', () => onChange(custom.value.trim()));
  return h('div', { class: 'set-model' }, select, custom);
}

function drawSettings() {
  const root = PANE_EL.settings;
  const { current: cur, providers: list, warning } = SET.data;
  const inUse = cur.provider || 'claude-cli';
  const sel = list.find((p) => p.id === SET.pick) || list[0];
  const f = formOf(sel);
  const canList = sel.id !== 'codex' && sel.id !== 'openai-compatible' || !!f.url;
  if (canList && !SET.models[sel.id] && (sel.ready || sel.kind === 'local' || sel.id === 'openai-compatible')) fetchModels(sel);
  const mods = SET.models[sel.id] || { list: [] };

  const picker = h('select', { 'aria-label': 'Proveedor de IA', onchange: (e) => { SET.pick = e.target.value; drawSettings(); } },
    Object.entries(KIND).map(([kind, title]) => h('optgroup', { label: title },
      list.filter((p) => p.kind === kind).map((p) => h('option', { value: p.id, selected: p.id === sel.id || null },
        p.name + (p.id === inUse ? ' (en uso)' : ''))))));
  const hasDefault = sel.kind === 'cli' || !!sel.defaultModel;
  const model = modelPicker({
    value: f.model, list: mods.list, loading: mods.loading, label: 'Modelo',
    empty: hasDefault ? 'Predeterminado' : 'Elige un modelo', onChange: (v) => { f.model = v; },
  });
  const quick = modelPicker({
    value: f.quick, list: mods.list, loading: mods.loading, label: 'Modelo rápido',
    empty: 'Igual que el modelo', onChange: (v) => { f.quick = v; },
  });
  const url = sel.editURL ? h('input', { type: 'url', value: f.url, placeholder: 'https://…/v1', 'aria-label': 'URL' }) : null;
  url?.addEventListener('change', () => { f.url = url.value.trim(); delete SET.models[sel.id]; drawSettings(); });
  const result = h('p', { class: 'set-result', role: 'status' });
  const body = () => ({ provider: sel.id, model: f.model, quickModel: f.quick, baseURL: url ? f.url : '' });
  const say = (cls, text) => { result.className = `set-result ${cls}`; result.textContent = text; };
  if (mods.error) say('bad', `No pude cargar los modelos: ${mods.error}`);

  // one line of plain status: ready or what is missing, and whether it searches the web
  const facts = [
    sel.ready ? 'Disponible' : sel.reason[0].toUpperCase() + sel.reason.slice(1),
    sel.search ? 'busca en la web' : 'sin búsqueda web (las guías salen sin recursos externos)',
    sel.experimental ? 'experimental' : '',
  ].filter(Boolean).join(' · ');

  const test = h('button', { class: 'btn small ghost', type: 'button', onclick: async (e) => {
    const b = e.currentTarget;
    b.disabled = true;
    say('', 'Probando…');
    try {
      const r = await api('/api/settings/test', { method: 'POST', body: body() });
      say('ok', `Funciona: respondió «${r.reply.slice(0, 60)}» en ${(r.ms / 1000).toFixed(1)} s.`);
    } catch (err) { say('bad', err.message); }
    b.disabled = false;
  } }, 'Probar');
  const save = h('button', { class: 'btn small primary', type: 'button', onclick: async (e) => {
    const b = e.currentTarget;
    b.disabled = true;
    try {
      const r = await api('/api/settings', { method: 'POST', body: body() });
      $('#provider-badge').textContent = r.provider;
      $('#banner').textContent = r.warning || '';
      $('#banner').hidden = !r.warning;
      toast(`Ahora responde ${sel.name}.`);
      await renderSettings();
    } catch (err) { say('bad', err.message); b.disabled = false; }
  } }, 'Guardar');

  const row = (label, control, extra) => h('div', { class: 'set-row' }, h('span', { class: 'set-label' }, label), h('div', { class: 'set-control' }, control, extra || null));
  const keyLine = sel.kind === 'api' && (sel.keyEnvs || []).length && !sel.ready
    ? h('p', { class: 'set-note' }, 'La clave va en una variable de entorno, no en la app: ', h('code', {}, `setx ${sel.keyEnvs[0]} "tu-clave"`), ' en PowerShell, y vuelve a abrir StudyHomie.')
    : null;

  root.replaceChildren(h('div', { class: 'settings' },
    h('h2', {}, 'Ajustes'),
    warning ? h('p', { class: 'set-warning' }, warning) : null,
    h('section', { class: 'set-section' },
      h('h3', {}, 'Inteligencia artificial'),
      row('Proveedor', picker, h('p', { class: 'set-facts' }, facts)),
      sel.note ? row('', h('p', { class: 'set-note' }, mdInline(sel.note))) : null,
      keyLine ? row('', keyLine) : null,
      url ? row('URL', url) : null,
      row('Modelo', model),
      row('Modelo rápido', quick, h('p', { class: 'set-facts' }, 'Para el chat, las notas de concepto y el plan.')),
      row('', h('div', { class: 'set-actions' }, save, test), result)),
    updateSection(row),
    h('p', { class: 'set-foot' }, `Se guarda en ${SET.data.configPath || 'studyhomie.toml'}. Lo que esté en curso termina con la IA anterior.`)));
}

/* ---------- Actualizaciones ---------- */
// The server looks for new versions on GitHub (at most every few hours). In "ask" mode a bar under
// the header offers it; installing downloads, verifies, swaps the binary and restarts the app, and
// this tab waits for the new version to answer and reloads.
const UPD = { st: null, busy: false };
const UPDATE_MODES = { ask: 'Preguntarme antes de actualizar', auto: 'Instalar sola (se usa al reiniciar)', never: 'No buscar versiones nuevas' };
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

function laterFor(tag) { try { return sessionStorage.getItem('update-later') === tag; } catch { return false; } }
function setLater(tag) { try { sessionStorage.setItem('update-later', tag); } catch { /* private mode */ } }

async function checkUpdate(force = false) {
  try { UPD.st = await api(`/api/update${force ? '?force=1' : ''}`); } catch { /* offline: keep what we knew */ }
  drawUpdateBar();
  return UPD.st;
}

function drawUpdateBar() {
  const st = UPD.st;
  const show = !!st && st.enabled && st.available && !st.skipped && st.mode !== 'never' && !laterFor(st.latest);
  $('#update-bar').hidden = !show || UPD.busy;
  if (!show) return;
  $('#update-text').replaceChildren(st.staged
    ? h('span', {}, h('strong', {}, `StudyHomie ${st.latest} ya está instalado.`), ' Se usa al reiniciar.')
    : h('span', {}, h('strong', {}, 'Hay una versión nueva'), ` (${st.latest}). Tienes la ${st.current}.`));
  $('#update-go span').textContent = st.staged ? 'Reiniciar ahora' : 'Actualizar';
  $('#update-go use').setAttribute('href', `icons.svg#i-${st.staged ? 'refresh-cw' : 'download'}`);
}

function fmtDay(iso) {
  const d = new Date(iso);
  return isNaN(d) || d.getFullYear() < 2000 ? '' : d.toLocaleDateString('es', { day: 'numeric', month: 'long', year: 'numeric' });
}

function openUpdateDialog() {
  const st = UPD.st;
  if (!st || !st.latest) return;
  $('#update-title').textContent = `StudyHomie ${st.latest}`;
  const day = st.published && fmtDay(st.published);
  $('#update-body').replaceChildren(...[
    h('p', { class: 'update-meta' }, [day ? `Publicada el ${day}` : '', `tienes la ${st.current}`].filter(Boolean).join(' · ')),
    mdBlock(st.notes || 'Esta versión no trae notas.'),
    st.page ? h('p', { class: 'update-meta' }, h('a', { href: st.page, target: '_blank', rel: 'noopener noreferrer' }, 'Ver la versión en GitHub')) : null,
  ].filter(Boolean));
  updateFoot();
  if (!$('#update-dialog').open) $('#update-dialog').showModal();
}

// the dialog's buttons, or a line of progress, or an error with what to do by hand
function updateFoot(state = {}) {
  const st = UPD.st;
  const foot = $('#update-foot');
  const btn = (cls, label, onclick, ic) => h('button', { class: `btn small ${cls}`, type: 'button', onclick }, ic ? icon(ic) : null, label);
  if (state.progress) {
    foot.replaceChildren(h('p', { class: 'update-progress', role: 'status' }, h('span', { class: 'pulse' }), state.progress));
    return;
  }
  const msg = state.error ? h('p', { class: 'update-msg bad', role: 'alert' }, state.error)
    : state.note ? h('p', { class: 'update-msg', role: 'status' }, state.note) : null;
  if (state.busy) {
    foot.replaceChildren(...[msg].filter(Boolean), h('div', { class: 'update-actions' },
      h('span', { class: 'spacer' }),
      btn('', 'Esperar a que termine', () => $('#update-dialog').close()),
      btn('primary', 'Actualizar igual', () => installUpdate(true), 'download')));
    return;
  }
  if (state.done) { foot.replaceChildren(...[msg].filter(Boolean)); return; }
  foot.replaceChildren(...[msg].filter(Boolean), h('div', { class: 'update-actions' },
    st.staged ? null : btn('ghost', 'Omitir esta versión', skipUpdate),
    h('span', { class: 'spacer' }),
    btn('', 'Más tarde', () => { setLater(st.latest); drawUpdateBar(); $('#update-dialog').close(); }),
    btn('primary', st.staged ? 'Reiniciar ahora' : 'Actualizar', () => installUpdate(), st.staged ? 'refresh-cw' : 'download')));
}

async function skipUpdate() {
  try {
    UPD.st = await api('/api/update/skip', { method: 'POST', body: { version: UPD.st.latest } });
    $('#update-dialog').close();
    drawUpdateBar();
    toast(`No te avisaré de la ${UPD.st.latest}; sí de la siguiente.`);
  } catch (err) { updateFoot({ error: err.message }); }
}

async function installUpdate(force = false) {
  const st = UPD.st;
  if (!st || UPD.busy) return;
  openUpdateDialog();
  UPD.busy = true;
  drawUpdateBar();
  updateFoot({ progress: st.staged ? 'Reiniciando…' : `Descargando y verificando ${st.latest}…` });
  try {
    const r = st.staged
      ? await api('/api/update/restart', { method: 'POST', body: {} })
      : await api('/api/update', { method: 'POST', body: { force } });
    if (!r.restarting) {
      UPD.busy = false;
      await checkUpdate();
      updateFoot({ done: true, note: `Listo: la ${r.version || st.latest} quedó instalada. Cierra StudyHomie y vuelve a abrirlo para usarla.` });
      return;
    }
    updateFoot({ progress: 'Reiniciando StudyHomie…' });
    await waitForVersion(r.version || st.latest);
  } catch (err) {
    UPD.busy = false;
    drawUpdateBar();
    if (err.data && err.data.busy) updateFoot({ busy: true, note: err.message });
    else updateFoot({ error: err.message });
  }
}

// waits until the restarted server answers with the new version, then reloads the page
async function waitForVersion(tag) {
  const until = Date.now() + 90_000;
  await sleep(1200);
  while (Date.now() < until) {
    try {
      const res = await fetch('/api/state', { cache: 'no-store' });
      if (res.ok && (await res.json()).version === tag) { location.reload(); return; }
    } catch { /* still restarting */ }
    await sleep(1000);
  }
  throw new Error('StudyHomie no volvió a arrancar solo. Ábrelo de nuevo: la versión nueva ya está instalada.');
}

$('#update-notes').addEventListener('click', openUpdateDialog);
$('#update-go').addEventListener('click', () => installUpdate());
$('#update-later').addEventListener('click', () => { setLater(UPD.st.latest); drawUpdateBar(); });
$('#update-close').addEventListener('click', () => $('#update-dialog').close());

// the "Actualizaciones" block of the settings page
function updateSection(row) {
  const st = UPD.st;
  if (!st) return null;
  const head = h('h3', {}, 'Actualizaciones');
  if (!st.enabled) {
    return h('section', { class: 'set-section' }, head,
      row('Versión', h('p', { class: 'set-facts' }, `${st.current} · compilación de desarrollo: no se actualiza sola.`)));
  }
  const result = h('p', { class: 'set-result', role: 'status' });
  const say = (cls, text) => { result.className = `set-result ${cls}`; result.textContent = text; };
  const mode = h('select', { 'aria-label': 'Al salir una versión nueva', onchange: async (e) => {
    try {
      UPD.st = await api('/api/update/mode', { method: 'POST', body: { mode: e.target.value } });
      drawUpdateBar();
      say('ok', 'Guardado.');
    } catch (err) { say('bad', err.message); }
  } }, Object.entries(UPDATE_MODES).map(([v, label]) => h('option', { value: v, selected: v === st.mode || null }, label)));

  let line;
  if (st.staged) line = `La ${st.latest} ya está instalada: se usa al reiniciar.`;
  else if (st.available) line = `Hay una versión nueva: ${st.latest}.`;
  else if (st.latest) line = 'Tienes la última versión.';
  else line = fmtDay(st.checked) ? 'Todavía no hay versiones publicadas.' : 'Todavía no se buscó.';
  const checked = fmtDay(st.checked) ? ` Última búsqueda: ${new Date(st.checked).toLocaleString('es', { dateStyle: 'medium', timeStyle: 'short' })}.` : '';
  if (st.error) say('bad', st.error);

  const find = h('button', { class: 'btn small ghost', type: 'button', onclick: async (e) => {
    e.currentTarget.disabled = true;
    say('', 'Buscando…');
    await checkUpdate(true);
    drawSettings();
  } }, icon('refresh-cw'), 'Buscar ahora');
  const see = st.available ? h('button', { class: 'btn small primary', type: 'button', onclick: openUpdateDialog }, st.staged ? 'Reiniciar' : 'Ver y actualizar') : null;

  return h('section', { class: 'set-section' }, head,
    row('Versión', h('p', { class: 'set-facts' }, h('strong', {}, st.current), ` · ${line}${checked}`)),
    row('Al salir una nueva', mode),
    row('', h('div', { class: 'set-actions' }, see, find), result));
}

// a little Markdown for the notes of the catalogue (code spans)
function mdInline(s) { const span = h('span'); span.innerHTML = DOMPurify.sanitize(marked.parseInline(s)); return span; }

// the layout menu: open a panel, or start over
$('#btn-layout').addEventListener('click', (e) => {
  e.stopPropagation();
  const menu = $('#layout-menu');
  if (!menu.hidden) { menu.hidden = true; return; }
  menu.replaceChildren(
    ...Object.entries(PANES).map(([id, title]) => h('button', { type: 'button', onclick: () => { menu.hidden = true; show(id); } },
      S.dock.getPanel(id) ? '● ' : '○ ', title)),
    h('hr'),
    h('button', { type: 'button', onclick: () => { menu.hidden = true; setLayoutEditing(!S.editingLayout); } },
      icon(S.editingLayout ? 'lock' : 'lock-open'), S.editingLayout ? 'Terminar de editar' : 'Editar diseño'),
    h('button', { type: 'button', onclick: () => { menu.hidden = true; store.set('dock', null); defaultLayout(); toast('Diseño restablecido.'); } },
      icon('rotate-ccw'), 'Restablecer diseño'));
  menu.hidden = false;
  $('#btn-layout').setAttribute('aria-expanded', 'true');
});
document.addEventListener('click', (e) => {
  const menu = $('#layout-menu');
  if (!menu.hidden && !e.target.closest('.menu')) { menu.hidden = true; $('#btn-layout').setAttribute('aria-expanded', 'false'); }
  if (!$('#plus-menu').hidden && !e.target.closest('.menu.plus')) closePlus();
  const pop = $('#explain-pop');
  if (!pop.hidden && !e.target.closest('#explain-pop') && !e.target.closest('a.pending')) pop.hidden = true;
});
document.addEventListener('keydown', (e) => { if (e.key === 'Escape') { $('#layout-menu').hidden = true; $('#explain-pop').hidden = true; closePlus(); } });

/* ---------- opening notes ---------- */
// Opens a guide (Guía) or a concept (Conceptos).
async function openNote(slug, { quiet = false } = {}) {
  try {
    const n = await api(`/api/notes/${encodeURIComponent(slug)}`);
    vt(() => {
      if (n.kind === 'concept') {
        S.concept = n;
        renderConcepts();
        PANE_EL.concepts.scrollTop = 0;
        if (!quiet) show('concepts');
      } else {
        setGuide(n);
        if (!quiet) show('guide');
      }
    });
  } catch (err) { toast(err.message); }
}

function setGuide(n) {
  const changed = !S.note || S.note.slug !== n.slug;
  S.note = n;
  S.guide = n.slug;
  store.set('guide', n.slug);
  if (changed) S.concept = null;
  const top = PANE_EL.guide.scrollTop;
  renderReader(n);
  PANE_EL.guide.scrollTop = changed ? 0 : top;
  renderConcepts();
  renderPractice();
}

async function reloadGuide() {
  if (!S.guide) return;
  try { setGuide(await api(`/api/notes/${encodeURIComponent(S.guide)}`)); } catch { closeGuide(); }
}
function closeGuide() {
  S.note = null; S.guide = ''; S.concept = null;
  store.set('guide', '');
  renderReader(null);
  renderConcepts();
  renderPractice();
}

// wikilinks and simulation links rendered anywhere in the app
document.addEventListener('click', (e) => {
  const a = e.target.closest('a[href^="#note/"], a[href^="#sim/"]');
  if (!a) return;
  e.preventDefault();
  const href = a.getAttribute('href');
  if (href.startsWith('#sim/')) { openSim(decodeURIComponent(href.slice(5)), S.note && S.note.meta.title); return; }
  const slug = decodeURIComponent(href.slice(6));
  const known = writtenNode(slug);
  if (known && !a.classList.contains('pending')) { openNote(known.id); return; }
  explainPop(a, slug);
});

// The popover of a grey link: what the concept is, and a button to explain it.
function explainPop(a, slug) {
  const pop = $('#explain-pop');
  const title = a.textContent.trim() || slug;
  const pend = (S.note && (S.note.meta.pending || []).find((p) => p.id === slug || p.id === foldSlug(slug))) || nodeOf(slug) || {};
  const guide = owningGuide(slug);
  pop.replaceChildren(
    h('p', { class: 'eyebrow' }, 'Aún sin nota'),
    h('h4', {}, pend.title || pend.label || title),
    h('p', {}, pend.summary || 'Escribo una nota corta, con lo justo para seguir la guía. Queda en tu vault.'),
    h('div', { class: 'actions' },
      h('button', { class: 'btn small primary', type: 'button', onclick: () => {
        pop.hidden = true;
        explainConcept(slug, { guide, title: pend.title || title, el: a });
      } }, icon('sparkles'), 'Explicar'),
      h('button', { class: 'btn small ghost', type: 'button', onclick: () => {
        pop.hidden = true;
        chatAction({ action: 'guide', topic: pend.title || title, depth: S.depth }, `Guía: ${pend.title || title}`);
      } }, 'Guía completa')));
  pop.hidden = false;
  const r = a.getBoundingClientRect();
  const w = pop.offsetWidth, ht = pop.offsetHeight;
  pop.style.left = `${Math.max(8, Math.min(window.innerWidth - w - 8, r.left))}px`;
  pop.style.top = `${r.bottom + ht + 12 > window.innerHeight ? r.top - ht - 8 : r.bottom + 8}px`;
}

// the guide a concept to explain belongs to: the open one, or the guide the open concept comes from
function owningGuide(slug) {
  if (S.note && ((S.note.meta.pending || []).some((p) => p.id === slug) || !S.concept)) return S.note.slug;
  if (S.concept && (S.concept.guides || []).length) return S.concept.guides[0].slug;
  return S.note ? S.note.slug : '';
}

// Writes the note of a concept under water. el (a link or a pill) shows that it is being written.
async function explainConcept(ref, { guide = S.guide, title = ref, el } = {}) {
  if (!guide) { chatAction({ action: 'concept', title }, `Explícame «${title}»`); return; }
  el?.classList.add('writing');
  try {
    const r = await api(`/api/notes/${encodeURIComponent(guide)}/explain`, { method: 'POST', body: { ref } });
    S.lit = r.slug;
    await loadGraph();
    if (S.guide === guide) await reloadGuide(); else renderConcepts();
    if (S.concept) { try { S.concept = await api(`/api/notes/${encodeURIComponent(S.concept.slug)}`); renderConcepts(); } catch { /* it stays as it was */ } }
    toast(`«${title}» ya tiene su nota.`);
  } catch (err) {
    el?.classList.remove('writing');
    toast(err.message);
  }
}

function openSim(slug, title) {
  $('#sim-title').textContent = title || 'Simulación';
  $('#sim-frame').src = `/interactive/${encodeURIComponent(slug)}.html`;
  $('#sim-dialog').showModal();
}
$('#sim-close').addEventListener('click', () => $('#sim-dialog').close());
$('#sim-dialog').addEventListener('close', () => { $('#sim-frame').src = 'about:blank'; });

/* ---------- Guía: the guide as an article ---------- */
const statusChip = (status) => h('span', { class: 'chip status', style: `--c: var(--s-${status})` }, STATUS[status] || status);

// widgets in a note can build their own page on demand
const vizCtx = (slug) => ({
  generate: async (spec) => (await api(`/api/notes/${encodeURIComponent(slug)}/viz`, { method: 'POST', body: { brief: spec.brief } })).src,
});

// every guide, grouped by roadmap: what the panel offers when no guide is open
function guideList() {
  const guides = ((S.graph && S.graph.nodes) || []).filter((n) => n.kind === 'guide');
  if (!guides.length) return null;
  const groups = new Map();
  for (const n of guides) {
    const k = n.roadmap || '';
    if (!groups.has(k)) groups.set(k, []);
    groups.get(k).push(n);
  }
  const keys = [...groups.keys()].sort((a, b) => (!a) - (!b) || groupTitle(a).localeCompare(groupTitle(b)));
  return h('div', { class: 'guide-list' }, keys.map((k) => h('section', { class: 'dg' },
    h('h3', {}, k ? groupTitle(k) : 'Tus guías'),
    h('ul', {}, groups.get(k).sort((a, b) => (a.order || 0) - (b.order || 0) || a.label.localeCompare(b.label)).map((n) => h('li', {},
      h('button', { class: 'dn', type: 'button', style: `--lc: ${lineColor(n.group)}`, onclick: () => openNote(n.id) },
        h('i', { class: `st st-${n.status}`, title: STATUS[n.status] || n.status }), n.label)))))));
}

function renderReader(n) {
  const root = $('#reader');
  root.replaceChildren();
  if (!n) {
    root.append(h('div', { class: 'panel-empty' },
      h('h3', {}, 'Aquí aparecerá tu guía'),
      h('p', {}, 'Pide en el chat lo que quieras entender. «Escribir ahora» decide cuántos niveles de conceptos base se escriben de entrada; los demás quedan mapeados, en gris, para explicarlos cuando los necesites.'),
      guideList() || ''));
    return;
  }
  const m = n.meta;
  const status = m.status || 'new';
  const title = m.title || n.slug;
  const node = nodeOf(n.slug);
  const concepts = n.concepts || [];
  const under = (m.pending || []).length;
  const wrap = h('div', { class: 'reader-wrap', style: `--line-c: ${lineColor(node ? node.group : '')}` });
  wrap.append(h('header', { class: 'r-head' },
    h('button', { class: 'crumb', type: 'button', onclick: closeGuide }, icon('arrow-left'), 'Todas las guías'),
    h('p', { class: 'r-line' }, m.roadmap ? groupTitle(m.roadmap) : 'Guía'),
    h('h1', {}, title),
    h('div', { class: 'chips' }, statusChip(status),
      m.focus ? h('span', { class: 'chip' }, `Foco: ${m.focus}`) : null,
      m.field ? h('span', { class: 'chip' }, m.field) : null),
    m.mastery ? h('div', { class: 'mastery' },
      h('div', { class: 'row' }, h('span', {}, 'Lo que llevas'), h('span', {}, `${m.mastery}/100`)),
      h('div', { class: 'bar' }, h('i', { style: `width:${m.mastery}%; background: var(--line-c)` }))) : null,
    h('div', { class: 'actions' },
      concepts.length || under ? h('button', { class: 'btn small yellow', type: 'button', onclick: () => show('concepts') },
        icon('mountain-snow'), `${concepts.length} conceptos${under ? ` · ${under} bajo el agua` : ''}`) : null,
      status !== 'new' ? h('button', { class: 'btn small', type: 'button', onclick: () => show('practice') }, icon('target'), 'Practicar') : null,
      notesButton(n.slug, title, n.apunte),
      h('button', { class: 'btn small ghost', type: 'button', onclick: () => askAbout(`Sobre «${title}»: `) }, icon('message-circle'), 'Preguntar'))));

  const article = h('article', { class: 'r-article' });
  const bodySrc = stripEmoji(stripSection(n.body, 'Recursos')).trim();
  if (bodySrc) {
    const md = mdBlock(bodySrc, { viz: vizCtx(n.slug) });
    for (const hd of $$('h2', md)) hd.textContent = SECTION[hd.textContent.trim()] || hd.textContent;
    article.append(md);
  } else {
    article.append(h('p', { class: 'r-empty' }, 'Esta nota aún no tiene guía. Pídela en el chat.'));
  }
  const res = m.resources || [];
  if (res.length) {
    article.append(h('section', { class: 'r-resources' }, h('h2', {}, 'Para seguir explorando'),
      h('ul', { class: 'res-list' }, res.map((r) => resourceCard(r, { slug: n.slug, title })))));
  }
  const back = (n.backlinks || []).filter((b) => !concepts.some((c) => c.slug === b.slug));
  const before = n.requires || [];
  if (before.length || back.length) {
    const nb = (label, l) => h('button', { class: 'neighbor', type: 'button', onclick: () => openNote(l.slug) }, h('small', {}, label), h('strong', {}, l.title));
    article.append(h('section', { class: 'r-next' }, h('h2', {}, 'Relacionado'),
      before.map((l) => nb('Antes', l)), back.map((l) => nb('Enlaza aquí', l))));
  }
  wrap.append(article);
  root.append(wrap);
}

const YOUTUBE = /^https?:\/\/(?:www\.|m\.)?(?:youtube\.com\/watch\?(?:[^#]*&)?v=|youtu\.be\/)([\w-]{11})(?![\w-])/i;
const youtubeId = (url) => { const m = YOUTUBE.exec(url || ''); return m && m[1]; };
const hostOf = (url) => { try { return new URL(url).hostname.replace(/^www\./, ''); } catch { return ''; } };

// A video is only loaded from YouTube once the learner asks for it.
function videoPlayer(r, id) {
  const frame = h('div', { class: 'video-frame' });
  frame.append(h('button', {
    class: 'video-play', type: 'button', 'aria-label': `Reproducir: ${r.title}`,
    onclick: () => frame.replaceChildren(h('iframe', {
      src: `https://www.youtube-nocookie.com/embed/${id}?rel=0&autoplay=1`, title: r.title, loading: 'lazy',
      allow: 'accelerometer; autoplay; encrypted-media; picture-in-picture; fullscreen', allowfullscreen: '',
      referrerpolicy: 'strict-origin-when-cross-origin',
    })),
  }, h('span', { class: 'play-icon', 'aria-hidden': 'true' }, '▶'), h('span', {}, 'Reproducir aquí'), h('small', {}, 'Carga YouTube al hacer clic')));
  return frame;
}

// One recommended resource. ctx.slug/title identify the note it belongs to (for simulations).
function resourceCard(r, ctx = {}) {
  const external = r.url && /^https?:\/\//i.test(r.url);
  const host = external ? hostOf(r.url) : '';
  const head = (titleEl) => h('div', { class: 'res-head' },
    h('div', { class: 'res-main' }, titleEl, h('span', { class: 'res-meta' }, [TYPE[r.type] || r.type, host].filter(Boolean).join(' · '))));
  const why = r.why && h('span', { class: 'why' }, r.why);
  const link = () => h('a', { href: r.url, target: '_blank', rel: 'noopener noreferrer' }, r.title);
  const yt = external && youtubeId(r.url);
  if (yt) return h('li', { class: 'res res-video' }, head(link()), videoPlayer(r, yt), why);
  if (external) return h('li', { class: 'res' }, head(link()), why);
  const local = r.url && /^interactive\/([^/]+)\.html$/.exec(r.url);
  if (local) return h('li', { class: 'res' }, head(h('button', { class: 'link', type: 'button', onclick: () => openSim(local[1], ctx.title) }, r.title)), why);
  if (r.type === 'interactive') {
    return h('li', { class: 'res' }, head(h('strong', {}, `${r.title} (propuesta)`)), why,
      ctx.slug && h('div', {}, h('button', { class: 'btn small', type: 'button', onclick: () => chatAction({ action: 'interactive', brief: r.why || '' }, `Simulación: ${ctx.title}`) }, 'Generar')));
  }
  return h('li', { class: 'res' }, head(h('strong', {}, r.title)), why);
}

/* ---------- Conceptos: the iceberg ---------- */
// Levels of the written concepts: 1 for what the topic needs directly, then what those need, and so on.
function conceptLevels(n) {
  const list = n.concepts || [];
  const by = new Map(list.map((c) => [c.slug, c]));
  const level = new Map();
  let frontier = ((n.meta.base && n.meta.base.length) ? n.meta.base : list.map((c) => c.slug)).filter((s) => by.has(s));
  frontier.forEach((s) => level.set(s, 1));
  for (let l = 1; frontier.length; l++) {
    const next = [];
    for (const s of frontier) for (const r of by.get(s).requires || []) if (by.has(r) && !level.has(r)) { level.set(r, l + 1); next.push(r); }
    frontier = next;
  }
  for (const c of list) if (!level.has(c.slug)) level.set(c.slug, 1);
  return level;
}
const levelName = (l) => (l === 1 ? 'Lo que el tema usa directamente' : l === 2 ? 'Lo que eso necesita' : `Más hondo (nivel ${l})`);

function renderConcepts() {
  const root = PANE_EL.concepts;
  if (!root) return;
  root.replaceChildren();
  if (S.concept) { root.append(conceptReader(S.concept)); return; }
  const n = S.note;
  const box = h('div', { class: 'concepts-wrap' });
  const pending = (n && n.meta.pending) || [];
  if (n && ((n.concepts || []).length || pending.length)) {
    const levels = new Map(); // level -> { written, pending }
    const at = (l) => { if (!levels.has(l)) levels.set(l, { written: [], pending: [] }); return levels.get(l); };
    const lv = conceptLevels(n);
    for (const c of n.concepts || []) at(lv.get(c.slug) || 1).written.push(c);
    for (const p of pending) at(p.level || (levels.size + 1)).pending.push(p);
    const keys = [...levels.keys()].sort((a, b) => a - b);
    let i = 0;
    const pill = (c, isPending) => {
      const el = h('button', {
        class: `ice-pill${isPending ? ' pending' : ''}${S.lit === (c.slug || c.id) ? ' lit' : ''}`, type: 'button', style: `--i:${i++}`,
        title: isPending ? 'Aún sin nota: pulsa para explicarlo' : 'Abrir la nota',
        onclick: () => (isPending ? explainConcept(c.id, { guide: n.slug, title: c.title, el }) : openNote(c.slug)),
      }, h('strong', {}, c.title), c.summary ? h('small', {}, c.summary) : null, isPending ? h('span', { class: 'go' }, 'Explicar') : null);
      return el;
    };
    box.append(
      h('header', { class: 'c-head' },
        h('p', { class: 'eyebrow' }, 'Lo que hay debajo'),
        h('h2', {}, n.meta.title),
        h('p', { class: 'hint' }, pending.length
          ? 'Lo blanco ya tiene nota. Lo punteado está mapeado pero sin escribir: explícalo solo si lo necesitas.'
          : 'Cada nivel sostiene al de arriba. Si algo no se entiende, baja un nivel.')),
      h('div', { class: 'iceberg' },
        h('div', { class: 'ice-tip' }, h('span', { class: 'tip' }, n.meta.title), h('span', { class: 'ice-wave', 'aria-hidden': 'true' })),
        h('ol', { class: 'ice-levels' }, keys.map((l) => {
          const g = levels.get(l);
          return h('li', { class: `ice-level${l > 1 ? ' deep' : ''}`, style: `--fade:${Math.max(0, 1 - (l - 1) * 0.22)}` },
            h('h3', {}, levelName(l), h('small', {}, g.pending.length ? `${g.pending.length} por explicar` : '')),
            h('div', { class: 'ice-pills' }, g.written.map((c) => pill(c, !c.exists)), g.pending.map((p) => pill(p, true))));
        }))),
      h('div', { class: 'ice-legend' },
        h('span', {}, h('i', { class: 'st st-concept' }), 'Con nota'),
        h('span', {}, h('i', { class: 'st st-pending' }), 'Por explicar (se escribe al pulsarlo)')));
    S.lit = '';
  } else if (n) {
    box.append(h('div', { class: 'panel-empty' }, h('h3', {}, 'Esta guía no tiene conceptos mapeados'),
      h('p', {}, 'Las guías nuevas mapean todo lo que dan por sabido. También puedes pedir en el chat la nota de un concepto concreto.')));
  }
  const all = ((S.graph && S.graph.nodes) || []).filter((x) => x.kind === 'concept');
  if (all.length) {
    const byCat = new Map();
    for (const c of all) { const k = c.category || 'sin categoría'; if (!byCat.has(k)) byCat.set(k, []); byCat.get(k).push(c); }
    box.append(h('details', { class: 'all-concepts', open: !n || null },
      h('summary', {}, `Todos tus conceptos (${all.length})`),
      [...byCat].sort((a, b) => a[0].localeCompare(b[0])).map(([cat, list]) => h('section', { class: 'dg' }, h('h3', {}, cat),
        h('div', { class: 'links' }, list.sort((a, b) => a.label.localeCompare(b.label)).map((c) => h('button', { type: 'button', onclick: () => openNote(c.id) }, c.label)))))));
  } else if (!n) {
    box.append(h('div', { class: 'panel-empty' }, h('h3', {}, 'Aún no hay conceptos'), h('p', {}, 'Cuando pidas una guía, aquí verás su iceberg: lo que tiene nota y lo que queda por explicar.')));
  }
  root.append(box);
}

function conceptReader(c) {
  const m = c.meta;
  const node = nodeOf(c.slug);
  const body = stripEmoji(c.body.replace(/^# .*\n+/, '')).trim();
  return h('div', { class: 'reader-wrap concept', style: `--line-c: ${lineColor(node ? node.group : '')}` },
    h('header', { class: 'r-head' },
      h('button', { class: 'crumb', type: 'button', onclick: () => { vt(() => { S.concept = null; renderConcepts(); }); } }, icon('arrow-left'), S.note ? `Iceberg de ${S.note.meta.title}` : 'Conceptos'),
      h('p', { class: 'r-line' }, ['Concepto', m.category].filter(Boolean).join(' · ')),
      h('h1', {}, m.title || c.slug),
      (m.aliases || []).length ? h('div', { class: 'chips' }, m.aliases.map((a) => h('span', { class: 'chip' }, a))) : null,
      (c.requires || []).length ? h('div', { class: 'c-req' }, h('span', { class: 'hint' }, 'Se apoya en'), h('div', { class: 'links' }, c.requires.map((l) => h('button', { type: 'button', onclick: () => (writtenNode(l.slug) ? openNote(l.slug) : explainConcept(l.slug, { title: l.title, guide: owningGuide(l.slug) })) }, l.title)))) : null,
      h('div', { class: 'actions' }, notesButton(c.slug, m.title || c.slug, c.apunte),
        h('button', { class: 'btn small ghost', type: 'button', onclick: () => askAbout(`Sobre «${m.title}»: `) }, icon('message-circle'), 'Preguntar'))),
    h('article', { class: 'r-article' }, mdBlock(stripSection(body, 'Aparece en'), { viz: vizCtx(c.slug) }),
      (c.guides || []).length ? h('section', { class: 'r-next' }, h('h2', {}, 'Aparece en'),
        c.guides.map((g) => h('button', { class: 'neighbor', type: 'button', onclick: () => openNote(g.slug) }, h('small', {}, 'Guía'), h('strong', {}, g.title)))) : null));
}

function askAbout(prefix) {
  show('chat');
  const ta = $('#reply');
  ta.value = prefix;
  ta.focus();
  ta.setSelectionRange(ta.value.length, ta.value.length);
}

/* ---------- Apuntes: the learner's own notes on a guide or a concept ---------- */
// Kept in apuntes/<slug>.md, apart from what the app writes; pasted files go to apuntes/adjuntos/.
const N = { slug: '', title: '', body: '', file: '', mode: 'view', status: '', timer: null, saving: null, loading: false };

function notesButton(slug, title, has) {
  return h('button', { class: 'btn small ghost', type: 'button', onclick: () => openNotes(slug, title) },
    icon('notebook-pen'), has ? 'Mis apuntes' : 'Tomar apuntes');
}

async function openNotes(slug, title) {
  await flushNotes();
  if (N.slug !== slug) Object.assign(N, { slug, title, body: '', file: '', status: '', loading: true });
  show('notes');
  try {
    const r = await api(`/api/apuntes/${encodeURIComponent(slug)}`);
    if (N.slug !== slug) return;
    Object.assign(N, { body: r.body, file: r.file, loading: false, mode: r.body.trim() ? 'view' : 'edit', status: r.updated ? `Guardado el ${r.updated}` : '' });
  } catch (err) { N.loading = false; N.status = err.message; }
  renderNotes();
}

// Obsidian embeds: ![[foto.png]] shows the image, [[archivo.pdf]] links the file
const ATTACH_URL = (name) => `/api/adjuntos/${encodeURIComponent(name)}`;
const IMAGE_EXT = /\.(png|jpe?g|gif|webp|avif|bmp)$/i;
function notesMd(src) {
  const s = src
    .replace(/!\[\[([^\]|]+?)(?:\|[^\]]*)?\]\]/g, (_, name) => (IMAGE_EXT.test(name.trim()) ? `![${name.trim()}](${ATTACH_URL(name.trim())})` : `[${name.trim()}](${ATTACH_URL(name.trim())})`))
    .replace(/\[\[([^\]|]+?\.(?!md\b)[a-z0-9]{1,6})(?:\|([^\]]*))?\]\]/gi, (_, name, label) => `[${(label || name).trim()}](${ATTACH_URL(name.trim())})`);
  return mdBlock(s, { breaks: true });
}

function renderNotes() {
  const root = PANE_EL.notes;
  if (!root) return;
  if (!N.slug) {
    const target = S.concept && isShown('concepts') ? S.concept : S.note;
    if (target) { openNotes(target.slug, target.meta.title || target.slug); return; }
    root.replaceChildren(h('div', { class: 'panel-empty' }, h('h3', {}, 'Apuntes'),
      h('p', {}, 'Abre una guía o un concepto y pulsa «Tomar apuntes»: lo que escribas queda junto a esa nota, en tu vault.')));
    return;
  }
  const status = h('span', { class: 'notes-status', role: 'status' }, N.status);
  const file = h('input', { type: 'file', multiple: true, hidden: true });
  file.addEventListener('change', () => { attachToNotes(file.files); file.value = ''; });
  const mode = (m, label) => h('button', { type: 'button', 'aria-pressed': String(N.mode === m), onclick: () => { N.mode = m; renderNotes(); } }, label);
  let editor;
  if (N.loading) editor = h('p', { class: 'hint' }, 'Cargando…');
  else if (N.mode === 'edit') {
    editor = h('textarea', { class: 'notes-text', 'aria-label': `Apuntes de ${N.title}`, spellcheck: 'true',
      placeholder: 'Lo que entendiste, tus dudas, fórmulas con $…$, enlaces a otras notas con [[…]].\nPega o arrastra imágenes y archivos aquí.' });
    editor.value = N.body;
    editor.addEventListener('input', () => { N.body = editor.value; scheduleNotesSave(); });
    editor.addEventListener('blur', () => flushNotes());
    editor.addEventListener('paste', (e) => { if (e.clipboardData.files.length) { e.preventDefault(); attachToNotes(e.clipboardData.files, editor); } });
    editor.addEventListener('dragover', (e) => { if (e.dataTransfer.types.includes('Files')) { e.preventDefault(); editor.classList.add('drop'); } });
    editor.addEventListener('dragleave', () => editor.classList.remove('drop'));
    editor.addEventListener('drop', (e) => { editor.classList.remove('drop'); if (e.dataTransfer.files.length) { e.preventDefault(); attachToNotes(e.dataTransfer.files, editor); } });
  } else {
    editor = N.body.trim() ? h('div', { class: 'notes-view', ondblclick: () => { N.mode = 'edit'; renderNotes(); } }, notesMd(N.body))
      : h('p', { class: 'hint' }, 'Aún no escribiste nada aquí. Pulsa «Escribir».');
  }
  root.replaceChildren(h('div', { class: 'notes' },
    h('p', { class: 'eyebrow' }, 'Apuntes'),
    h('h2', {}, h('a', { href: `#note/${N.slug}` }, N.title)),
    h('div', { class: 'notes-bar' },
      h('div', { class: 'seg', role: 'group', 'aria-label': 'Modo' }, mode('edit', 'Escribir'), mode('view', 'Ver')),
      h('button', { class: 'btn small ghost', type: 'button', onclick: () => file.click() }, icon('paperclip'), 'Adjuntar'),
      file, status),
    editor,
    h('p', { class: 'hint notes-foot' }, `Se guardan solos en ${N.file || `apuntes/${N.slug}.md`} de tu vault.`)));
  if (N.mode === 'edit' && editor.tagName === 'TEXTAREA') editor.focus({ preventScroll: true });
}

function scheduleNotesSave() {
  clearTimeout(N.timer);
  setNotesStatus('Sin guardar…');
  N.timer = setTimeout(() => flushNotes(), 700);
}
function setNotesStatus(text) {
  N.status = text;
  const el = PANE_EL.notes && PANE_EL.notes.querySelector('.notes-status');
  if (el) el.textContent = text;
}
async function flushNotes() {
  if (!N.timer) return N.saving;
  clearTimeout(N.timer);
  N.timer = null;
  const { slug, body } = N;
  setNotesStatus('Guardando…');
  N.saving = api(`/api/apuntes/${encodeURIComponent(slug)}`, { method: 'PUT', body: { body } })
    .then(() => {
      if (N.slug === slug) setNotesStatus('Guardado');
      const has = !!body.trim();
      if (S.note && S.note.slug === slug && !!S.note.apunte !== has) { S.note.apunte = has; renderReader(S.note); }
      if (S.concept && S.concept.slug === slug && !!S.concept.apunte !== has) { S.concept.apunte = has; renderConcepts(); }
    })
    .catch((err) => { if (N.slug === slug) setNotesStatus(`No se guardó: ${err.message}`); });
  return N.saving;
}
// closing the page right after typing: send what is pending, without waiting for the answer
window.addEventListener('pagehide', () => {
  if (!N.timer) return;
  clearTimeout(N.timer);
  N.timer = null;
  fetch(`/api/apuntes/${encodeURIComponent(N.slug)}`, { method: 'PUT', keepalive: true, headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ body: N.body }) });
});

// uploads files into the vault and writes their embeds where the cursor is
async function attachToNotes(files, editor) {
  if (!files || !files.length) return;
  const fd = new FormData();
  for (const f of files) fd.append('files', f);
  setNotesStatus('Subiendo…');
  try {
    const r = await api('/api/adjuntos', { method: 'POST', body: fd });
    const text = r.files.map((f) => (f.image ? `![[${f.name}]]` : `[[${f.name}]]`)).join('\n');
    if (N.mode !== 'edit' || !editor) {
      N.body = N.body.replace(/\s*$/, N.body.trim() ? '\n\n' : '') + text + '\n';
      N.mode = 'edit';
      renderNotes();
    } else {
      const at = editor.selectionStart;
      const before = N.body.slice(0, at);
      const sep = before && !before.endsWith('\n') ? '\n' : '';
      N.body = before + sep + text + '\n' + N.body.slice(editor.selectionEnd);
      editor.value = N.body;
      editor.selectionStart = editor.selectionEnd = (before + sep + text + '\n').length;
    }
    scheduleNotesSave();
  } catch (err) { setNotesStatus(err.message); }
}

/* ---------- Vault: your vaults, and every note in the one in use ---------- */
const DIRS = { guias: 'Guía', conceptos: 'Concepto', apuntes: 'Apuntes', roadmaps: 'Roadmap', daily: 'Diario' };
const DIRS_PLURAL = { guias: 'guías', conceptos: 'conceptos', apuntes: 'apuntes', roadmaps: 'roadmaps', daily: 'días' };
const VLT = { info: null, files: [], hits: null, q: '', dir: '', adding: false, edit: null, timer: 0 };

function fmtBytes(n) {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(0)} KB`;
  return `${(n / 1024 / 1024).toFixed(1).replace('.', ',')} MB`;
}

async function renderVault() {
  const root = PANE_EL.vault;
  if (!root) return;
  if (VLT.edit) { drawEditor(); return; }
  try {
    [VLT.info, VLT.files] = await Promise.all([api('/api/vaults'), api('/api/vault/files')]);
  } catch (err) { root.replaceChildren(h('p', { class: 'hint', style: 'padding:24px' }, err.message)); return; }
  drawVault();
}

function openOutside(app, file) {
  return api('/api/vault/open', { method: 'POST', body: { app, dir: file?.dir || '', slug: file?.slug || '' } })
    .then(() => toast(app === 'obsidian' ? 'Abriendo en Obsidian…' : 'Abriendo la carpeta…'))
    .catch((err) => toast(err.message));
}

// switching vaults reloads the app: chats, guides and map all belong to the vault
async function useVault(path, add) {
  await flushNotes();
  try {
    await api(add ? '/api/vaults' : '/api/vaults/use', { method: 'POST', body: { path } });
    store.set('guide', null);
    location.reload();
  } catch (err) { toast(err.message); }
}

function drawVault() {
  const root = PANE_EL.vault;
  const { vaults, root: abs, stats, canPick } = VLT.info;
  const cur = vaults.find((v) => v.current) || { name: abs.split(/[\\/]/).pop() };
  const ONE = { guias: 'guía', conceptos: 'concepto', apuntes: 'nota de apuntes', roadmaps: 'roadmap', daily: 'día' };
  const counts = Object.keys(DIRS).filter((d) => stats.notes[d]).map((d) => `${stats.notes[d]} ${stats.notes[d] === 1 ? ONE[d] : DIRS_PLURAL[d]}`);

  // the vaults: one row each, the one in use first
  const vaultRows = h('ul', { class: 'v-list' }, vaults.map((v) => h('li', { class: v.current ? 'on' : null },
    h('div', { class: 'v-main' }, h('strong', {}, v.name), h('small', {}, v.abs)),
    h('span', { class: 'v-meta' }, v.current ? 'en uso' : !v.exists ? 'no se encuentra' : `${v.guides} guías`),
    v.current ? null : h('div', { class: 'v-actions' },
      v.exists ? h('button', { class: 'btn small', type: 'button', onclick: () => useVault(v.path) }, 'Usar') : null,
      h('button', { class: 'btn small ghost', type: 'button', title: 'Quitar de la lista (la carpeta no se borra)', onclick: async () => {
        try { await api('/api/vaults/forget', { method: 'POST', body: { path: v.path } }); renderVault(); } catch (err) { toast(err.message); }
      } }, 'Quitar')))));

  const pathIn = h('input', { type: 'text', placeholder: 'D:\\Universidad\\Calculo', 'aria-label': 'Carpeta del vault', spellcheck: 'false' });
  const addForm = VLT.adding ? h('form', { class: 'v-add', onsubmit: (e) => { e.preventDefault(); if (pathIn.value.trim()) useVault(pathIn.value.trim(), true); } },
    h('div', { class: 'v-add-row' }, pathIn,
      canPick ? h('button', { class: 'btn small ghost', type: 'button', onclick: async (e) => {
        const b = e.currentTarget;
        b.disabled = true;
        try { const r = await api('/api/vaults/pick', { method: 'POST', body: {} }); if (r.path) pathIn.value = r.path; } catch (err) { toast(err.message); }
        b.disabled = false;
        pathIn.focus();
      } }, icon('folder-open'), 'Elegir carpeta…') : null,
      h('button', { class: 'btn small primary', type: 'submit' }, 'Abrir')),
    h('p', { class: 'hint' }, 'Una carpeta vacía (o nueva) se vuelve un vault con sus carpetas; una que ya tiene notas se abre tal cual. Cada vault tiene sus propias conversaciones.')) : null;

  // the notes: filter by folder, search by title and text
  const search = h('input', { type: 'search', value: VLT.q, placeholder: 'Buscar en títulos y texto…', 'aria-label': 'Buscar notas' });
  search.addEventListener('input', () => {
    VLT.q = search.value;
    clearTimeout(VLT.timer);
    VLT.timer = setTimeout(async () => {
      VLT.hits = VLT.q.trim().length >= 2 ? await api(`/api/vault/search?q=${encodeURIComponent(VLT.q.trim())}`).catch(() => []) : null;
      drawNoteList();
    }, 220);
  });
  const seg = h('div', { class: 'seg', role: 'group', 'aria-label': 'Carpeta' },
    [['', 'Todas'], ...Object.entries(DIRS).filter(([d]) => stats.notes[d]).map(([d]) => [d, DIRS_PLURAL[d][0].toUpperCase() + DIRS_PLURAL[d].slice(1)])]
      .map(([d, label]) => h('button', { type: 'button', 'aria-pressed': String(VLT.dir === d), onclick: () => { VLT.dir = d; drawVault(); } }, label)));

  root.replaceChildren(h('div', { class: 'vault' },
    h('p', { class: 'eyebrow' }, 'Vault'),
    h('h2', {}, cur.name),
    h('p', { class: 'v-path' }, abs),
    h('p', { class: 'v-stats' }, [...counts, `${fmtBytes(stats.bytes)} en total`].join(' · ')),
    h('div', { class: 'actions' },
      h('button', { class: 'btn small', type: 'button', onclick: () => openOutside('folder') }, icon('folder-open'), 'Abrir la carpeta'),
      h('button', { class: 'btn small', type: 'button', onclick: () => openOutside('obsidian') }, icon('external-link'), 'Abrir en Obsidian')),
    h('section', { class: 'v-section' },
      h('h3', {}, 'Tus vaults'),
      vaultRows,
      VLT.adding ? addForm : h('button', { class: 'btn small ghost v-add-btn', type: 'button', onclick: () => { VLT.adding = true; drawVault(); PANE_EL.vault.querySelector('.v-add input').focus(); } },
        icon('folder-plus'), 'Agregar o crear un vault')),
    h('section', { class: 'v-section' },
      h('h3', {}, 'Notas'),
      h('div', { class: 'v-tools' }, search, seg),
      h('ul', { class: 'v-notes', id: 'v-notes' }))));
  drawNoteList();
}

function drawNoteList() {
  const ul = $('#v-notes');
  if (!ul) return;
  const rows = VLT.hits || VLT.files.filter((f) => !VLT.q.trim() || slugify(f.title).includes(slugify(VLT.q)));
  const list = rows.filter((f) => !VLT.dir || f.dir === VLT.dir);
  if (!list.length) { ul.replaceChildren(h('li', { class: 'hint' }, VLT.q ? 'Nada coincide.' : 'Aún no hay notas aquí.')); return; }
  ul.replaceChildren(...list.slice(0, 300).map((f) => h('li', {},
    h('button', { class: 'v-open', type: 'button', onclick: () => openVaultFile(f) },
      h('strong', {}, f.title),
      h('small', {}, `${DIRS[f.dir] || f.dir} · ${when(f.updated)}`),
      f.snippet ? h('span', { class: 'v-snip' }, f.snippet) : null),
    h('div', { class: 'v-actions' },
      h('button', { class: 'icon-btn', type: 'button', title: 'Editar el Markdown', 'aria-label': `Editar ${f.title}`, onclick: () => editVaultFile(f) }, icon('file-pen-line')),
      h('button', { class: 'icon-btn', type: 'button', title: 'Abrir en Obsidian', 'aria-label': `Abrir ${f.title} en Obsidian`, onclick: () => openOutside('obsidian', f) }, icon('external-link')),
      h('button', { class: 'icon-btn', type: 'button', title: 'Mostrar en la carpeta', 'aria-label': `Mostrar ${f.title} en la carpeta`, onclick: () => openOutside('folder', f) }, icon('folder-open'))))));
}

function openVaultFile(f) {
  if (f.dir === 'guias' || f.dir === 'conceptos') openNote(f.slug);
  else if (f.dir === 'apuntes') openNotes(f.slug, f.title.replace(/^Apuntes: /, ''));
  else editVaultFile(f, 'view');
}

// the Markdown of a note, frontmatter included, edited by hand
async function editVaultFile(f, mode = 'edit') {
  try {
    const r = await api(`/api/vault/raw/${f.dir}/${encodeURIComponent(f.slug)}`);
    VLT.edit = { ...f, text: r.text, since: r.updated, mode, dirty: false, status: '' };
    show('vault');
    drawEditor();
  } catch (err) { toast(err.message); }
}

function drawEditor() {
  const root = PANE_EL.vault;
  const E = VLT.edit;
  const status = h('span', { class: 'notes-status', role: 'status' }, E.status);
  const say = (t) => { E.status = t; status.textContent = t; };
  const save = async (force = false) => {
    try {
      const r = await api(`/api/vault/raw/${E.dir}/${encodeURIComponent(E.slug)}`, { method: 'PUT', body: { text: E.text, since: E.since, force } });
      Object.assign(E, { since: r.updated, dirty: false });
      say('Guardado');
      loadGraph().catch(() => {});
      if (S.note && S.note.slug === E.slug && E.dir === 'guias') openNote(E.slug, { quiet: true });
      if (S.concept && S.concept.slug === E.slug && E.dir === 'conceptos') openNote(E.slug, { quiet: true });
      if (E.dir === 'apuntes' && N.slug === E.slug) N.slug = ''; // reload the notes from the file next time
    } catch (err) {
      if (/cambió/.test(err.message) && confirm('La nota cambió desde que la abriste (la app o Obsidian la reescribió). ¿Guardar tu versión encima?')) return save(true);
      say(err.message);
    }
  };
  const back = () => {
    if (E.dirty && !confirm('Tienes cambios sin guardar. ¿Salir sin guardarlos?')) return;
    VLT.edit = null;
    renderVault();
  };
  const mode = (m, label) => h('button', { type: 'button', 'aria-pressed': String(E.mode === m), onclick: () => { E.mode = m; drawEditor(); } }, label);
  let body;
  if (E.mode === 'edit') {
    body = h('textarea', { class: 'v-text', spellcheck: 'false', 'aria-label': `Markdown de ${E.title}` });
    body.value = E.text;
    body.addEventListener('input', () => { E.text = body.value; E.dirty = true; say('Sin guardar'); });
    body.addEventListener('keydown', (e) => { if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 's') { e.preventDefault(); save(); } });
  } else {
    const md = E.text.replace(/^---\n[\s\S]*?\n---\n*/, '');
    body = h('div', { class: 'notes-view' }, E.dir === 'apuntes' ? notesMd(md) : mdBlock(md, { breaks: E.dir !== 'guias' && E.dir !== 'conceptos' }));
  }
  root.replaceChildren(h('div', { class: 'vault v-editor' },
    h('button', { class: 'crumb', type: 'button', onclick: back }, icon('arrow-left'), 'Todas las notas'),
    h('p', { class: 'eyebrow' }, `${DIRS[E.dir] || E.dir} · ${E.dir}/${E.slug}.md`),
    h('h2', {}, E.title),
    h('div', { class: 'notes-bar' },
      h('div', { class: 'seg', role: 'group', 'aria-label': 'Modo' }, mode('edit', 'Editar'), mode('view', 'Ver')),
      h('button', { class: 'btn small primary', type: 'button', onclick: () => save() }, icon('save'), 'Guardar'),
      h('button', { class: 'btn small ghost', type: 'button', onclick: () => openOutside('obsidian', E) }, icon('external-link'), 'Obsidian'),
      status),
    body,
    h('p', { class: 'hint notes-foot' }, E.dir === 'guias' || E.dir === 'conceptos'
      ? 'Lo de arriba, entre las líneas ---, son las propiedades (YAML) que la app lee: cámbialas con cuidado. Si pides escribir esta nota de nuevo, la app la reemplaza; tus apuntes no se tocan. Ctrl+S guarda.'
      : 'Ctrl+S guarda.')));
  if (E.mode === 'edit') body.focus({ preventScroll: true });
}
$('#btn-vault').addEventListener('click', () => { VLT.edit = null; show('vault'); });

/* ---------- Practicar ---------- */
const STEP_RANK = { new: 0, prepared: 1, tested: 2, explained: 3 };
function renderPractice() {
  const root = PANE_EL.practice;
  if (!root) return;
  root.replaceChildren();
  const n = S.note;
  if (!n) {
    root.append(h('div', { class: 'panel-empty' }, h('h3', {}, 'Abre una guía para practicar'),
      h('p', {}, 'Las preguntas, la explicación con tus palabras y el repaso de conceptos trabajan sobre la guía abierta.'), guideList() || ''));
    return;
  }
  const m = n.meta;
  const rank = STEP_RANK[m.status || 'new'] ?? 0;
  const title = m.title || n.slug;
  const nInput = h('input', { type: 'number', min: 1, max: 30, value: 5, 'aria-label': 'Número de preguntas' });
  const diff = h('select', { 'aria-label': 'Dificultad' }, ['básica', 'media', 'avanzada'].map((d) => h('option', { value: d, selected: d === 'media' }, d)));
  const steps = [
    { label: 'Leer la guía', done: rank >= 1 },
    { label: 'Responder preguntas', done: rank >= 2 },
    { label: 'Explicarlo con tus palabras', done: rank >= 3 },
  ];
  const current = steps.findIndex((s) => !s.done);
  root.append(h('div', { class: 'practice' },
    h('header', { class: 'c-head' }, h('p', { class: 'eyebrow' }, 'Practicar'), h('h2', {}, title),
      m.mastery ? h('div', { class: 'mastery' }, h('div', { class: 'row' }, h('span', {}, 'Lo que llevas'), h('span', {}, `${m.mastery}/100`)), h('div', { class: 'bar' }, h('i', { style: `width:${m.mastery}%` }))) : null),
    h('ol', { class: 'sequence', 'aria-label': 'Cómo afianzarlo' }, steps.map((s, i) => h('li', {},
      h('span', { class: `step${i === current ? ' go' : ''}`, 'data-state': s.done ? 'done' : i === current ? 'current' : 'todo' }, h('span', { class: 'step-n' }, s.done ? '✓' : String(i + 1)), s.label)))),
    h('section', { class: 'p-card' },
      h('h3', {}, 'Preguntas'),
      h('p', { class: 'hint' }, 'Piden razonar o predecir, no memorizar. Se corrigen al final y el resultado queda en la guía.'),
      h('div', { class: 'quiz-opts' }, nInput, 'preguntas, dificultad', diff,
        h('button', { class: 'btn primary small', type: 'button', onclick: () => {
          const q = Number(nInput.value) || 5;
          chatAction({ action: 'quiz', n: q, difficulty: diff.value }, `Quiz de «${title}»: ${q} preguntas, ${diff.value}`);
        } }, 'Empezar'))),
    h('section', { class: 'p-card' },
      h('h3', {}, 'Explícamelo tú'),
      h('p', { class: 'hint' }, 'Me lo explicas como a alguien que no sabe nada. Te señalo los huecos y te pregunto lo que preguntaría un novato.'),
      h('button', { class: 'btn primary small', type: 'button', onclick: () => chatAction({ action: 'feynman' }, `Quiero explicar «${title}»`) }, 'Empezar a explicar')),
    flashcards(n),
    h('section', { class: 'p-card' },
      h('h3', {}, 'Simulación'),
      h('p', { class: 'hint' }, 'Una página interactiva para jugar con el tema, aparte de los visuales de la guía.'),
      n.hasSimulation
        ? h('button', { class: 'btn small', type: 'button', onclick: () => openSim(n.slug, title) }, 'Abrir simulación')
        : h('button', { class: 'btn small', type: 'button', onclick: () => chatAction({ action: 'interactive', brief: '' }, `Simulación de «${title}»`) }, 'Generar simulación'))));
}

// Flashcards from the concept notes: the name on the front, "En una frase" on the back. No model call.
function flashcards(n) {
  const list = (n.concepts || []).filter((c) => c.exists);
  const sec = h('section', { class: 'p-card' }, h('h3', {}, 'Repaso de conceptos'));
  if (!list.length) { sec.append(h('p', { class: 'hint' }, 'Cuando la guía tenga notas de concepto, aquí podrás repasarlas como tarjetas.')); return sec; }
  let i = 0, known = 0, seen = 0;
  const deck = [...list].sort(() => Math.random() - 0.5);
  const cache = new Map();
  const card = h('div', { class: 'flash' });
  const score = h('p', { class: 'hint' });
  const flip = () => { card.classList.remove('flip'); void card.offsetWidth; card.classList.add('flip'); };
  function show1() {
    const c = deck[i];
    card.replaceChildren(h('p', { class: 'eyebrow' }, `Tarjeta ${i + 1} de ${deck.length}`), h('h4', {}, c.title),
      h('button', { class: 'btn small yellow', type: 'button', onclick: reveal }, 'Dar la vuelta'));
    score.textContent = seen ? `Sabías ${known} de ${seen}.` : '';
    async function reveal() {
      let text = cache.get(c.slug);
      if (text == null) {
        try { const full = await api(`/api/notes/${encodeURIComponent(c.slug)}`); text = sectionOf(full.body, 'En una frase') || c.summary || ''; } catch { text = c.summary || ''; }
        cache.set(c.slug, text);
      }
      flip();
      card.replaceChildren(h('p', { class: 'eyebrow' }, c.title), mdBlock(text || '_Sin resumen._'),
        h('div', { class: 'actions' },
          h('button', { class: 'btn small green', type: 'button', onclick: () => next(true) }, 'Lo sabía'),
          h('button', { class: 'btn small', type: 'button', onclick: () => next(false) }, 'A repasar'),
          h('button', { class: 'btn small ghost', type: 'button', onclick: () => openNote(c.slug) }, 'Abrir la nota')));
    }
  }
  function next(ok) { seen++; if (ok) known++; i = (i + 1) % deck.length; flip(); show1(); }
  sec.append(h('p', { class: 'hint' }, 'Di la idea en una frase antes de dar la vuelta a la tarjeta.'), card, score);
  show1();
  return sec;
}

/* ---------- Mapa ---------- */
function cyStyle() {
  const ink = cssVar('--ink'), surface = cssVar('--surface'), muted = cssVar('--muted'), line = cssVar('--line');
  const lineOf = (i) => cssVar(i < 0 ? '--l-none' : `--l${i}`);
  const s = [
    { selector: 'node', style: {
      'background-color': surface, 'border-width': 3, 'border-color': lineOf(-1), shape: 'ellipse', width: 20, height: 20,
      label: 'data(label)', color: ink, 'font-size': 12.5, 'font-weight': 600, 'font-family': cssVar('--font'),
      'text-valign': 'bottom', 'text-margin-y': 7, 'text-wrap': 'wrap', 'text-max-width': 130,
      'text-background-color': surface, 'text-background-opacity': .85, 'text-background-padding': 2, 'text-background-shape': 'roundrectangle',
      'z-index-compare': 'manual', 'z-index': 10, 'overlay-opacity': 0, 'transition-property': 'opacity, background-color', 'transition-duration': '200ms' } },
    { selector: 'node[kind = "concept"]', style: { width: 12, height: 12, 'border-width': 2, 'font-size': 11.5, 'font-weight': 500 } },
    { selector: 'node[kind = "pending"]', style: { width: 10, height: 10, 'border-width': 1.5, 'border-style': 'dashed', 'border-color': muted, 'background-color': surface, color: muted, 'font-size': 11, 'font-weight': 400 } },
    { selector: 'edge', style: {
      width: 3, 'line-color': lineOf(-1), 'curve-style': 'bezier', 'line-cap': 'round', opacity: .75,
      'z-index-compare': 'manual', 'z-index': 1, 'overlay-opacity': 0 } },
    { selector: 'edge[kind="related"]', style: { 'line-style': 'dashed', width: 1.2, opacity: .45, 'line-color': muted } },
    { selector: 'edge[pend = 1]', style: { 'line-style': 'dotted', width: 1.5, 'line-color': line, opacity: 1 } },
    { selector: 'node.grouplabel', style: {
      width: 1, height: 1, 'background-opacity': 0, 'border-width': 0, label: 'data(label)', 'font-size': 15, 'font-weight': 700,
      color: ink, 'text-halign': 'right', 'text-valign': 'center', 'text-margin-x': 6, 'text-wrap': 'none', 'text-background-opacity': 0, events: 'no' } },
  ];
  for (let i = -1; i < LINES; i++) {
    const c = lineOf(i);
    s.push(
      { selector: `node[line = ${i}][kind != "pending"]`, style: { 'border-color': c } },
      { selector: `node[line = ${i}][kind = "concept"]`, style: { 'background-color': c } },
      { selector: `node[line = ${i}][status = "prepared"]`, style: { 'pie-size': '100%', 'pie-1-background-color': c, 'pie-1-background-size': 50, 'pie-1-background-opacity': 1 } },
      { selector: `node[line = ${i}][status = "tested"]`, style: { 'background-color': c } },
      { selector: `node[line = ${i}][status = "explained"]`, style: { 'background-color': c, 'outline-width': 3, 'outline-color': c, 'outline-offset': 3, 'outline-opacity': 1 } },
      { selector: `edge[kind = "requires"][line = ${i}]`, style: { 'line-color': c } },
      { selector: `node.grouplabel[line = ${i}]`, style: { color: c } },
    );
  }
  s.push(
    { selector: 'node:selected', style: { 'outline-width': 3, 'outline-color': cssVar('--accent'), 'outline-offset': 4, 'outline-opacity': 1 } },
    { selector: '.dim', style: { opacity: .1 } },
  );
  return s;
}

const GX = 180, GY = 62; // gap between layers, gap between stations
// Layered layout: each guide is a block, and prerequisites flow left to right into it.
function layeredLayout(g) {
  const pos = {}, labels = [];
  const groups = new Map();
  for (const n of [...g.nodes].sort((a, b) => (a.order || 0) - (b.order || 0))) {
    const k = n.group || '';
    if (!groups.has(k)) groups.set(k, []);
    groups.get(k).push(n);
  }
  const keys = [...groups.keys()].sort((a, b) => groups.get(b).length - groups.get(a).length);
  const prereqs = new Map();
  for (const e of g.edges) if (e.kind === 'requires') {
    if (!prereqs.has(e.to)) prereqs.set(e.to, []);
    prereqs.get(e.to).push(e.from);
  }
  let y0 = 0;
  for (const key of keys) {
    const nodes = groups.get(key);
    labels.push({ group: key, label: groupTitle(key), x: 0, y: y0 - 46 });
    const ids = new Set(nodes.map((n) => n.id));
    const layerOf = new Map(), busy = new Set();
    const depth = (id) => {
      if (layerOf.has(id)) return layerOf.get(id);
      if (busy.has(id)) return 0; // never loops forever on a cycle
      busy.add(id);
      let d = 0;
      for (const p of prereqs.get(id) || []) if (ids.has(p)) d = Math.max(d, depth(p) + 1);
      busy.delete(id);
      layerOf.set(id, d);
      return d;
    };
    nodes.forEach((n) => depth(n.id));
    const layers = [];
    nodes.forEach((n) => { (layers[layerOf.get(n.id)] ||= []).push(n); });
    const row = new Map();
    const bary = (n) => {
      const ps = (prereqs.get(n.id) || []).filter((p) => row.has(p));
      return ps.length ? ps.reduce((s, p) => s + row.get(p), 0) / ps.length : Infinity;
    };
    layers.forEach((l, li) => { if (!l) return; l.sort((a, b) => (li ? bary(a) - bary(b) : 0) || 0); l.forEach((n, i) => row.set(n.id, i)); });
    const tallest = Math.max(...layers.filter(Boolean).map((l) => l.length));
    layers.forEach((l, li) => l && l.forEach((n, i) => { pos[n.id] = { x: li * GX, y: y0 + ((tallest - l.length) * GY) / 2 + i * GY }; }));
    y0 += tallest * GY + 120;
  }
  return { pos, labels };
}

const graphSignature = (g) => g.nodes.map((n) => `${n.id}:${n.kind}`).sort().join('|') + '#' + g.edges.map((e) => `${e.from}>${e.to}`).sort().join('|');

async function loadGraph() {
  const [g, rms] = await Promise.all([api('/api/graph'), api('/api/roadmaps').catch(() => [])]);
  S.graph = g;
  S.roadmaps = rms;
  S.lines = assignLines(new Set(g.nodes.map((n) => n.group)));
  await drawMap();
}

// The map shows the chat that is open, or every chat: S.graph stays the whole vault either way.
S.mapScope = store.get('mapScope', 'chat');
async function drawMap() {
  if (!S.graph) return;
  const chatID = S.run && S.run.id;
  let g = S.graph;
  if (S.mapScope === 'chat') {
    g = chatID ? await api(`/api/graph?chat=${chatID}`).catch(() => ({ nodes: [], edges: [] })) : { nodes: [], edges: [] };
    if ((S.run && S.run.id) !== chatID) return; // the chat changed meanwhile
  }
  for (const b of $$('#map-scope button')) b.setAttribute('aria-pressed', String(b.dataset.scope === S.mapScope));
  const emptyChat = S.mapScope === 'chat' && !g.nodes.length;
  $('#map-empty').hidden = g.nodes.length > 0;
  $('#map-empty-title').textContent = emptyChat ? 'Esta conversación aún no tiene guías' : 'Tu mapa está vacío';
  $('#map-empty-text').textContent = emptyChat
    ? 'Cuando esta conversación tenga una guía o explique un concepto, aparecerán aquí con lo que hay debajo.'
    : 'Pide una guía en el chat. Cada guía y sus conceptos aparecerán aquí, conectados.';
  $('#map-see-all').hidden = !(emptyChat && S.graph.nodes.length);
  const sig = `${S.mapScope}:${chatID}:${graphSignature(g)}`;
  const same = S.mapSig === sig;
  S.mapSig = sig;
  const lineOf = (k) => (S.lines.has(k) ? S.lines.get(k) : -1);
  const lineOfNode = new Map(g.nodes.map((n) => [n.id, lineOf(n.group)]));
  const kindOf = new Map(g.nodes.map((n) => [n.id, n.kind]));
  if (!S.cy) {
    S.cy = cytoscape({ container: $('#cy'), elements: [], style: cyStyle(), minZoom: .15, maxZoom: 3 });
    S.cy.on('tap', 'node', (ev) => {
      const n = ev.target;
      if (n.hasClass('grouplabel')) return;
      if (n.data('kind') === 'pending') explainConcept(n.id(), { guide: (n.data('grp') || '').replace(/^g:/, '') || S.guide, title: n.data('label') });
      else openNote(n.id(), { quiet: false });
    });
    new ResizeObserver(() => fitMap()).observe($('#cy'));
  }
  if (same) {
    for (const n of g.nodes) S.cy.getElementById(n.id).data({ status: n.status, label: n.label, line: lineOfNode.get(n.id) });
  } else {
    S.cy.elements().remove();
    const { pos, labels } = layeredLayout(g);
    S.cy.add([
      ...g.nodes.map((n) => ({ group: 'nodes', data: { id: n.id, label: n.label, status: n.status, kind: n.kind, grp: n.group, line: lineOfNode.get(n.id) }, position: pos[n.id] })),
      ...labels.map((l) => ({ group: 'nodes', data: { id: `gl:${l.group}`, label: l.label, grp: l.group, line: lineOf(l.group) }, position: { x: l.x, y: l.y }, classes: 'grouplabel', selectable: false, grabbable: false })),
      ...g.edges.map((e, i) => ({ group: 'edges', data: { id: `e${i}`, source: e.from, target: e.to, kind: e.kind, line: lineOfNode.get(e.to), pend: kindOf.get(e.from) === 'pending' || kindOf.get(e.to) === 'pending' ? 1 : 0 } })),
    ]);
    S.needsFit = g.nodes.length > 0;
    fitMap();
  }
  S.mapGroups = new Set(g.nodes.map((n) => n.group));
  if (S.filter && !S.mapGroups.has(S.filter)) S.filter = '';
  renderChips();
  applyFilters();
}
for (const b of $$('#map-scope button')) {
  b.addEventListener('click', () => { S.mapScope = b.dataset.scope; store.set('mapScope', S.mapScope); drawMap(); });
}
$('#map-see-all').addEventListener('click', () => { S.mapScope = 'all'; store.set('mapScope', 'all'); drawMap(); });

// Cytoscape cannot fit a graph into a hidden panel, so a new layout waits until the map is on screen.
function fitMap() {
  if (!S.cy || !isShown('map')) return;
  requestAnimationFrame(() => {
    S.cy.resize();
    if (S.needsFit && $('#cy').clientWidth > 0) { S.cy.fit(undefined, 40); S.needsFit = false; }
  });
}

function renderChips() {
  const box = $('#filter-rm');
  box.replaceChildren();
  const keys = [...S.lines.keys()].filter((k) => !k.startsWith('c:') && (!S.mapGroups || S.mapGroups.has(k)));
  if (keys.length < 2) return;
  const chip = (key, label) => h('button', {
    class: `line-chip${key ? '' : ' all'}`, type: 'button', 'aria-pressed': String(S.filter === key),
    style: key ? `--lc: ${lineColor(key)}` : null,
    onclick: () => { S.filter = S.filter === key ? '' : key; renderChips(); applyFilters(); },
  }, label);
  box.append(chip('', 'Todas'), ...keys.map((k) => chip(k, groupTitle(k))));
}

function applyFilters() {
  if (!S.cy) return;
  const q = slugify($('#search').value);
  S.cy.batch(() => {
    S.cy.nodes().not('.grouplabel').forEach((n) => {
      const okQ = !q || slugify(n.data('label')).includes(q) || n.id().includes(q);
      const okR = !S.filter || n.data('grp') === S.filter;
      n.toggleClass('dim', !(okQ && okR));
    });
    S.cy.edges().forEach((e) => e.toggleClass('dim', e.source().hasClass('dim') || e.target().hasClass('dim')));
  });
}
$('#search').addEventListener('input', applyFilters);

/* ---------- chat ---------- */
const T = { raw: '', bubble: null, timer: null, auto: true, progress: null, plan: null, echo: null, choiceCard: null, anim: null };

// lists that move get AutoAnimate once it has loaded (an ES module, so it may come after this script)
function withAutoAnimate(fn) {
  if (window.autoAnimate) fn(window.autoAnimate); else window.addEventListener('autoanimate-ready', () => fn(window.autoAnimate), { once: true });
}
withAutoAnimate((aa) => {
  T.anim = aa($('#transcript'), { duration: 220 });
  aa($('#chat-list'), { duration: 200 });
});

function scrollDown() {
  const t = $('#transcript');
  if (T.auto) t.scrollTop = t.scrollHeight;
}
$('#transcript').addEventListener('scroll', () => {
  const t = $('#transcript');
  T.auto = t.scrollHeight - t.scrollTop - t.clientHeight < 80;
});

function resetTranscript() {
  clearTimeout(T.timer);
  T.anim?.disable(); // a new conversation replaces the old one at once, without every message flying out
  $('#transcript').replaceChildren();
  Object.assign(T, { raw: '', bubble: null, progress: null, plan: null, choiceCard: null, echo: null, auto: true });
  requestAnimationFrame(() => T.anim?.enable());
}

function emptyState() {
  return h('div', { class: 'chat-empty' },
    h('p', { class: 'eyebrow' }, 'StudyHomie'),
    h('h1', {}, Phrases.welcome()),
    h('p', { class: 'lead' }, 'Pregunta lo que sea o pide una guía: te explico el tema y lo que da por sabido, hasta lo básico.'),
  );
}

// the + of the composer: the actions that start something bigger than a question
function closePlus() { $('#plus-menu').hidden = true; $('#btn-plus').setAttribute('aria-expanded', 'false'); }
function dailyForm() {
  // today's challenge: an optional theme; empty draws from your interests
  const field = h('input', { type: 'text', name: 'field', list: 'daily-fields', autocomplete: 'off', placeholder: 'Cualquier tema, o vacío para sorprenderte', 'aria-label': 'Temática del reto' });
  const form = h('form', { class: 'daily-pick', onsubmit: (e) => {
    e.preventDefault();
    const f = field.value.trim();
    closePlus();
    chatAction({ action: 'daily', field: f }, f ? `El reto de hoy, sobre ${f}` : 'El reto de hoy');
  } },
    h('label', {}, 'Reto de hoy sobre'),
    field,
    h('datalist', { id: 'daily-fields' }, (S.info?.interests || []).map((i) => h('option', { value: i }))),
    h('button', { class: 'btn small primary', type: 'submit' }, 'Sortear'));
  $('#plus-menu').replaceChildren(form);
  field.focus();
}
$('#btn-plus').addEventListener('click', (e) => {
  e.stopPropagation();
  const menu = $('#plus-menu');
  if (!menu.hidden) { closePlus(); return; }
  menu.replaceChildren(
    h('button', { type: 'button', onclick: (ev) => { ev.stopPropagation(); dailyForm(); } }, icon('sparkles'), 'El reto de hoy'),
    h('button', { type: 'button', onclick: () => { closePlus(); askAbout('Quiero aprender toda una materia: '); } }, icon('map'), 'Planear una materia'),
    h('button', { type: 'button', onclick: () => { closePlus(); $('#material-input').click(); } }, icon('file'), 'Subir apuntes'));
  menu.hidden = false;
  $('#btn-plus').setAttribute('aria-expanded', 'true');
});

function botText(text) {
  T.raw += text;
  if (!T.bubble) {
    T.bubble = h('div', { class: 'msg bot streaming' });
    $('#transcript').append(T.bubble);
  }
  clearTimeout(T.timer);
  T.timer = setTimeout(() => flushBot(true), 60);
}
function flushBot(streaming) {
  clearTimeout(T.timer);
  if (!T.bubble) return;
  T.bubble.classList.toggle('streaming', streaming);
  T.bubble.replaceChildren(mdBlock(T.raw, { breaks: true, streaming }));
  scrollDown();
}
// the bubble is complete: drawn once more with its widgets
function closeBubble() {
  if (!T.bubble) return;
  if (T.raw.trim()) flushBot(false); else T.bubble.remove();
  T.raw = ''; T.bubble = null;
}

function addMsg(cls, content) {
  closeBubble();
  $('.chat-empty')?.remove();
  const m = h('div', { class: `msg ${cls}` });
  if (typeof content === 'string') m.textContent = content; else m.append(content);
  $('#transcript').append(m);
  scrollDown();
  return m;
}

let workingTimer;
function setWorking(on, text) {
  $('#working').hidden = !on;
  if (text) $('#working-text').textContent = text;
  // a long wait reads better when the phrase changes now and then
  clearInterval(workingTimer);
  if (on && Phrases.has('thinking', $('#working-text').textContent)) {
    workingTimer = setInterval(() => { $('#working-text').textContent = Phrases.pick('thinking'); }, 6000);
  }
  $('#btn-cancel').hidden = !on;
  $('#session-dot').hidden = !on;
}

// Questions with a closed set of answers are asked with buttons instead of a text box.
function quickOptions(e) {
  const label = e.label || '';
  if (e.kind !== 'line') return null;
  if (/\(s\/n\)\s*$/i.test(label)) {
    const [yes, no] = /aceptas/i.test(label) ? ['Aceptar', 'Otro tema']
      : /respondemos|comprobamos|prueba/i.test(label) ? ['Responder preguntas', 'Ahora no']
        : /explicas|feynman/i.test(label) ? ['Explicar', 'Terminar aquí']
          : /genero/i.test(label) ? ['Generar', 'Omitir']
            : /empezamos/i.test(label) ? ['Empezar', 'Ahora no'] : ['Sí', 'No'];
    return [{ label: yes, reply: 's', primary: true }, { label: no, reply: 'n' }];
  }
  if (/dificultad/i.test(label)) return ['básica', 'media', 'avanzada'].map((d) => ({ label: d[0].toUpperCase() + d.slice(1), reply: d, primary: d === 'media' }));
  if (/cuántas preguntas/i.test(label)) return [3, 5, 8, 10].map((n) => ({ label: `${n} preguntas`, reply: String(n), primary: n === 5 }));
  return null;
}

// The composer follows what the run waits for: a chat message, a short answer, an explanation, or nothing.
function setComposer(mode, e = {}) {
  const form = $('#composer'), ta = $('#reply');
  form.dataset.kind = mode;
  const label = (e.label || '').replace(/\s*\(s\/n\)\s*$/i, '').trim();
  const opts = mode === 'line' ? quickOptions(e) : null;
  $('#composer-label').textContent = mode === 'line' ? label : '';
  $('#composer-label').hidden = mode !== 'line' || !label;
  $('#quick').replaceChildren(...(opts || []).map((o) => h('button', {
    type: 'button', class: `btn${o.primary ? ' primary' : ''}`, onclick: () => { T.echo = o; sendReply(o.reply); },
  }, o.label)));
  $('#quick').hidden = !opts;
  $('#freeform').hidden = !!opts;
  $('#depth').hidden = mode !== 'chat';
  $('#btn-attach').hidden = mode !== 'chat';
  $('#btn-plus').hidden = mode !== 'chat';
  if (mode !== 'chat') closePlus();
  $('#btn-finish').hidden = mode !== 'block';
  ta.disabled = $('#btn-send').disabled = mode === 'busy';
  ta.rows = mode === 'block' ? 6 : 2;
  ta.placeholder = {
    chat: 'Pregunta lo que quieras, o pide una guía: «Explícame la transformada de Fourier, enfocada en audio»',
    line: e.def ? `Enter para usar «${e.def}»` : 'Escribe tu respuesta',
    block: 'Explícalo con tus palabras, como si el otro no supiera nada…',
    busy: 'Espera a que termine, o detenlo.',
  }[mode];
  $('#composer-hint').textContent = mode === 'block' ? 'Ctrl + Enter para enviar' : '';
  if (mode !== 'busy' && !isPhone() && ta.isConnected) (opts ? $('#quick .primary') : ta)?.focus({ preventScroll: true });
}

// What the learner sends goes to the live chat, or starts one (continuing the chat on screen, if any).
async function chatSend(body) {
  const run = S.run;
  body = { depth: S.depth, guide: S.guide || '', ...body };
  if (run && !run.done && run.waiting === 'chat') {
    run.waiting = '';
    setComposer('busy');
    setWorking(true, Phrases.pick('thinking'));
    try { await api(`/api/runs/${run.id}/input`, { method: 'POST', body }); return true; } catch (err) {
      toast(err.message); run.waiting = 'chat'; setComposer('chat'); setWorking(false); return false;
    }
  }
  if (run && !run.done) { toast('Espera a que termine lo que está en curso, o detenlo.'); return false; }
  try {
    const req = { cmd: 'chat', chat: body, guide: S.guide || '' };
    if (run && run.done) req.resume = run.id;
    setComposer('busy');
    setWorking(true, Phrases.pick('thinking'));
    const { id } = await api('/api/runs', { method: 'POST', body: req });
    attachRun(id, run && run.done ? run.title : null);
    loadChats();
    return true;
  } catch (err) { toast(err.message); setComposer('chat'); setWorking(false); return false; }
}

// a button that asks the chat to do something, without going through the model
function chatAction(action, label) {
  show('chat');
  chatSend({ text: '', action, label });
}

async function sendReply(text, finish = false) {
  const run = S.run;
  if (!run || run.done) return;
  run.waiting = '';
  setComposer('busy');
  setWorking(true, Phrases.pick('thinking'));
  try { await api(`/api/runs/${run.id}/input`, { method: 'POST', body: { text, finish } }); } catch (err) { toast(err.message); setWorking(false); }
}

$('#composer').addEventListener('submit', async (e) => {
  e.preventDefault();
  const ta = $('#reply');
  const mode = $('#composer').dataset.kind || 'chat';
  const text = ta.value;
  if (mode === 'busy') return;
  if (mode === 'chat') {
    if (!text.trim() && !STAGED.length) return;
    const files = takeStaged();
    if (!files) return;
    ta.value = '';
    if (!(await chatSend(files.length ? { text, files } : { text }))) ta.value = text;
    return;
  }
  if (mode === 'block' && !text.trim()) { toast('Escribe tu explicación antes de enviar.'); return; }
  ta.value = '';
  sendReply(text, false);
});
$('#reply').addEventListener('keydown', (e) => {
  if (e.key !== 'Enter' || e.isComposing) return;
  const block = $('#composer').dataset.kind === 'block';
  if (block ? (e.ctrlKey || e.metaKey) : !e.shiftKey) { e.preventDefault(); $('#composer').requestSubmit(); }
});
$('#btn-finish').addEventListener('click', () => { const t = $('#reply').value; $('#reply').value = ''; sendReply(t, true); });
$('#btn-cancel').addEventListener('click', async () => {
  if (!S.run || S.run.done) return;
  try { await api(`/api/runs/${S.run.id}/cancel`, { method: 'POST', body: {} }); } catch (err) { toast(err.message); }
});

// Attachments of a message: picked, pasted or dropped, uploaded at once and sent with the next message.
const STAGED = []; // { name, id (upload id, once uploaded), image, url (local preview) }
function drawStaged() {
  const ul = $('#staged');
  ul.hidden = !STAGED.length;
  ul.replaceChildren(...STAGED.map((f) => h('li', { class: f.id ? null : 'uploading' },
    f.url ? h('img', { src: f.url, alt: '' }) : icon('file'),
    h('span', {}, f.name),
    h('button', { type: 'button', class: 'icon-btn', title: `Quitar ${f.name}`, 'aria-label': `Quitar ${f.name}`, onclick: () => {
      STAGED.splice(STAGED.indexOf(f), 1);
      if (f.url) URL.revokeObjectURL(f.url);
      drawStaged();
    } }, icon('x')))));
}
async function stageFiles(files) {
  const list = [...files];
  if (!list.length) return;
  const items = list.map((f) => ({ name: f.name || 'imagen.png', image: f.type.startsWith('image/'), url: f.type.startsWith('image/') ? URL.createObjectURL(f) : '' }));
  STAGED.push(...items);
  drawStaged();
  const fd = new FormData();
  list.forEach((f, i) => fd.append('files', f, items[i].name));
  try {
    const up = await api('/api/upload', { method: 'POST', body: fd });
    up.files.forEach((id, i) => { items[i].id = id; });
  } catch (err) {
    toast(err.message);
    for (const it of items) STAGED.splice(STAGED.indexOf(it), 1);
  }
  drawStaged();
}
function takeStaged() {
  if (STAGED.some((f) => !f.id)) { toast('Espera a que terminen de subir los adjuntos.'); return null; }
  const ids = STAGED.map((f) => f.id);
  for (const f of STAGED) if (f.url) URL.revokeObjectURL(f.url);
  STAGED.length = 0;
  drawStaged();
  return ids;
}
$('#btn-attach').addEventListener('click', () => $('#attach-input').click());
$('#attach-input').addEventListener('change', (e) => { stageFiles(e.target.files); e.target.value = ''; });
$('#reply').addEventListener('paste', (e) => {
  if (e.clipboardData.files.length && ($('#composer').dataset.kind || 'chat') === 'chat') { e.preventDefault(); stageFiles(e.clipboardData.files); }
});
$('#composer').addEventListener('dragover', (e) => { if (e.dataTransfer.types.includes('Files')) { e.preventDefault(); $('.composer-box').classList.add('drop'); } });
$('#composer').addEventListener('dragleave', (e) => { if (!e.currentTarget.contains(e.relatedTarget)) $('.composer-box').classList.remove('drop'); });
$('#composer').addEventListener('drop', (e) => {
  $('.composer-box').classList.remove('drop');
  if (e.dataTransfer.files.length) { e.preventDefault(); stageFiles(e.dataTransfer.files); }
});

// "Subir apuntes": the files are study material, and the chat builds a study line from them
$('#material-input').addEventListener('change', async (e) => {
  const files = e.target.files;
  if (!files.length) return;
  const fd = new FormData();
  for (const f of files) fd.append('files', f);
  try {
    setWorking(true, Phrases.pick('uploading'));
    const up = await api('/api/upload', { method: 'POST', body: fd });
    await chatSend({ text: '', files: up.files, material: true, label: `Mis apuntes: ${[...files].map((f) => f.name).join(', ')}` });
  } catch (err) { toast(err.message); setWorking(false); } finally { e.target.value = ''; }
});

// "write now": 0 (the guide alone) to 4 (down to the basics); the rest is mapped either way
function renderDepth() {
  $('#depth-stations').replaceChildren(...DEPTH.map((label, d) => h('button', {
    type: 'button', role: 'radio', 'aria-checked': String(d === S.depth), class: d <= S.depth ? 'on' : null,
    title: `${d}: ${label}`, 'aria-label': `Escribir ahora: ${label}`,
    onclick: () => { S.depth = d; store.set('depth', d); renderDepth(); },
  }, String(d))));
  $('#depth-text').textContent = DEPTH[S.depth];
}

/* ---------- cards: structured output drawn as elements ---------- */
function scoreTone(score) { return score >= 70 ? 'good' : score >= 40 ? 'mid' : 'low'; }

function renderCard(c, run) {
  switch (c.kind) {
    case 'phase': break;
    case 'status': if (!run.replay && !run.done) setWorking(true, c.detail); break;
    case 'plan': T.plan = planCard(c.plan, run); addMsg('card', T.plan.el); break;
    case 'progress': progressCard(c.items); break;
    case 'guide':
      T.progress = null;
      addMsg('card', h('div', { class: 'rich rich-guide' },
        h('div', { class: 'rich-eyebrow' }, 'Guía lista'),
        h('h3', {}, c.topic),
        h('div', { class: 'actions' },
          h('button', { class: 'btn small primary', type: 'button', onclick: () => openNote(c.slug) }, icon('book-open'), 'Leer la guía'),
          h('button', { class: 'btn small', type: 'button', onclick: async () => { await openNote(c.slug, { quiet: true }); show('concepts'); } }, icon('mountain-snow'), 'Ver el iceberg'))));
      if (!run.replay) loadGraph().catch(() => {}).then(() => openNote(c.slug));
      break;
    case 'concept':
      addMsg('card', h('div', { class: 'rich' }, h('div', { class: 'rich-eyebrow' }, 'Nota de concepto'), h('h3', {}, c.topic),
        h('button', { class: 'btn small', type: 'button', onclick: () => openNote(c.slug) }, 'Abrir la nota')));
      if (!run.replay) loadGraph().catch(() => {}).then(() => reloadGuide());
      break;
    case 'daily':
      addMsg('card', h('div', { class: 'rich' },
        h('div', { class: 'rich-eyebrow' }, 'Para descubrir hoy', c.field && h('span', { class: 'chip' }, c.field)),
        h('h3', {}, c.topic), c.hook && h('p', { class: 'hint' }, c.hook)));
      break;
    case 'question': addMsg('card', questionCard(c, run)); break;
    case 'score': {
      const tone = scoreTone(c.score);
      const verdict = tone === 'good' ? 'Lo entendiste bien' : tone === 'mid' ? 'Vas por buen camino' : 'Buen punto de partida: quedan cosas por aclarar';
      addMsg('card', h('div', { class: `rich rich-score tone-${tone}` },
        h('div', { class: 'score-ring', style: `--p:${Math.max(0, Math.min(100, c.score))}`, role: 'img', 'aria-label': `${c.score} de 100` }, h('span', {}, c.score)),
        h('div', {}, h('strong', {}, `${c.label}: ${verdict}`), c.detail && h('div', { class: 'hint' }, c.detail))));
      if (!run.replay) reloadGuide();
      break;
    }
    case 'resources':
      addMsg('card', h('details', { class: 'rich rich-resources' },
        h('summary', { class: 'rich-eyebrow' }, `Para seguir explorando (${(c.resources || []).length})`),
        h('ul', { class: 'res-list' }, (c.resources || []).map((r) => resourceCard(r)))));
      break;
  }
}

// The plan: what gets a note now, and the part under water, mapped and left to explain on demand.
function planCard(p, run) {
  const byLevel = (list) => { const out = []; for (const c of list || []) (out[c.level - 1] ||= []).push(c); return out; };
  const above = byLevel(p.concepts), below = byLevel(p.pending);
  const boxes = new Map(), promote = new Map();
  const added = [];
  const addList = h('ul', { class: 'plan-added' });
  const addInput = h('input', { type: 'text', maxlength: 80, placeholder: 'Añadir un concepto…', 'aria-label': 'Añadir un concepto' });
  const add = () => {
    const t = addInput.value.trim();
    if (!t) return;
    added.push(t);
    addList.append(h('li', {}, `+ ${t}`));
    addInput.value = '';
    update();
  };
  addInput.addEventListener('keydown', (e) => { if (e.key === 'Enter') { e.preventDefault(); add(); } });
  const count = h('p', { class: 'hint' });
  const go = h('button', { class: 'btn primary', type: 'button' });
  const fresh = (p.concepts || []).filter((c) => !c.exists);
  function update() {
    const kept = fresh.filter((c) => boxes.get(c.id).checked).length + [...promote.values()].filter((cb) => cb.checked).length + added.length;
    go.textContent = kept ? `Escribir la guía y ${kept} ${kept === 1 ? 'nota' : 'notas'}` : 'Escribir solo la guía';
    const linked = (p.concepts || []).filter((c) => c.exists && boxes.get(c.id).checked).length;
    const under = (p.pending || []).length - [...promote.values()].filter((cb) => cb.checked).length;
    count.textContent = [linked ? `${linked} ya ${linked === 1 ? 'está' : 'están'} en tu vault: se enlazan.` : '',
      under > 0 ? `${under} quedan bajo el agua: en gris, para explicar cuando quieras.` : ''].filter(Boolean).join(' ');
  }
  const item = (c, checked, map, underWater) => {
    const cb = h('input', { type: 'checkbox', checked: checked || null });
    cb.addEventListener('change', update);
    map.set(c.id, cb);
    return h('li', {}, h('label', { class: `plan-item${c.exists ? ' exists' : ''}${underWater ? ' under' : ''}` }, cb,
      h('span', {}, h('strong', {}, c.title), c.exists ? h('em', {}, ' · ya en tu vault') : null, c.summary ? h('small', {}, c.summary) : null)));
  };
  const levelRows = (levels, under) => levels.map((lv, i) => lv && h('li', { class: under ? 'under' : null },
    h('span', { class: 'plan-level' }, i === 0 ? 'Directo' : `Nivel ${i + 1}`),
    h('ul', {}, lv.map((c) => item(c, !under, under ? promote : boxes, under)))));
  const el = h('div', { class: 'rich rich-plan' },
    h('div', { class: 'rich-eyebrow' }, icon('mountain-snow'), `Plan · escribir ahora ${p.depth} ${p.depth === 1 ? 'nivel' : 'niveles'}`),
    h('h3', {}, p.topic),
    h('p', { class: 'hint' }, 'Desmarca lo que ya sabes; marca algo de abajo si lo quieres escrito ya.'),
    h('ol', { class: 'plan-levels' }, levelRows(above, false)),
    (p.pending || []).length ? h('p', { class: 'plan-water' }, 'Bajo el agua: mapeado, sin escribir') : null,
    (p.pending || []).length ? h('ol', { class: 'plan-levels' }, levelRows(below, true)) : null,
    addList,
    h('div', { class: 'plan-add' }, addInput, h('button', { class: 'btn small', type: 'button', onclick: add }, icon('plus'), 'Añadir')),
    h('div', { class: 'actions' }, go,
      h('button', { class: 'btn', type: 'button', onclick: () => answer('g', 'Solo la guía') }, 'Solo la guía'),
      h('button', { class: 'btn ghost small', type: 'button', onclick: () => answer('n', 'Cancelar') }, 'Cancelar')),
    count);
  const state = { el, live: false, answered: false };
  // once answered the plan folds to its title: what matters next is the guide being written
  const fold = () => {
    el.classList.add('answered');
    el.querySelector('.rich-eyebrow').after(h('button', { class: 'btn small ghost plan-toggle', type: 'button',
      onclick: (e) => { const open = el.classList.toggle('expanded'); e.currentTarget.textContent = open ? 'Ocultar el plan' : 'Ver el plan'; } }, 'Ver el plan'));
  };
  function answer(reply, label) {
    if (state.answered || S.run !== run || run.done || !state.live) return;
    state.answered = true;
    $$('button, input', el).forEach((x) => { x.disabled = true; });
    fold();
    T.echo = { reply, label };
    sendReply(reply);
  }
  go.addEventListener('click', () => {
    const skip = [...boxes].filter(([, cb]) => !cb.checked).map(([id]) => id);
    const write = [...promote].filter(([, cb]) => cb.checked).map(([id]) => id);
    if (!skip.length && !added.length && !write.length) { answer('s', go.textContent); return; }
    answer(JSON.stringify({ skip, add: added, write, go: true }), go.textContent);
  });
  update();
  if (run.replay) { $$('button, input', el).forEach((x) => { x.disabled = true; }); fold(); }
  return state;
}

// "Listo · guía y 2 notas": what a finished progress card folds to
function writtenSummary(items) {
  const guide = items.some((i) => i.kind === 'guide');
  const notes = items.filter((i) => i.kind !== 'guide').length;
  const parts = [guide ? 'guía' : '', notes ? `${notes} ${notes === 1 ? 'nota' : 'notas'}` : ''].filter(Boolean);
  return `Listo · ${parts.join(' y ')}`;
}

// One card per guide being written, redrawn in place as each note finishes.
function progressCard(items) {
  const state = { pending: 'en cola', working: 'escribiendo…', done: 'lista', linked: 'ya estaba', error: 'falló' };
  const done = items.filter((i) => i.state !== 'pending' && i.state !== 'working').length;
  const failed = items.filter((i) => i.state === 'error').length;
  const list = h('ul', { class: 'progress-list' }, items.map((it) => h('li', { 'data-state': it.state, class: it.kind },
    h('span', { class: 'p-dot', 'aria-hidden': 'true' }),
    h('span', { class: 'p-title' }, it.title, it.kind === 'guide' ? h('small', {}, ' · guía') : null),
    h('small', { class: 'p-state' }, state[it.state] || it.state))));
  // once everything is written the card folds to one line; it stays open if something failed
  const body = done === items.length
    ? [h('details', { open: failed ? true : null },
      h('summary', { class: 'rich-eyebrow' }, icon(failed ? 'x' : 'check'),
        failed ? `Escrito · ${failed} ${failed === 1 ? 'falló' : 'fallaron'}` : writtenSummary(items)),
      list)]
    : [h('div', { class: 'rich-eyebrow' }, `Escribiendo · ${done} de ${items.length}`),
      h('div', { class: 'bar' }, h('i', { style: `width:${Math.round((100 * done) / items.length)}%` })),
      list];
  if (T.progress && T.progress.isConnected) { T.progress.replaceChildren(...body); return; }
  T.progress = h('div', { class: 'rich rich-progress' }, ...body);
  addMsg('card', T.progress);
}

function questionCard(c, run) {
  const card = h('div', { class: 'rich rich-question' },
    h('div', { class: 'rich-eyebrow' }, c.total ? `Pregunta ${c.n} de ${c.total}` : 'Pregunta'),
    h('p', { class: 'q-text' }, c.question));
  if (!c.choices || !c.choices.length) return card;
  const state = { answered: false, buttons: [] };
  const list = h('div', { class: 'choices', role: 'group', 'aria-label': 'Opciones' });
  for (const choice of c.choices) {
    const b = h('button', { type: 'button', class: 'choice', disabled: run.replay || null, onclick: () => {
      if (state.answered || S.run !== run || run.done) return;
      state.answered = true;
      b.classList.add('picked');
      state.buttons.forEach((x) => { x.disabled = true; });
      sendReply(choice);
    } }, choice);
    state.buttons.push(b);
    list.append(b);
  }
  card.append(list);
  T.choiceCard = state;
  return card;
}

// once the learner has answered, or the run is over, earlier buttons are no longer live
function closeChoices() {
  if (T.choiceCard) { T.choiceCard.answered = true; T.choiceCard.buttons.forEach((x) => { x.disabled = true; }); }
  if (T.plan && !T.plan.answered) { T.plan.answered = true; $$('button, input', T.plan.el).forEach((x) => { x.disabled = true; }); }
}

/* ---------- runs ---------- */
// Shows a chat: a live one streams in, a stored one replays from its first event.
function attachRun(id, title, { replay = false } = {}) {
  if (S.run && S.run.es) S.run.es.close();
  resetTranscript();
  const run = { id, done: false, replay, waiting: '', title: title || 'Chat', es: new EventSource(`/api/runs/${id}/events?from=0`) };
  S.run = run;
  $('#chat-title').textContent = run.title;
  if (S.mapScope === 'chat') drawMap();
  setComposer('busy');
  setWorking(!replay, Phrases.pick('thinking'));
  run.es.onmessage = (ev) => handleEvent(run, JSON.parse(ev.data));
  run.es.onerror = () => { if (run.done) run.es.close(); }; // otherwise the browser reconnects with Last-Event-ID
  markActiveChat();
}

function handleEvent(run, e) {
  if (run !== S.run) return;
  switch (e.type) {
    case 'out': $('.chat-empty')?.remove(); botText(e.text); break;
    case 'in': {
      closeChoices();
      // a button answers with a short code ("s", "media"); show what the learner actually clicked
      const known = (T.echo && T.echo.reply === e.text && T.echo) || ((run.lastAsk && quickOptions(run.lastAsk)) || []).find((o) => o.reply === e.text);
      T.echo = null;
      const shown = known ? known.label : /^\{.*"go":true/.test(e.text) ? 'Escribir con estos cambios' : e.text;
      addMsg('user', mdBlock(shown, { breaks: true }));
      if (!run.replay) setWorking(true, Phrases.pick('thinking'));
      break;
    }
    case 'card': closeBubble(); renderCard(e.card, run); break;
    case 'ask':
      closeBubble();
      run.lastAsk = e;
      run.waiting = e.kind;
      if (run.replay) break;
      setWorking(false);
      if (e.kind === 'line' && T.plan && !T.plan.answered && /genero la guía/i.test(e.label || '')) {
        T.plan.live = true;
        setComposer('busy');
        $('#reply').placeholder = 'Elige en el plan de arriba.';
        T.plan.el.scrollIntoView({ behavior: smoothness(), block: 'nearest' });
        break;
      }
      if (e.kind === 'line' && T.choiceCard && !T.choiceCard.answered && /^respuesta$/i.test(e.label || '')) {
        setComposer('busy');
        $('#reply').placeholder = 'Elige una opción de la pregunta.';
        break;
      }
      setComposer(e.kind, e);
      break;
    case 'done': finishRun(run, e); break;
  }
}

function finishRun(run, e) {
  run.done = true;
  run.waiting = '';
  run.es.close();
  closeBubble();
  closeChoices();
  setWorking(false);
  if (e.error) addMsg('bot error', e.error);
  if (!$('#transcript').children.length) $('#transcript').append(emptyState());
  setComposer('chat'); // writing again continues this chat
  if (!run.replay) { loadChats(); refresh(); }
}

/* ---------- chat history ---------- */
const when = (iso) => {
  const d = new Date(iso), now = new Date();
  return d.toDateString() === now.toDateString() ? d.toLocaleTimeString('es', { timeStyle: 'short' }) : d.toLocaleDateString('es', { day: 'numeric', month: 'short' });
};
async function loadChats() {
  try { S.chats = await api('/api/sessions'); } catch { S.chats = []; }
  renderChats();
}
function renderChats() {
  const ul = $('#chat-list');
  const q = slugify($('#chat-search').value);
  ul.replaceChildren();
  const list = S.chats.filter((r) => !q || slugify(r.title || '').includes(q));
  if (!list.length) { ul.append(h('li', { class: 'hint' }, S.chats.length ? 'Ninguna conversación coincide.' : 'Tus conversaciones aparecerán aquí.')); return; }
  for (const r of list) {
    const node = r.slug && nodeOf(r.slug);
    ul.append(h('li', { class: `chat-item${S.run && S.run.id === r.id ? ' on' : ''}`, 'data-id': r.id, style: node ? `--lc: ${lineColor(node.group)}` : null },
      h('button', { class: 'ci-open', type: 'button', onclick: () => openChat(r) },
        h('span', { class: 'ci-title' }, r.title || 'Chat'),
        h('span', { class: 'ci-meta' }, r.status === 'running' ? 'En curso' : when(r.started), node ? ` · ${node.label}` : '')),
      r.status !== 'running' && h('button', { class: 'ci-del', type: 'button', 'aria-label': `Eliminar «${r.title}»`, title: 'Eliminar', onclick: async () => {
        if (!confirm(`¿Eliminar la conversación «${r.title}»? Tus notas se conservan.`)) return;
        try {
          await api(`/api/sessions/${r.id}`, { method: 'DELETE' });
          if (S.run && S.run.id === r.id) newChat();
          loadChats();
        } catch (err) { toast(err.message); }
      } }, icon('trash-2'))));
  }
}
$('#chat-search').addEventListener('input', renderChats);
function markActiveChat() { $$('.chat-item', PANE_EL.chats || document).forEach((li) => li.classList.toggle('on', !!S.run && String(S.run.id) === li.dataset.id)); }

function openChat(r) {
  attachRun(r.id, r.title, { replay: r.status !== 'running' });
  if (r.slug && r.slug !== S.guide && nodeOf(r.slug)) openNote(r.slug, { quiet: true });
  show('chat');
}

function newChat() {
  if (S.run && S.run.es) S.run.es.close();
  S.run = null;
  if (S.mapScope === 'chat') drawMap();
  resetTranscript();
  $('#transcript').append(emptyState());
  $('#chat-title').textContent = 'Nueva conversación';
  setWorking(false);
  setComposer('chat');
  markActiveChat();
}
$('#btn-new').addEventListener('click', () => { newChat(); show('chat'); });
$('#brand').addEventListener('click', (e) => { e.preventDefault(); newChat(); show('chat'); });

/* ---------- boot ---------- */
async function refresh() {
  await loadGraph().catch((err) => toast(err.message));
  if (S.guide) await reloadGuide(); else { renderReader(null); renderConcepts(); renderPractice(); }
  renderChats();
}

async function boot() {
  createDock();
  applyTheme();
  renderDepth();
  newChat();
  try {
    S.info = await api('/api/state');
    $('#btn-vault').title = `Vault: ${String(S.info.vault || '').split(/[\\/]/).filter(Boolean).pop() || 'vault'} (tus notas y tus vaults)`;
    $('#provider-badge').textContent = S.info.provider;
    if (S.info.warning) { $('#banner').textContent = S.info.warning; $('#banner').hidden = false; }
    checkUpdate();
    setInterval(checkUpdate, 60 * 60 * 1000);
    await refresh();
    await loadChats();
    if (S.info.activeRun) {
      const me = S.chats.find((r) => r.id === S.info.activeRun);
      attachRun(S.info.activeRun, me ? me.title : 'Chat');
    }
    // a link straight to a note: #open/<slug>[/<panel>]
    const m = /^#open\/([^/]+)(?:\/(guide|concepts|map|practice))?$/.exec(location.hash);
    if (m) {
      await openNote(decodeURIComponent(m[1]), { quiet: !!m[2] });
      if (m[2]) show(m[2]);
    }
  } catch (err) { toast(err.message); }
}
boot();
