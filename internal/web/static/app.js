'use strict';

/* ---------- language ---------- */
// i18n.js (served by the app) runs before this script: window.I18N holds the page's language and
// its catalog, and t() translates. Messages are written in Spanish and the Spanish text is the key.
const LANG = (window.I18N && window.I18N.lang) || 'es';
const LOCALE = (window.I18N && window.I18N.locale) || 'es';
document.documentElement.lang = LOCALE;
// t(msg, vars) and tn(n, one, many) come with i18n.js
// the same section of a note in any language: "Guide" is "Guía"
const HEADINGS = (window.I18N && window.I18N.headings) || {};
function sectionKey(heading) {
  const h = heading.trim();
  for (const [k, hs] of Object.entries(HEADINGS)) if (hs.includes(h)) return k;
  return h;
}
// the static texts of index.html: data-i18n (text), data-i18n-placeholder, -title, -aria-label
function translatePage(root = document) {
  for (const el of root.querySelectorAll('[data-i18n]')) el.textContent = t(el.dataset.i18n);
  for (const attr of ['placeholder', 'title', 'aria-label']) {
    for (const el of root.querySelectorAll(`[data-i18n-${attr}]`)) el.setAttribute(attr, t(el.getAttribute(`data-i18n-${attr}`)));
  }
}
translatePage();

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
    const err = new Error((data && data.error && t(data.error)) || t('Error {status}', { status: res.status }));
    err.status = res.status;
    err.data = data;
    throw err;
  }
  return data;
}

let toastTimer;
function toast(msg) {
  const box = $('#toast');
  box.textContent = msg;
  box.hidden = false;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => { box.hidden = true; }, 6000);
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

const STATUS = { new: t('Por descubrir'), prepared: t('Guía lista'), tested: t('Comprobado'), explained: t('Explicado') };
const SECTION = { 'Guía': t('La guía'), 'Conceptos base': t('Conceptos base'), 'Dudas': t('Dudas'), 'Prueba': t('Preguntas'), 'Feynman': t('Explicación'), 'Conexiones': t('Conexiones') };
const TYPE = { video: t('Video'), web: t('Web'), book: t('Libro'), paper: t('Paper'), interactive: t('Interactivo') };
const DEPTH = [t('solo la guía'), t('1 nivel de conceptos'), t('{n} niveles', { n: 2 }), t('{n} niveles', { n: 3 }), t('{n} niveles', { n: 4 })];
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
  if (!key) return t('Sueltos');
  if (key.startsWith('g:')) return (nodeOf(key.slice(2)) || {}).label || key.slice(2);
  if (key.startsWith('c:')) return key.length > 2 ? t('Conceptos de {guide}', { guide: key.slice(2) }) : t('Conceptos sueltos');
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
    .replace(/\$\$([\s\S]+?)\$\$/g, (_, tex) => math(tex, true))
    .replace(/\\\[([\s\S]+?)\\\]/g, (_, tex) => math(tex, true))
    .replace(/\\\(([\s\S]+?)\\\)/g, (_, tex) => math(tex, false))
    .replace(/(?<![\\$\w])\$(?!\s)([^$\n]+?)(?<!\s)\$(?![\w$])/g, (_, tex) => math(tex, false));
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
    if (!writtenNode(slug) && !roadmapOf(slug)) a.classList.add('pending');
  }
}

/* ---------- rich blocks inside Markdown: callouts and diagrams ---------- */
// Obsidian callout syntax ("> [!tip] Title"), so the same notes look right in both apps.
const CALLOUT = {
  info: ['info', t('Nota')], note: ['info', t('Nota')], abstract: ['info', t('Resumen')], summary: ['info', t('Resumen')],
  tip: ['tip', t('Intuición')], hint: ['tip', t('Pista')], important: ['tip', t('Importante')], success: ['tip', t('Listo')],
  example: ['example', t('Ejemplo')],
  warning: ['warn', t('Ojo')], caution: ['warn', t('Ojo')], danger: ['warn', t('Ojo')], failure: ['warn', t('Error')], bug: ['warn', t('Error')],
  question: ['question', t('Piénsalo')], todo: ['question', t('Piénsalo')], help: ['question', t('Piénsalo')],
  quote: ['quote', t('Cita')], cite: ['quote', t('Cita')],
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
    const body = h('div', { class: 'diagram-body', 'aria-busy': 'true' }, h('span', { class: 'hint' }, t('Dibujando el diagrama…')));
    code.parentElement.replaceWith(h('figure', { class: 'diagram', role: 'img', 'aria-label': t('Diagrama') }, body));
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
      body.replaceChildren(h('p', { class: 'hint' }, t('No pude dibujar este diagrama. Este es su código:')), h('pre', {}, h('code', {}, src)));
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
// "## heading" in any language: the section's name (in Spanish) or how it reads in any other
const reEscape = (s) => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
const headingRe = (heading) => (HEADINGS[heading] || [heading]).map(reEscape).join('|');
function stripSection(body, heading) {
  const re = new RegExp(`(^|\\n)## (?:${headingRe(heading)})\\n[\\s\\S]*?(?=\\n## |$)`);
  return body.replace(re, '\n');
}
// the part of a note under "## heading", without the heading
function sectionOf(body, heading) {
  const m = new RegExp(`(?:^|\\n)## (?:${headingRe(heading)})\\s*\\n([\\s\\S]*?)(?=\\n## |$)`).exec(body || '');
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
  const th = effectiveTheme();
  $('#btn-theme use').setAttribute('href', `icons.svg#i-${th === 'dark' ? 'sun' : 'moon'}`);
  if (S.cy) S.cy.style(cyStyle());
  // widgets read their colours when drawn: draw what is open again and sync live iframes
  if (S.note) renderReader(S.note);
  renderConcepts();
  renderPractice();
  if (window.Viz && Viz.syncTheme) Viz.syncTheme(th);
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
const PANES = { chat: t('Chat'), chats: t('Conversaciones'), guide: t('Guía'), concepts: t('Conceptos'), map: t('Mapa'), practice: t('Practicar'), notes: t('Apuntes'), progress: t('Progreso'), vault: t('Vault'), settings: t('Ajustes') };
const STUDY = ['guide', 'concepts', 'map', 'practice', 'notes']; // the study panel: closed until there is something to show
const NEAR_STUDY = [...STUDY, 'progress', 'vault', 'settings']; // panels that open next to the study group

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
$('#edit-reset').addEventListener('click', () => { store.set('dock', null); defaultLayout(); toast(t('Diseño restablecido.')); });

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
  b.title = open ? t('Cerrar el panel de estudio') : t('Abrir el panel de estudio (guía, conceptos, mapa, apuntes)');
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
        full ? btn('minimize-2', t('Salir de pantalla completa'), () => document.exitFullscreen().catch(() => {}))
          : btn('fullscreen', t('Pantalla completa'), () => fullScreen(group)),
        full ? null : btn(max ? 'minimize-2' : 'maximize-2', max ? t('Volver al tamaño anterior') : t('Ampliar'), () => {
          if (group.api.isMaximized()) group.api.exitMaximized(); else group.api.maximize();
          drawGroupActions();
        }),
        holdsChat || full ? null : btn('x', t('Cerrar el panel'), () => closeGroup(group)),
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
  if (id === 'progress') renderProgress();
  if (id === 'vault') renderVault();
  if (id === 'settings') renderSettings();
}
$('#btn-settings').addEventListener('click', () => show('settings'));
$('#btn-progress').addEventListener('click', () => show('progress'));

/* ---------- Ajustes: which AI answers ---------- */
const capitalize = (s) => (s ? s[0].toUpperCase() + s.slice(1) : s);
// why a provider is not ready, as the server says it (in Spanish): "falta KEY" names a variable
function providerReason(reason) {
  const m = /^falta ([A-Z0-9_]+)$/.exec(reason || '');
  return m ? t('falta {key}', { key: m[1] }) : t(reason || '');
}
// A plain settings page: one dropdown for the AI, dropdowns for its models, two buttons.
const KIND = { cli: t('Con tu suscripción'), api: t('Por API (clave propia)'), local: t('En tu PC') };
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
  const custom = h('input', { type: 'text', value: value || '', placeholder: t('Nombre exacto del modelo'), 'aria-label': t('{label}: otro', { label }) });
  const known = !value || list.includes(value);
  const select = h('select', { 'aria-label': label, disabled: loading || null },
    loading ? h('option', {}, t('Cargando modelos…')) : [
      h('option', { value: '', selected: !value || null }, empty),
      list.map((m) => h('option', { value: m, selected: m === value || null }, m)),
      h('option', { value: OTHER, selected: !known || null }, t('Otro…')),
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

  const picker = h('select', { 'aria-label': t('Proveedor de IA'), onchange: (e) => { SET.pick = e.target.value; drawSettings(); } },
    Object.entries(KIND).map(([kind, title]) => h('optgroup', { label: title },
      list.filter((p) => p.kind === kind).map((p) => h('option', { value: p.id, selected: p.id === sel.id || null },
        p.name + (p.id === inUse ? ` (${t('en uso')})` : ''))))));
  const hasDefault = sel.kind === 'cli' || !!sel.defaultModel;
  const model = modelPicker({
    value: f.model, list: mods.list, loading: mods.loading, label: t('Modelo'),
    empty: hasDefault ? t('Predeterminado') : t('Elige un modelo'), onChange: (v) => { f.model = v; },
  });
  const quick = modelPicker({
    value: f.quick, list: mods.list, loading: mods.loading, label: t('Modelo rápido'),
    empty: t('Igual que el modelo'), onChange: (v) => { f.quick = v; },
  });
  const url = sel.editURL ? h('input', { type: 'url', value: f.url, placeholder: 'https://…/v1', 'aria-label': 'URL' }) : null;
  url?.addEventListener('change', () => { f.url = url.value.trim(); delete SET.models[sel.id]; drawSettings(); });
  const result = h('p', { class: 'set-result', role: 'status' });
  const body = () => ({ provider: sel.id, model: f.model, quickModel: f.quick, baseURL: url ? f.url : '' });
  const say = (cls, text) => { result.className = `set-result ${cls}`; result.textContent = text; };
  if (mods.error) say('bad', t('No pude cargar los modelos: {error}', { error: mods.error }));

  // one line of plain status: ready or what is missing, and whether it searches the web
  const facts = [
    sel.ready ? t('Disponible') : capitalize(providerReason(sel.reason)),
    sel.search ? t('busca en la web') : t('sin búsqueda web (las guías salen sin recursos externos)'),
    sel.experimental ? 'experimental' : '',
  ].filter(Boolean).join(' · ');

  const test = h('button', { class: 'btn small ghost', type: 'button', onclick: async (e) => {
    const b = e.currentTarget;
    b.disabled = true;
    say('', t('Probando…'));
    try {
      const r = await api('/api/settings/test', { method: 'POST', body: body() });
      say('ok', t('Funciona: respondió «{reply}» en {s} s.', { reply: r.reply.slice(0, 60), s: (r.ms / 1000).toFixed(1) }));
    } catch (err) { say('bad', err.message); }
    b.disabled = false;
  } }, t('Probar'));
  const save = h('button', { class: 'btn small primary', type: 'button', onclick: async (e) => {
    const b = e.currentTarget;
    b.disabled = true;
    try {
      const r = await api('/api/settings', { method: 'POST', body: body() });
      $('#provider-badge').textContent = r.provider;
      $('#banner').textContent = r.warning || '';
      $('#banner').hidden = !r.warning;
      toast(t('Ahora responde {name}.', { name: sel.name }));
      await renderSettings();
    } catch (err) { say('bad', err.message); b.disabled = false; }
  } }, t('Guardar'));

  const row = (label, control, extra) => h('div', { class: 'set-row' }, h('span', { class: 'set-label' }, label), h('div', { class: 'set-control' }, control, extra || null));
  const keyLine = sel.kind === 'api' && (sel.keyEnvs || []).length && !sel.ready
    ? h('p', { class: 'set-note' }, t('La clave va en una variable de entorno, no en la app: '), h('code', {}, `setx ${sel.keyEnvs[0]} "${t('tu-clave')}"`), t(' en PowerShell, y vuelve a abrir StudyHomie.'))
    : null;

  root.replaceChildren(h('div', { class: 'settings' },
    h('h2', {}, t('Ajustes')),
    warning ? h('p', { class: 'set-warning' }, warning) : null,
    h('section', { class: 'set-section' },
      h('h3', {}, t('Inteligencia artificial')),
      row(t('Proveedor'), picker, h('p', { class: 'set-facts' }, facts)),
      sel.note ? row('', h('p', { class: 'set-note' }, mdInline(t(sel.note)))) : null,
      keyLine ? row('', keyLine) : null,
      url ? row('URL', url) : null,
      row(t('Modelo'), model),
      row(t('Modelo rápido'), quick, h('p', { class: 'set-facts' }, t('Para el chat, las notas de concepto y el plan.'))),
      row('', h('div', { class: 'set-actions' }, save, test), result)),
    languageSection(row),
    studySection(row),
    updateSection(row),
    h('p', { class: 'set-foot' }, t('Se guarda en {file}. Lo que esté en curso termina con la IA anterior.', { file: SET.data.configPath || 'studyhomie.toml' }))));
}

// the "Idioma" block of the settings page: the interface and what the AI writes. Changing it
// reloads the page, so every text comes back in the new language.
function languageSection(row) {
  const langs = (window.I18N && window.I18N.langs) || [];
  const result = h('p', { class: 'set-result', role: 'status' });
  const pick = h('select', { 'aria-label': t('Idioma'), onchange: async (e) => {
    try {
      await api('/api/settings/language', { method: 'POST', body: { language: e.target.value } });
      location.reload();
    } catch (err) { result.className = 'set-result bad'; result.textContent = err.message; }
  } }, langs.map((l) => h('option', { value: l.code, selected: l.code === LANG || null }, l.name)));
  return h('section', { class: 'set-section' }, h('h3', {}, t('Idioma')),
    row(t('Idioma'), pick, h('p', { class: 'set-facts' }, t('De la interfaz y de lo que escribe la IA: guías, notas, preguntas y el chat. Las notas que ya tienes quedan como están.'))),
    row('', result));
}

// the "Estudio" block of the settings page: the score a roadmap topic needs to unlock the next one,
// and how long one AI answer may take before it is cut
function studySection(row) {
  const study = SET.data.study || {};
  const result = h('p', { class: 'set-result', role: 'status' });
  const say = (cls, text) => { result.className = `set-result ${cls}`; result.textContent = text; };
  const input = h('input', { type: 'number', min: 0, max: 100, step: 5, value: study.passScore ?? 70, class: 'set-num', 'aria-label': t('Nota para avanzar en un roadmap') });
  input.addEventListener('change', async () => {
    const v = Math.round(Number(input.value));
    if (!Number.isFinite(v) || v < 0 || v > 100) { say('bad', t('Pon un número de 0 a 100.')); return; }
    try {
      const r = await api('/api/settings/study', { method: 'POST', body: { passScore: v } });
      SET.data.study = r;
      say('ok', t('Guardado.'));
      loadGraph().catch(() => {}).then(() => reloadGuide());
    } catch (err) { say('bad', err.message); }
  });
  const limit = h('input', { type: 'number', min: 0, max: 60, step: 1, value: study.timeout ?? 5, class: 'set-num', 'aria-label': t('Límite de tiempo por respuesta de la IA') });
  limit.addEventListener('change', async () => {
    const v = Math.round(Number(limit.value));
    if (!Number.isFinite(v) || v < 0 || v > 60) { say('bad', t('Pon un número de 0 a 60.')); return; }
    try {
      SET.data.study = await api('/api/settings/study', { method: 'POST', body: { timeout: v } });
      say('ok', t('Guardado.'));
    } catch (err) { say('bad', err.message); }
  });
  const goalIn = h('input', { type: 'number', min: 10, max: 500, step: 10, value: study.dailyGoal ?? 50, class: 'set-num', 'aria-label': t('Meta diaria de XP') });
  const presets = [30, 50, 100];
  let presetBtns = [];
  const saveGoal = async (v) => {
    if (!Number.isFinite(v) || v < 10 || v > 500) { say('bad', t('Pon un número de 10 a 500.')); return; }
    try {
      SET.data.study = await api('/api/settings/study', { method: 'POST', body: { dailyGoal: v } });
      goalIn.value = v;
      presetBtns.forEach((b, i) => b.setAttribute('aria-pressed', String(presets[i] === v)));
      say('ok', t('Guardado.'));
      loadStats();
    } catch (err) { say('bad', err.message); }
  };
  goalIn.addEventListener('change', () => saveGoal(Math.round(Number(goalIn.value))));
  presetBtns = presets.map((v) => h('button', {
    type: 'button', class: 'btn small', 'aria-pressed': String((study.dailyGoal ?? 50) === v),
    onclick: () => saveGoal(v),
  }, `${v} XP`));
  return h('section', { class: 'set-section' }, h('h3', {}, t('Estudio')),
    row(t('Meta diaria de XP'), h('div', { class: 'set-inline goal-presets' }, ...presetBtns, goalIn, h('span', {}, 'XP')),
      h('p', { class: 'set-facts' }, t('XP que necesitas cada día para mantener tu racha. Ganas 1 congelador cada 7 días seguidos (máx. 2).'))),
    row(t('Nota para avanzar'), h('div', { class: 'set-inline' }, input, h('span', {}, '/ 100')),
      h('p', { class: 'set-facts' }, t('En un roadmap, el tema siguiente se desbloquea (y se escribe su guía) cuando sacas esta nota o más en la prueba o explicándolo.'))),
    row(t('Límite por respuesta'), h('div', { class: 'set-inline' }, limit, h('span', {}, t('minutos'))),
      h('p', { class: 'set-facts' }, t('Si la IA tarda más que esto en una respuesta, la corto y te aviso, en vez de esperar para siempre. Las guías que buscan en la web tienen el triple. 0 es sin límite.'))),
    row(t('Tutorial'), h('button', { type: 'button', class: 'btn small', onclick: () => openTutorial(true) }, t('Ver tutorial')),
      h('p', { class: 'set-facts' }, t('Repasa en 6 pasos cómo funcionan las guías, el iceberg, el mapa, la práctica y tu racha.'))),
    row('', result));
}

/* ---------- Actualizaciones ---------- */
// The server looks for new versions on GitHub (at most every few hours). In "ask" mode a bar under
// the header offers it; installing downloads, verifies, swaps the binary and restarts the app, and
// this tab waits for the new version to answer and reloads.
const UPD = { st: null, busy: false };
const UPDATE_MODES = { ask: t('Preguntarme antes de actualizar'), auto: t('Instalar sola (se usa al reiniciar)'), never: t('No buscar versiones nuevas') };
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
    ? h('span', {}, h('strong', {}, t('StudyHomie {version} ya está instalado.', { version: st.latest })), ' ', t('Se usa al reiniciar.'))
    : h('span', {}, h('strong', {}, t('Hay una versión nueva')), ' ' + t('({latest}). Tienes la {current}.', { latest: st.latest, current: st.current })));
  $('#update-go span').textContent = st.staged ? t('Reiniciar ahora') : t('Actualizar');
  $('#update-go use').setAttribute('href', `icons.svg#i-${st.staged ? 'refresh-cw' : 'download'}`);
}

function fmtDay(iso) {
  const d = new Date(iso);
  return isNaN(d) || d.getFullYear() < 2000 ? '' : d.toLocaleDateString(LOCALE, { day: 'numeric', month: 'long', year: 'numeric' });
}

function openUpdateDialog() {
  const st = UPD.st;
  if (!st || !st.latest) return;
  $('#update-title').textContent = `StudyHomie ${st.latest}`;
  const day = st.published && fmtDay(st.published);
  $('#update-body').replaceChildren(...[
    h('p', { class: 'update-meta' }, [day ? t('Publicada el {day}', { day }) : '', t('tienes la {current}', { current: st.current })].filter(Boolean).join(' · ')),
    mdBlock(st.notes || t('Esta versión no trae notas.')),
    st.page ? h('p', { class: 'update-meta' }, h('a', { href: st.page, target: '_blank', rel: 'noopener noreferrer' }, t('Ver la versión en GitHub'))) : null,
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
      btn('', t('Esperar a que termine'), () => $('#update-dialog').close()),
      btn('primary', t('Actualizar igual'), () => installUpdate(true), 'download')));
    return;
  }
  if (state.done) { foot.replaceChildren(...[msg].filter(Boolean)); return; }
  foot.replaceChildren(...[msg].filter(Boolean), h('div', { class: 'update-actions' },
    st.staged ? null : btn('ghost', t('Omitir esta versión'), skipUpdate),
    h('span', { class: 'spacer' }),
    btn('', t('Más tarde'), () => { setLater(st.latest); drawUpdateBar(); $('#update-dialog').close(); }),
    btn('primary', st.staged ? t('Reiniciar ahora') : t('Actualizar'), () => installUpdate(), st.staged ? 'refresh-cw' : 'download')));
}

async function skipUpdate() {
  try {
    UPD.st = await api('/api/update/skip', { method: 'POST', body: { version: UPD.st.latest } });
    $('#update-dialog').close();
    drawUpdateBar();
    toast(t('No te avisaré de la {version}; sí de la siguiente.', { version: UPD.st.latest }));
  } catch (err) { updateFoot({ error: err.message }); }
}

async function installUpdate(force = false) {
  const st = UPD.st;
  if (!st || UPD.busy) return;
  openUpdateDialog();
  UPD.busy = true;
  drawUpdateBar();
  updateFoot({ progress: st.staged ? t('Reiniciando…') : t('Descargando y verificando {version}…', { version: st.latest }) });
  try {
    const r = st.staged
      ? await api('/api/update/restart', { method: 'POST', body: {} })
      : await api('/api/update', { method: 'POST', body: { force } });
    if (!r.restarting) {
      UPD.busy = false;
      await checkUpdate();
      updateFoot({ done: true, note: t('Listo: la {version} quedó instalada. Cierra StudyHomie y vuelve a abrirlo para usarla.', { version: r.version || st.latest }) });
      return;
    }
    updateFoot({ progress: t('Reiniciando StudyHomie…') });
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
  throw new Error(t('StudyHomie no volvió a arrancar solo. Ábrelo de nuevo: la versión nueva ya está instalada.'));
}

$('#update-notes').addEventListener('click', openUpdateDialog);
$('#update-go').addEventListener('click', () => installUpdate());
$('#update-later').addEventListener('click', () => { setLater(UPD.st.latest); drawUpdateBar(); });
$('#update-close').addEventListener('click', () => $('#update-dialog').close());

// the "Actualizaciones" block of the settings page
function updateSection(row) {
  const st = UPD.st;
  if (!st) return null;
  const head = h('h3', {}, t('Actualizaciones'));
  if (!st.enabled) {
    return h('section', { class: 'set-section' }, head,
      row(t('Versión'), h('p', { class: 'set-facts' }, t('{version} · compilación de desarrollo: no se actualiza sola.', { version: st.current }))));
  }
  const result = h('p', { class: 'set-result', role: 'status' });
  const say = (cls, text) => { result.className = `set-result ${cls}`; result.textContent = text; };
  const mode = h('select', { 'aria-label': t('Al salir una versión nueva'), onchange: async (e) => {
    try {
      UPD.st = await api('/api/update/mode', { method: 'POST', body: { mode: e.target.value } });
      drawUpdateBar();
      say('ok', t('Guardado.'));
    } catch (err) { say('bad', err.message); }
  } }, Object.entries(UPDATE_MODES).map(([v, label]) => h('option', { value: v, selected: v === st.mode || null }, label)));

  let line;
  if (st.staged) line = t('La {version} ya está instalada: se usa al reiniciar.', { version: st.latest });
  else if (st.available) line = t('Hay una versión nueva: {version}.', { version: st.latest });
  else if (st.latest) line = t('Tienes la última versión.');
  else line = fmtDay(st.checked) ? t('Todavía no hay versiones publicadas.') : t('Todavía no se buscó.');
  const checked = fmtDay(st.checked) ? ' ' + t('Última búsqueda: {when}.', { when: new Date(st.checked).toLocaleString(LOCALE, { dateStyle: 'medium', timeStyle: 'short' }) }) : '';
  if (st.error) say('bad', st.error);

  const find = h('button', { class: 'btn small ghost', type: 'button', onclick: async (e) => {
    e.currentTarget.disabled = true;
    say('', t('Buscando…'));
    await checkUpdate(true);
    drawSettings();
  } }, icon('refresh-cw'), t('Buscar ahora'));
  const see = st.available ? h('button', { class: 'btn small primary', type: 'button', onclick: openUpdateDialog }, st.staged ? t('Reiniciar') : t('Ver y actualizar')) : null;

  return h('section', { class: 'set-section' }, head,
    row(t('Versión'), h('p', { class: 'set-facts' }, h('strong', {}, st.current), ` · ${line}${checked}`)),
    row(t('Al salir una nueva'), mode),
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
      icon(S.editingLayout ? 'lock' : 'lock-open'), S.editingLayout ? t('Terminar de editar') : t('Editar diseño')),
    h('button', { type: 'button', onclick: () => { menu.hidden = true; store.set('dock', null); defaultLayout(); toast(t('Diseño restablecido.')); } },
      icon('rotate-ccw'), t('Restablecer diseño')));
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
    loadStats();
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
  if (roadmapOf(slug)) { closeGuide(); show('guide'); return; } // a roadmap lives in the list of guides
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
    h('p', { class: 'eyebrow' }, t('Aún sin nota')),
    h('h4', {}, pend.title || pend.label || title),
    h('p', {}, pend.summary || t('Escribo una nota corta, con lo justo para seguir la guía. Queda en tu vault.')),
    h('div', { class: 'actions' },
      h('button', { class: 'btn small primary', type: 'button', onclick: () => {
        pop.hidden = true;
        explainConcept(slug, { guide, title: pend.title || title, el: a });
      } }, icon('sparkles'), t('Explicar')),
      h('button', { class: 'btn small ghost', type: 'button', onclick: () => {
        pop.hidden = true;
        chatAction({ action: 'guide', topic: pend.title || title, depth: S.depth }, t('Guía: {topic}', { topic: pend.title || title }));
      } }, t('Guía completa'))));
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
  if (!guide) { chatAction({ action: 'concept', title }, t('Explícame «{title}»', { title })); return; }
  el?.classList.add('writing');
  try {
    const r = await api(`/api/notes/${encodeURIComponent(guide)}/explain`, { method: 'POST', body: { ref } });
    S.lit = r.slug;
    await loadGraph();
    if (S.guide === guide) await reloadGuide(); else renderConcepts();
    if (S.concept) { try { S.concept = await api(`/api/notes/${encodeURIComponent(S.concept.slug)}`); renderConcepts(); } catch { /* it stays as it was */ } }
    toast(t('«{title}» ya tiene su nota.', { title }));
  } catch (err) {
    el?.classList.remove('writing');
    toast(err.message);
  }
}

function openSim(slugOrSrc, title) {
  $('#sim-title').textContent = title || t('Simulación');
  const raw = String(slugOrSrc || '');
  const path = raw.startsWith('/interactive/') ? raw : raw.startsWith('interactive/') ? `/${raw}` : `/interactive/${encodeURIComponent(raw)}.html`;
  const sep = path.includes('?') ? '&' : '?';
  $('#sim-frame').src = `${path}${sep}theme=${encodeURIComponent(effectiveTheme())}`;
  $('#sim-dialog').showModal();
}
$('#sim-close').addEventListener('click', () => $('#sim-dialog').close());
$('#sim-dialog').addEventListener('close', () => { $('#sim-frame').src = 'about:blank'; });

/* ---------- Guía: the guide as an article ---------- */
const statusChip = (status) => h('span', { class: 'chip status', style: `--c: var(--s-${status})` }, STATUS[status] || status);

// widgets in a note can build, regenerate, repair and expand their own page on demand
const vizCtx = (slug) => {
  const url = `/api/notes/${encodeURIComponent(slug)}/viz`;
  return {
    theme: effectiveTheme,
    generate: async (spec) => (await api(url, { method: 'POST', body: { brief: spec.brief } })).src,
    regenerate: async (spec) => (await api(url, { method: 'POST', body: { brief: spec.brief, force: true } })).src,
    fix: async (spec, fixError) => (await api(url, { method: 'POST', body: { brief: spec.brief, fixError } })).src,
    expand: (src, title) => openSim(src, title),
  };
};

/* ---------- Roadmaps: a numbered path, each topic unlocked by passing the one before ---------- */
const roadmapOf = (slug) => S.roadmaps.find((r) => r.slug === slug);

// asks the chat to write (or open) the guide of the topic the learner is on
function nextTopic(rm, step) {
  chatAction({ action: 'next', roadmap: rm.slug }, t('Siguiente tema: {title}', { title: step.title }));
}

// One roadmap as a numbered path: passed topics show their score, the current one opens (or gets
// its guide written), the rest wait behind a lock until the topic before them is passed.
function roadmapPath(rm, { open = true } = {}) {
  const steps = rm.steps || [];
  const list = h('ol', { class: 'rm-path' });
  let block = '';
  for (const st of steps) {
    if (st.block && st.block !== block) {
      block = st.block;
      list.append(h('li', { class: 'rm-block' }, st.block));
    }
    const mark = st.state === 'passed' ? icon('check') : st.state === 'locked' ? icon('lock') : h('span', {}, String(st.n));
    let side = null;
    if (st.state === 'passed') side = h('small', { class: 'rm-score' }, `${st.mastery}/100`);
    else if (st.state === 'locked') side = h('small', { class: 'rm-wait' }, t('Bloqueado'));
    else if (!st.ready && open) {
      side = h('button', { class: 'btn small primary', type: 'button', onclick: (e) => { e.stopPropagation(); nextTopic(rm, st); } },
        icon('sparkles'), t('Escribir la guía'));
    } else if (st.ready) side = h('small', { class: 'rm-now' }, st.mastery ? t('{mastery}/100 · necesitas {pass}', { mastery: st.mastery, pass: rm.pass }) : t('Ahora'));
    const canOpen = open && st.ready && st.state !== 'locked'; // a locked topic stays closed, even with a guide from before
    const row = h(canOpen ? 'button' : 'div', {
      class: 'rm-step', type: canOpen ? 'button' : null, title: st.summary || null,
      onclick: canOpen ? () => openNote(st.slug) : null,
    }, h('span', { class: 'rm-mark', 'aria-hidden': 'true' }, mark),
    h('span', { class: 'rm-title' }, h('span', { class: 'sr-only' }, `${st.n}. `), st.title, st.summary ? h('small', {}, st.summary) : null),
    side);
    list.append(h('li', { 'data-state': st.state }, row));
  }
  return list;
}

function roadmapHint(rm) {
  const done = (rm.steps || []).filter((s) => s.state === 'passed').length;
  return t('{done} de {total} · cada tema se desbloquea con {pass}/100 en el anterior', { done, total: (rm.steps || []).length, pass: rm.pass });
}

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
  return h('div', { class: 'guide-list' }, keys.map((k) => roadmapOf(k) ? h('section', { class: 'dg dg-roadmap' },
    h('h3', {}, icon('map'), groupTitle(k)),
    h('p', { class: 'rm-hint' }, roadmapHint(roadmapOf(k))),
    roadmapPath(roadmapOf(k))) : h('section', { class: 'dg' },
    h('h3', {}, k ? groupTitle(k) : t('Tus guías')),
    h('ul', {}, groups.get(k).sort((a, b) => (a.order || 0) - (b.order || 0) || a.label.localeCompare(b.label)).map((n) => h('li', {},
      h('button', { class: 'dn', type: 'button', style: `--lc: ${lineColor(n.group)}`, onclick: () => openNote(n.id) },
        h('i', { class: `st st-${n.status}`, title: STATUS[n.status] || n.status }), n.label)))))));
}

// the end of a roadmap guide: what unlocks the next topic, or the way to it
function pathSection(rm, st, title) {
  const next = rm.steps[st.n]; // steps are numbered from 1
  let line;
  if (!next) line = h('p', {}, st.state === 'passed' ? t('Completaste «{title}».', { title: rm.title }) : t('Es el último tema de «{title}».', { title: rm.title }));
  else if (st.state !== 'passed') {
    line = h('p', {}, t('El tema {n}, «{next}», se desbloquea cuando saques {pass}/100 o más en «{title}»', { n: next.n, next: next.title, pass: rm.pass, title }),
      st.mastery ? ' ' + t('(llevas {mastery}).', { mastery: st.mastery }) : t(', con la prueba o explicándolo.'));
  } else if (next.ready) {
    line = h('button', { class: 'neighbor', type: 'button', onclick: () => openNote(next.slug) }, h('small', {}, t('Tema {n}', { n: next.n })), h('strong', {}, next.title));
  } else {
    line = h('div', { class: 'actions' }, h('button', { class: 'btn small primary', type: 'button', onclick: () => nextTopic(rm, next) },
      icon('sparkles'), t('Escribir el tema {n}: {title}', { n: next.n, title: next.title })));
  }
  return h('section', { class: 'r-path' }, h('h2', {}, t('En la ruta')),
    h('p', { class: 'r-path-sub' }, `«${rm.title}» · ${roadmapHint(rm)}`), line,
    st.state !== 'passed' && next ? h('div', { class: 'actions' },
      h('button', { class: 'btn small', type: 'button', onclick: () => show('practice') }, icon('target'), t('Practicar'))) : null);
}

function renderReader(n) {
  const root = $('#reader');
  root.replaceChildren();
  if (!n) {
    root.append(h('div', { class: 'panel-empty' },
      h('h3', {}, t('Aquí aparecerá tu guía')),
      h('p', {}, t('Pide en el chat lo que quieras entender. «Escribir ahora» decide cuántos niveles de conceptos base se escriben de entrada; los demás quedan mapeados, en gris, para explicarlos cuando los necesites.')),
      guideList() || ''));
    return;
  }
  const m = n.meta;
  const status = m.status || 'new';
  const title = m.title || n.slug;
  const node = nodeOf(n.slug);
  const concepts = n.concepts || [];
  const under = (m.pending || []).length;
  const rm = m.roadmap ? roadmapOf(m.roadmap) : null;
  const rmStep = rm ? (rm.steps || []).find((s) => s.slug === n.slug) : null;
  const wrap = h('div', { class: 'reader-wrap', style: `--line-c: ${lineColor(node ? node.group : '')}` });
  wrap.append(h('header', { class: 'r-head' },
    h('button', { class: 'crumb', type: 'button', onclick: closeGuide }, icon('arrow-left'), t('Todas las guías')),
    h('p', { class: 'r-line' }, m.roadmap ? groupTitle(m.roadmap) : t('Guía')),
    h('h1', {}, title),
    h('div', { class: 'chips' }, statusChip(status),
      rmStep ? h('span', { class: 'chip' }, t('Tema {n} de {total}', { n: rmStep.n, total: rm.steps.length })) : null,
      m.focus ? h('span', { class: 'chip' }, `Foco: ${m.focus}`) : null,
      m.field ? h('span', { class: 'chip' }, m.field) : null),
    m.mastery ? h('div', { class: 'mastery' },
      h('div', { class: 'row' }, h('span', {}, t('Lo que llevas')), h('span', {}, `${m.mastery}/100`)),
      h('div', { class: 'bar' }, h('i', { style: `width:${m.mastery}%; background: var(--line-c)` }))) : null,
    h('div', { class: 'actions' },
      concepts.length || under ? h('button', { class: 'btn small yellow', type: 'button', onclick: () => show('concepts') },
        icon('mountain-snow'), tn(concepts.length, '{n} concepto', '{n} conceptos') + (under ? ' · ' + t('{n} bajo el agua', { n: under }) : '')) : null,
      status !== 'new' ? h('button', { class: 'btn small', type: 'button', onclick: () => show('practice') }, icon('target'), t('Practicar')) : null,
      notesButton(n.slug, title, n.apunte),
      h('button', { class: 'btn small ghost', type: 'button', onclick: () => askAbout(t('Sobre «{title}»:', { title }) + ' ') }, icon('message-circle'), t('Preguntar')))));

  const article = h('article', { class: 'r-article' });
  const bodySrc = stripEmoji(stripSection(stripSection(n.body, 'Recursos'), 'Material')).trim();
  if (bodySrc) {
    const md = mdBlock(bodySrc, { viz: vizCtx(n.slug) });
    for (const hd of $$('h2', md)) hd.textContent = SECTION[sectionKey(hd.textContent)] || hd.textContent;
    article.append(md);
  } else {
    article.append(h('p', { class: 'r-empty' }, t('Esta nota aún no tiene guía. Pídela en el chat.')));
  }
  const mats = m.materials || [];
  if (mats.length) {
    article.append(h('section', { class: 'r-materials' }, h('h2', {}, t('Material fuente')),
      h('ul', { class: 'mat-list' }, mats.map((rel) => {
        const parts = String(rel).replace(/^material\//, '').split('/');
        const topic = parts[0] || n.slug;
        const name = parts.slice(1).join('/') || rel;
        return h('li', {}, h('a', {
          class: 'mat-chip', href: `/api/material/${encodeURIComponent(topic)}/${encodeURIComponent(name)}`,
          target: '_blank', rel: 'noopener noreferrer',
        }, icon('file'), h('span', {}, name)));
      }))));
  }
  const res = m.resources || [];
  if (res.length) {
    article.append(h('section', { class: 'r-resources' }, h('h2', {}, t('Para seguir explorando')),
      h('ul', { class: 'res-list' }, res.map((r) => resourceCard(r, { slug: n.slug, title })))));
  }
  const back = (n.backlinks || []).filter((b) => !concepts.some((c) => c.slug === b.slug));
  const before = n.requires || [];
  if (before.length || back.length) {
    const nb = (label, l) => h('button', { class: 'neighbor', type: 'button', onclick: () => openNote(l.slug) }, h('small', {}, label), h('strong', {}, l.title));
    article.append(h('section', { class: 'r-next' }, h('h2', {}, t('Relacionado')),
      before.map((l) => nb(t('Antes'), l)), back.map((l) => nb(t('Enlaza aquí'), l))));
  }
  if (rmStep) article.append(pathSection(rm, rmStep, title));
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
  }, h('span', { class: 'play-icon', 'aria-hidden': 'true' }, '▶'), h('span', {}, t('Reproducir aquí')), h('small', {}, t('Carga YouTube al hacer clic'))));
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
    return h('li', { class: 'res' }, head(h('strong', {}, t('{title} (propuesta)', { title: r.title }))), why,
      ctx.slug && h('div', {}, h('button', { class: 'btn small', type: 'button', onclick: () => chatAction({ action: 'interactive', brief: r.why || '' }, t('Simulación: {title}', { title: ctx.title })) }, t('Generar'))));
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
const levelName = (l) => (l === 1 ? t('Lo que el tema usa directamente') : l === 2 ? t('Lo que eso necesita') : t('Más hondo (nivel {n})', { n: l }));

function renderConcepts() {
  const root = PANE_EL.concepts;
  if (!root) return;
  if (S.bergObs) { S.bergObs.disconnect(); S.bergObs = null; }
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
      const id = c.slug || c.id;
      const title = c.title || id || '';
      const sharedText = (c.guides || 0) > 1 ? tn(c.guides - 1, 'también en otra guía', 'también en {n} guías más') : '';
      const actionHint = isPending ? t('Aún sin nota: pulsa para explicarlo') : t('Abrir la nota');
      const el = h('button', {
        class: `ice-pill${isPending ? ' pending' : ''}${S.lit === id ? ' lit' : ''}`, type: 'button', style: `--i:${i++}`,
        'data-id': id, 'data-requires': (c.requires || []).join(' '),
        'data-title': title,
        'data-summary': c.summary || '',
        'data-shared': sharedText,
        'data-hint': actionHint,
        title: [title, c.summary, sharedText, actionHint].filter(Boolean).join(' · '),
        onclick: () => (isPending ? explainConcept(c.id, { guide: n.slug, title, el }) : openNote(c.slug)),
      },
      h('span', { class: 'ice-dot', 'aria-hidden': 'true' }),
      h('strong', {}, title),
      (c.guides || 0) > 1 ? h('span', { class: 'shared', title: sharedText }, `×${c.guides}`) : null,
      isPending ? h('span', { class: 'go', 'aria-label': t('Explicar') }, '+') : null);
      return el;
    };
    const deepest = keys[keys.length - 1];
    const bergTipCard = h('div', { class: 'berg-tooltip', hidden: true, 'aria-hidden': 'true' });
    const berg = h('div', { class: 'berg', style: `--levels:${keys.length}` },
      h('div', { class: 'berg-tip' }, h('span', { class: 'tip', 'data-id': n.slug, 'data-requires': (n.meta.base || []).join(' ') }, n.meta.title)),
      h('ol', { class: 'berg-levels' }, keys.map((l) => {
        const g = levels.get(l);
        return h('li', { class: `berg-level${l > 1 ? ' deep' : ''}`, 'data-level': l, style: `--l:${l}; --water: var(--water-${Math.min(l, 5)})` },
          h('h3', {},
            h('span', { class: 'lvl-tag' }, `N${l}`),
            h('span', {}, levelName(l)),
            g.pending.length ? h('small', {}, t('{n} por explicar', { n: g.pending.length })) : null),
          h('div', { class: 'ice-pills' }, g.written.map((c) => pill(c, !c.exists)), g.pending.map((p) => pill(p, true))));
      })),
      h('div', { class: 'berg-floor', style: `--l:${deepest + 1}` }, digButton(n, deepest)),
      bergTipCard);
    box.append(
      h('header', { class: 'c-head' },
        h('p', { class: 'eyebrow' }, t('Lo que hay debajo')),
        h('h2', {}, n.meta.title),
        h('p', { class: 'hint' }, pending.length
          ? t('Lo blanco ya tiene nota. Lo punteado está mapeado pero sin escribir: explícalo solo si lo necesitas.')
          : t('Cada nivel sostiene al de arriba. Si algo no se entiende, baja un nivel.'))),
      berg,
      h('div', { class: 'ice-legend' },
        h('span', {}, h('i', { class: 'st st-concept' }), t('Con nota')),
        h('span', {}, h('i', { class: 'st st-pending' }), t('Por explicar (se escribe al pulsarlo)'))));
    S.lit = '';
    S.bergObs = new ResizeObserver(() => drawBerg(berg));
    S.bergObs.observe(berg);
  } else if (n) {
    box.append(h('div', { class: 'panel-empty' }, h('h3', {}, t('Esta guía no tiene conceptos mapeados')),
      h('p', {}, t('Las guías nuevas mapean lo que dan por sabido, hasta la profundidad que elijas. Puedes mapearlo ahora, nivel por nivel.')),
      digButton(n, 0)));
  }
  const all = ((S.graph && S.graph.nodes) || []).filter((x) => x.kind === 'concept');
  if (all.length) {
    const byCat = new Map();
    for (const c of all) { const k = c.category || t('sin categoría'); if (!byCat.has(k)) byCat.set(k, []); byCat.get(k).push(c); }
    box.append(h('details', { class: 'all-concepts', open: !n || null },
      h('summary', {}, t('Todos tus conceptos ({n})', { n: all.length })),
      [...byCat].sort((a, b) => a[0].localeCompare(b[0])).map(([cat, list]) => h('section', { class: 'dg' }, h('h3', {}, cat),
        h('div', { class: 'links' }, list.sort((a, b) => a.label.localeCompare(b.label)).map((c) => h('button', { type: 'button', onclick: () => openNote(c.id) }, c.label)))))));
  } else if (!n) {
    box.append(h('div', { class: 'panel-empty' }, h('h3', {}, t('Aún no hay conceptos')), h('p', {}, t('Cuando pidas una guía, aquí verás su iceberg: lo que tiene nota y lo que queda por explicar.'))));
  }
  root.append(box);
}

// The button at the bottom of the iceberg: maps one more level under the deepest concepts.
function digButton(n, deepest) {
  const label = deepest ? t('Bajar un nivel más') : t('Mapear lo que da por sabido');
  const btn = h('button', { class: 'btn small dig', type: 'button', onclick: async () => {
    btn.disabled = true;
    btn.replaceChildren(icon('loader'), t('Mapeando…'));
    btn.classList.add('working');
    try {
      const r = await api(`/api/notes/${encodeURIComponent(n.slug)}/dig`, { method: 'POST', body: {} });
      const added = r.added || [];
      if (added.length) S.lit = added[0].id;
      await loadGraph().catch(() => {});
      if (S.guide === n.slug) await reloadGuide(); else renderConcepts();
      toast(added.length ? tn(added.length, 'Mapeé un concepto más abajo.', 'Mapeé {n} conceptos más abajo.')
        : t('Llegaste al fondo: lo que hay debajo ya es lo básico.'));
    } catch (err) {
      toast(err.message);
      btn.disabled = false;
      btn.classList.remove('working');
      btn.replaceChildren(icon('chevrons-down'), label);
    }
  } }, icon('chevrons-down'), label);
  return h('div', { class: 'dig-box' }, btn,
    deepest ? h('small', {}, t('Mapea lo que necesita el nivel {n}. Se queda por explicar hasta que lo pulses.', { n: deepest })) : null);
}

// position of an element inside the iceberg box, ignoring the pills' entrance transforms
function offsetIn(el, root) {
  let x = 0, y = 0;
  for (let e = el; e && e !== root; e = e.offsetParent) { x += e.offsetLeft; y += e.offsetTop; }
  return { x, y, w: el.offsetWidth, h: el.offsetHeight };
}

// Draws the iceberg behind the pills: smooth sky-to-abyss gradient, faceted crystalline ice peak
// and submerged keel, shimmering waterline, subtle depth hairlines, and interactive dependency edges.
let bergSvgSeq = 0;
function drawBerg(berg) {
  if (!berg.isConnected || !berg.offsetWidth) return;
  const NS = 'http://www.w3.org/2000/svg';
  const el = (tag, attrs) => { const e = document.createElementNS(NS, tag); for (const [k, v] of Object.entries(attrs)) e.setAttribute(k, v); return e; };
  const W = berg.offsetWidth, H = berg.offsetHeight;
  const tip = offsetIn(berg.querySelector('.tip'), berg);
  const bands = [...berg.querySelectorAll('.berg-level')];
  const floor = offsetIn(berg.querySelector('.berg-floor'), berg);
  const waterY = tip.y + tip.h + 2;
  const cx = W / 2;
  const gid = `bg${++bergSvgSeq}`;
  const svg = el('svg', { class: 'berg-art', width: W, height: H, viewBox: `0 0 ${W} ${H}`, 'aria-hidden': 'true' });

  // Gradients for sky, continuous ocean depth, and faceted crystal ice
  const defs = el('defs', {});
  const mkGrad = (id, x1, y1, x2, y2, stops) => {
    const g = el('linearGradient', { id, x1, y1, x2, y2 });
    for (const [offset, color, opacity] of stops) {
      const s = el('stop', { offset, 'stop-color': color });
      if (opacity != null) s.setAttribute('stop-opacity', String(opacity));
      g.append(s);
    }
    return g;
  };
  defs.append(
    mkGrad(`${gid}-sky`, '0%', '0%', '0%', '100%', [
      ['0%', 'var(--sky)', 1],
      ['100%', 'var(--water-1)', 0.45],
    ]),
    mkGrad(`${gid}-sea`, '0%', '0%', '0%', '100%', [
      ['0%', 'var(--water-1)', 1],
      ['28%', 'var(--water-2)', 1],
      ['58%', 'var(--water-3)', 1],
      ['84%', 'var(--water-4)', 1],
      ['100%', 'var(--water-5)', 1],
    ]),
    mkGrad(`${gid}-ice`, '0%', '0%', '0%', '100%', [
      ['0%', 'var(--ice)', 0.28],
      ['48%', 'var(--ice)', 0.14],
      ['100%', 'var(--ice)', 0.05],
    ]),
    mkGrad(`${gid}-shade`, '0%', '0%', '100%', '100%', [
      ['0%', 'var(--water-4)', 0.08],
      ['100%', 'var(--water-5)', 0.28],
    ]),
    mkGrad(`${gid}-foam`, '0%', '0%', '0%', '100%', [
      ['0%', '#ffffff', 0.34],
      ['100%', '#ffffff', 0],
    ]),
  );
  svg.append(defs);

  // Sky + continuous ocean depth gradient
  svg.append(el('rect', { x: 0, y: 0, width: W, height: waterY, fill: `url(#${gid}-sky)` }));
  svg.append(el('rect', { x: 0, y: waterY, width: W, height: Math.max(0, H - waterY), fill: `url(#${gid}-sea)` }));
  svg.append(el('rect', { x: 0, y: waterY, width: W, height: 18, fill: `url(#${gid}-foam)` }));

  // Subtle horizontal depth dividers between levels
  const tops = bands.map((b, i) => (i ? (offsetIn(b, berg).y + offsetIn(bands[i - 1], berg).y + bands[i - 1].offsetHeight) / 2 : waterY));
  for (let i = 1; i < tops.length; i++) {
    svg.append(el('line', { class: 'berg-divider', x1: 18, y1: tops[i], x2: W - 18, y2: tops[i] }));
  }

  // Sculpted underwater iceberg silhouette: smoothly widens below the waterline without awkward pinches,
  // hugs each level's pills, and tapers naturally to a crystalline keel
  const jag = (k) => ((k * 37) % 7) - 3;
  const left = [], right = [];
  const tipL = Math.max(18, tip.x - 14), tipR = Math.min(W - 18, tip.x + tip.w + 14);
  let hullSpan = Math.max((tipR - tipL) / 2 + 24, W * 0.34);
  bands.forEach((b, i) => {
    const pills = [...b.querySelectorAll('.ice-pill')].map((p) => offsetIn(p, berg));
    const pillSpan = pills.length ? Math.max(...pills.map((p) => Math.max(cx - p.x, p.x + p.w - cx))) + 18 : hullSpan;
    const targetSpan = Math.min(W * 0.48, Math.max(hullSpan, pillSpan, W * (0.34 + i * 0.055)));
    hullSpan = targetSpan;
    const lo = Math.max(10, cx - targetSpan), hi = Math.min(W - 10, cx + targetSpan);
    const y = i ? tops[i] : waterY, y2 = i + 1 < bands.length ? tops[i + 1] : floor.y;
    left.push([lo + jag(i), (y + y2) / 2]);
    right.push([hi + jag(i + 1), (y + y2) / 2]);
  });
  const bottom = Math.min(H - 10, floor.y + 18);
  const keelPts = [[cx - Math.min(hullSpan * 0.45, 76), bottom - 14], [cx - 4, bottom], [cx + Math.min(hullSpan * 0.42, 70), bottom - 16]];
  const rightRev = [...right].reverse();
  const body = [[tipL, waterY], ...left, ...keelPts, ...rightRev, [tipR, waterY]];
  svg.append(el('path', { class: 'berg-body', fill: `url(#${gid}-ice)`, d: `M${body.map(([x, y]) => `${x.toFixed(1)},${y.toFixed(1)}`).join('L')}Z` }));

  // Subtle crystalline right-side shadow facet underwater for depth
  const rightFacet = [[cx + 4, waterY], [cx - 6, bottom], [cx + 42, bottom - 14], ...rightRev, [tipR, waterY]];
  svg.append(el('path', { class: 'berg-facet', fill: `url(#${gid}-shade)`, d: `M${rightFacet.map(([x, y]) => `${x.toFixed(1)},${y.toFixed(1)}`).join('L')}Z` }));

  // Two-tone faceted peak above water
  const peakY = Math.max(6, tip.y - 22);
  const ridgeX = tip.x + tip.w * 0.45;
  const pLeft = [[tipL, waterY], [tip.x + tip.w * 0.12, tip.y + 2], [ridgeX, peakY], [cx + 4, waterY]];
  const pRight = [[ridgeX, peakY], [tip.x + tip.w * 0.74, tip.y - 2], [tip.x + tip.w * 0.9, tip.y + 7], [tipR, waterY], [cx + 4, waterY]];
  const pFull = [[tipL, waterY], [tip.x + tip.w * 0.12, tip.y + 2], [ridgeX, peakY], [tip.x + tip.w * 0.74, tip.y - 2], [tip.x + tip.w * 0.9, tip.y + 7], [tipR, waterY]];
  svg.append(el('path', { class: 'berg-peak', d: `M${pFull.map(([x, y]) => `${x.toFixed(1)},${y.toFixed(1)}`).join('L')}Z` }));
  svg.append(el('path', { class: 'berg-peak-lit', d: `M${pLeft.map(([x, y]) => `${x.toFixed(1)},${y.toFixed(1)}`).join('L')}Z` }));
  svg.append(el('path', { class: 'berg-peak-shade', d: `M${pRight.map(([x, y]) => `${x.toFixed(1)},${y.toFixed(1)}`).join('L')}Z` }));

  // Gentle animated waterline
  svg.append(el('path', { class: 'berg-wave', d: `M0,${waterY} ${Array.from({ length: Math.ceil(W / 28) + 2 }, () => `q7,-3.5 14,0 t14,0`).join(' ')}` }));

  // Dependency lines from each concept to what it stands on
  const at = new Map([...berg.querySelectorAll('[data-id]')].map((e) => [e.dataset.id, e]));
  const edges = el('g', { class: 'berg-edges' });
  for (const [id, e] of at) {
    const a = offsetIn(e, berg);
    for (const r of (e.dataset.requires || '').split(' ').filter(Boolean)) {
      const t2 = at.get(r);
      if (!t2) continue;
      const b = offsetIn(t2, berg);
      if (b.y <= a.y + 4) continue;
      const x1 = a.x + a.w / 2, y1 = a.y + a.h, x2 = b.x + b.w / 2, y2 = b.y, my = (y1 + y2) / 2;
      edges.append(el('path', { d: `M${x1.toFixed(1)},${y1.toFixed(1)} C${x1.toFixed(1)},${my.toFixed(1)} ${x2.toFixed(1)},${my.toFixed(1)} ${x2.toFixed(1)},${y2.toFixed(1)}`, 'data-from': id, 'data-to': r, class: t2.classList.contains('pending') ? 'pend' : '' }));
    }
  }
  svg.append(edges);
  berg.querySelector('.berg-art')?.remove();
  berg.prepend(svg);

  // Interactive hover/focus: highlight dependency paths, related chips, and show glass summary tooltip
  if (!berg.dataset.hover) {
    berg.dataset.hover = '1';
    const tipCard = berg.querySelector('.berg-tooltip');
    const showTip = (chip) => {
      if (!tipCard || !chip || !chip.classList.contains('ice-pill')) {
        if (tipCard) tipCard.hidden = true;
        return;
      }
      const title = chip.dataset.title || '';
      const summary = chip.dataset.summary || '';
      const shared = chip.dataset.shared || '';
      const hint = chip.dataset.hint || '';
      tipCard.replaceChildren(
        h('strong', {}, title),
        summary ? h('p', {}, summary) : null,
        h('small', {}, [shared, hint].filter(Boolean).join(' · ')));
      tipCard.hidden = false;
      const pos = offsetIn(chip, berg);
      const bw = berg.offsetWidth;
      const tw = Math.min(280, bw - 24);
      tipCard.style.maxWidth = `${tw}px`;
      const th = tipCard.offsetHeight;
      const leftPos = Math.max(12, Math.min(bw - tipCard.offsetWidth - 12, pos.x + pos.w / 2 - tipCard.offsetWidth / 2));
      const above = pos.y - th - 10 > 8;
      tipCard.style.left = `${leftPos}px`;
      tipCard.style.top = `${above ? pos.y - th - 8 : pos.y + pos.h + 8}px`;
    };
    const lightUp = (target) => {
      const id = target?.dataset?.id || '';
      const rel = new Set(id ? [id] : []);
      for (const p of berg.querySelectorAll('.berg-edges path')) {
        const hot = !!id && (p.dataset.from === id || p.dataset.to === id);
        p.classList.toggle('hot', hot);
        if (hot) { rel.add(p.dataset.from); rel.add(p.dataset.to); }
      }
      for (const pillEl of berg.querySelectorAll('.ice-pill')) {
        pillEl.classList.toggle('rel', !!id && pillEl.dataset.id !== id && rel.has(pillEl.dataset.id));
        pillEl.classList.toggle('dim', !!id && !rel.has(pillEl.dataset.id));
      }
      showTip(target);
    };
    berg.addEventListener('pointerover', (e) => lightUp(e.target.closest('[data-id]')));
    berg.addEventListener('pointerleave', () => lightUp(null));
    berg.addEventListener('focusin', (e) => lightUp(e.target.closest('[data-id]')));
    berg.addEventListener('focusout', (e) => { if (!berg.contains(e.relatedTarget)) lightUp(null); });
  }
}

function conceptReader(c) {
  const m = c.meta;
  const node = nodeOf(c.slug);
  const body = stripEmoji(c.body.replace(/^# .*\n+/, '')).trim();
  return h('div', { class: 'reader-wrap concept', style: `--line-c: ${lineColor(node ? node.group : '')}` },
    h('header', { class: 'r-head' },
      h('button', { class: 'crumb', type: 'button', onclick: () => { vt(() => { S.concept = null; renderConcepts(); }); } }, icon('arrow-left'), S.note ? t('Iceberg de {title}', { title: S.note.meta.title }) : t('Conceptos')),
      h('p', { class: 'r-line' }, [t('Concepto'), m.category].filter(Boolean).join(' · ')),
      h('h1', {}, m.title || c.slug),
      (m.aliases || []).length ? h('div', { class: 'chips' }, m.aliases.map((a) => h('span', { class: 'chip' }, a))) : null,
      (c.requires || []).length ? h('div', { class: 'c-req' }, h('span', { class: 'hint' }, t('Se apoya en')), h('div', { class: 'links' }, c.requires.map((l) => h('button', { type: 'button', onclick: () => (writtenNode(l.slug) ? openNote(l.slug) : explainConcept(l.slug, { title: l.title, guide: owningGuide(l.slug) })) }, l.title)))) : null,
      h('div', { class: 'actions' }, notesButton(c.slug, m.title || c.slug, c.apunte),
        h('button', { class: 'btn small ghost', type: 'button', onclick: () => askAbout(t('Sobre «{title}»:', { title: m.title }) + ' ') }, icon('message-circle'), t('Preguntar')))),
    h('article', { class: 'r-article' }, mdBlock(stripSection(body, 'Aparece en'), { viz: vizCtx(c.slug) }),
      (c.guides || []).length ? h('section', { class: 'r-next' }, h('h2', {}, t('Aparece en')),
        c.guides.map((g) => h('button', { class: 'neighbor', type: 'button', onclick: () => openNote(g.slug) }, h('small', {}, t('Guía')), h('strong', {}, g.title)))) : null));
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
    icon('notebook-pen'), has ? t('Mis apuntes') : t('Tomar apuntes'));
}

async function openNotes(slug, title) {
  await flushNotes();
  if (N.slug !== slug) Object.assign(N, { slug, title, body: '', file: '', status: '', loading: true });
  show('notes');
  try {
    const r = await api(`/api/apuntes/${encodeURIComponent(slug)}`);
    if (N.slug !== slug) return;
    Object.assign(N, { body: r.body, file: r.file, loading: false, mode: r.body.trim() ? 'view' : 'edit', status: r.updated ? t('Guardado el {when}', { when: r.updated }) : '' });
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
    root.replaceChildren(h('div', { class: 'panel-empty' }, h('h3', {}, t('Apuntes')),
      h('p', {}, t('Abre una guía o un concepto y pulsa «Tomar apuntes»: lo que escribas queda junto a esa nota, en tu vault.'))));
    return;
  }
  const status = h('span', { class: 'notes-status', role: 'status' }, N.status);
  const file = h('input', { type: 'file', multiple: true, hidden: true });
  file.addEventListener('change', () => { attachToNotes(file.files); file.value = ''; });
  const mode = (m, label) => h('button', { type: 'button', 'aria-pressed': String(N.mode === m), onclick: () => { N.mode = m; renderNotes(); } }, label);
  let editor;
  if (N.loading) editor = h('p', { class: 'hint' }, t('Cargando…'));
  else if (N.mode === 'edit') {
    editor = h('textarea', { class: 'notes-text', 'aria-label': t('Apuntes de {title}', { title: N.title }), spellcheck: 'true',
      placeholder: t('Lo que entendiste, tus dudas, fórmulas con $…$, enlaces a otras notas con [[…]].\nPega o arrastra imágenes y archivos aquí.') });
    editor.value = N.body;
    editor.addEventListener('input', () => { N.body = editor.value; scheduleNotesSave(); });
    editor.addEventListener('blur', () => flushNotes());
    editor.addEventListener('paste', (e) => { if (e.clipboardData.files.length) { e.preventDefault(); attachToNotes(e.clipboardData.files, editor); } });
    editor.addEventListener('dragover', (e) => { if (e.dataTransfer.types.includes('Files')) { e.preventDefault(); editor.classList.add('drop'); } });
    editor.addEventListener('dragleave', () => editor.classList.remove('drop'));
    editor.addEventListener('drop', (e) => { editor.classList.remove('drop'); if (e.dataTransfer.files.length) { e.preventDefault(); attachToNotes(e.dataTransfer.files, editor); } });
  } else {
    editor = N.body.trim() ? h('div', { class: 'notes-view', ondblclick: () => { N.mode = 'edit'; renderNotes(); } }, notesMd(N.body))
      : h('p', { class: 'hint' }, t('Aún no escribiste nada aquí. Pulsa «Escribir».'));
  }
  root.replaceChildren(h('div', { class: 'notes' },
    h('p', { class: 'eyebrow' }, t('Apuntes')),
    h('h2', {}, h('a', { href: `#note/${N.slug}` }, N.title)),
    h('div', { class: 'notes-bar' },
      h('div', { class: 'seg', role: 'group', 'aria-label': t('Modo') }, mode('edit', t('Escribir')), mode('view', t('Ver'))),
      h('button', { class: 'btn small ghost', type: 'button', onclick: () => file.click() }, icon('paperclip'), t('Adjuntar')),
      file, status),
    editor,
    h('p', { class: 'hint notes-foot' }, t('Se guardan solos en {file} de tu vault.', { file: N.file || `apuntes/${N.slug}.md` }))));
  if (N.mode === 'edit' && editor.tagName === 'TEXTAREA') editor.focus({ preventScroll: true });
}

function scheduleNotesSave() {
  clearTimeout(N.timer);
  setNotesStatus(t('Sin guardar…'));
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
  setNotesStatus(t('Guardando…'));
  N.saving = api(`/api/apuntes/${encodeURIComponent(slug)}`, { method: 'PUT', body: { body } })
    .then(() => {
      if (N.slug === slug) setNotesStatus(t('Guardado'));
      const has = !!body.trim();
      if (S.note && S.note.slug === slug && !!S.note.apunte !== has) { S.note.apunte = has; renderReader(S.note); }
      if (S.concept && S.concept.slug === slug && !!S.concept.apunte !== has) { S.concept.apunte = has; renderConcepts(); }
    })
    .catch((err) => { if (N.slug === slug) setNotesStatus(t('No se guardó: {error}', { error: err.message })); });
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
  setNotesStatus(t('Subiendo…'));
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
const DIRS = { guias: t('Guía'), conceptos: t('Concepto'), apuntes: t('Apuntes'), flashcards: t('Tarjetas'), roadmaps: t('Roadmap'), daily: t('Diario') };
const DIRS_PLURAL = { guias: t('guías'), conceptos: t('conceptos'), apuntes: t('apuntes'), flashcards: t('tarjetas'), roadmaps: t('roadmaps'), daily: t('días') };
const VLT = { info: null, files: [], materials: [], hits: null, q: '', dir: '', adding: false, edit: null, timer: 0 };

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
    [VLT.info, VLT.files, VLT.materials] = await Promise.all([api('/api/vaults'), api('/api/vault/files'), api('/api/vault/materials')]);
  } catch (err) { root.replaceChildren(h('p', { class: 'hint', style: 'padding:24px' }, err.message)); return; }
  drawVault();
}

function openOutside(app, file) {
  return api('/api/vault/open', { method: 'POST', body: { app, dir: file?.dir || '', slug: file?.slug || '' } })
    .then(() => toast(app === 'obsidian' ? t('Abriendo en Obsidian…') : t('Abriendo la carpeta…')))
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
  const ONE = { guias: t('guía'), conceptos: t('concepto'), apuntes: t('nota de apuntes'), flashcards: t('mazo de tarjetas'), roadmaps: t('roadmap'), daily: t('día') };
  const counts = Object.keys(DIRS).filter((d) => stats.notes[d]).map((d) => `${stats.notes[d]} ${stats.notes[d] === 1 ? ONE[d] : DIRS_PLURAL[d]}`);
  if (VLT.materials.length) counts.push(tn(VLT.materials.length, '{n} archivo de material', '{n} archivos de material'));

  // the vaults: one row each, the one in use first
  const vaultRows = h('ul', { class: 'v-list' }, vaults.map((v) => h('li', { class: v.current ? 'on' : null },
    h('div', { class: 'v-main' }, h('strong', {}, v.name), h('small', {}, v.abs)),
    h('span', { class: 'v-meta' }, v.current ? t('en uso') : !v.exists ? t('no se encuentra') : tn(v.guides, '{n} guía', '{n} guías')),
    v.current ? null : h('div', { class: 'v-actions' },
      v.exists ? h('button', { class: 'btn small', type: 'button', onclick: () => useVault(v.path) }, t('Usar')) : null,
      h('button', { class: 'btn small ghost', type: 'button', title: t('Quitar de la lista (la carpeta no se borra)'), onclick: async () => {
        try { await api('/api/vaults/forget', { method: 'POST', body: { path: v.path } }); renderVault(); } catch (err) { toast(err.message); }
      } }, t('Quitar'))))));

  const pathIn = h('input', { type: 'text', placeholder: t('D:\\Universidad\\Calculo'), 'aria-label': t('Carpeta del vault'), spellcheck: 'false' });
  const addForm = VLT.adding ? h('form', { class: 'v-add', onsubmit: (e) => { e.preventDefault(); if (pathIn.value.trim()) useVault(pathIn.value.trim(), true); } },
    h('div', { class: 'v-add-row' }, pathIn,
      canPick ? h('button', { class: 'btn small ghost', type: 'button', onclick: async (e) => {
        const b = e.currentTarget;
        b.disabled = true;
        try { const r = await api('/api/vaults/pick', { method: 'POST', body: {} }); if (r.path) pathIn.value = r.path; } catch (err) { toast(err.message); }
        b.disabled = false;
        pathIn.focus();
      } }, icon('folder-open'), t('Elegir carpeta…')) : null,
      h('button', { class: 'btn small primary', type: 'submit' }, t('Abrir'))),
    h('p', { class: 'hint' }, t('Una carpeta vacía (o nueva) se vuelve un vault con sus carpetas; una que ya tiene notas se abre tal cual. Cada vault tiene sus propias conversaciones.'))) : null;

  // the notes: filter by folder, search by title and text
  const search = h('input', { type: 'search', value: VLT.q, placeholder: t('Buscar en títulos y texto…'), 'aria-label': t('Buscar notas') });
  search.addEventListener('input', () => {
    VLT.q = search.value;
    clearTimeout(VLT.timer);
    VLT.timer = setTimeout(async () => {
      VLT.hits = VLT.q.trim().length >= 2 ? await api(`/api/vault/search?q=${encodeURIComponent(VLT.q.trim())}`).catch(() => []) : null;
      drawNoteList();
    }, 220);
  });
  const segItems = [
    ['', t('Todas')],
    ...Object.entries(DIRS).filter(([d]) => stats.notes[d]).map(([d]) => [d, DIRS_PLURAL[d][0].toUpperCase() + DIRS_PLURAL[d].slice(1)]),
    ...(VLT.materials.length ? [['material', t('Material')]] : []),
  ];
  const seg = h('div', { class: 'seg', role: 'group', 'aria-label': t('Carpeta') },
    segItems.map(([d, label]) => h('button', { type: 'button', 'aria-pressed': String(VLT.dir === d), onclick: () => { VLT.dir = d; drawVault(); } }, label)));

  root.replaceChildren(h('div', { class: 'vault' },
    h('p', { class: 'eyebrow' }, t('Vault')),
    h('h2', {}, cur.name),
    h('p', { class: 'v-path' }, abs),
    h('p', { class: 'v-stats' }, [...counts, t('{size} en total', { size: fmtBytes(stats.bytes) })].join(' · ')),
    h('div', { class: 'actions' },
      h('button', { class: 'btn small', type: 'button', onclick: () => openOutside('folder') }, icon('folder-open'), t('Abrir la carpeta')),
      h('button', { class: 'btn small', type: 'button', onclick: () => openOutside('obsidian') }, icon('external-link'), t('Abrir en Obsidian'))),
    h('section', { class: 'v-section' },
      h('h3', {}, t('Tus vaults')),
      vaultRows,
      VLT.adding ? addForm : h('button', { class: 'btn small ghost v-add-btn', type: 'button', onclick: () => { VLT.adding = true; drawVault(); PANE_EL.vault.querySelector('.v-add input').focus(); } },
        icon('folder-plus'), t('Agregar o crear un vault'))),
    h('section', { class: 'v-section' },
      h('h3', {}, t('Notas')),
      h('div', { class: 'v-tools' }, search, seg),
      h('ul', { class: 'v-notes', id: 'v-notes' }))));
  drawNoteList();
}

function drawNoteList() {
  const ul = $('#v-notes');
  if (!ul) return;
  if (VLT.dir === 'material') {
    const q = slugify(VLT.q);
    const mats = VLT.materials.filter((m) => !q || slugify(`${m.topic} ${m.name}`).includes(q));
    if (!mats.length) { ul.replaceChildren(h('li', { class: 'hint' }, VLT.q ? t('Nada coincide.') : t('Aún no hay archivos de material.'))); return; }
    ul.replaceChildren(...mats.map((m) => {
      const href = `/api/material/${encodeURIComponent(m.topic)}/${encodeURIComponent(m.name)}`;
      return h('li', {},
        h('a', { class: 'v-open v-mat-link', href, target: '_blank', rel: 'noopener noreferrer' },
          h('strong', {}, m.name),
          h('small', {}, `${t('Material')} · ${m.topic} · ${fmtBytes(m.size)} · ${when(m.updated)}`)),
        h('div', { class: 'v-actions' },
          h('a', { class: 'icon-btn', href, target: '_blank', rel: 'noopener noreferrer', title: t('Abrir archivo'), 'aria-label': t('Abrir archivo') }, icon('external-link'))));
    }));
    return;
  }
  const rows = VLT.hits || VLT.files.filter((f) => !VLT.q.trim() || slugify(f.title).includes(slugify(VLT.q)));
  const list = rows.filter((f) => !VLT.dir || f.dir === VLT.dir);
  if (!list.length) { ul.replaceChildren(h('li', { class: 'hint' }, VLT.q ? t('Nada coincide.') : t('Aún no hay notas aquí.'))); return; }
  ul.replaceChildren(...list.slice(0, 300).map((f) => h('li', {},
    h('button', { class: 'v-open', type: 'button', onclick: () => openVaultFile(f) },
      h('strong', {}, f.title),
      h('small', {}, `${DIRS[f.dir] || f.dir} · ${when(f.updated)}`),
      f.snippet ? h('span', { class: 'v-snip' }, f.snippet) : null),
    h('div', { class: 'v-actions' },
      h('button', { class: 'icon-btn', type: 'button', title: t('Editar el Markdown'), 'aria-label': t('Editar {title}', { title: f.title }), onclick: () => editVaultFile(f) }, icon('file-pen-line')),
      h('button', { class: 'icon-btn', type: 'button', title: t('Abrir en Obsidian'), 'aria-label': t('Abrir {title} en Obsidian', { title: f.title }), onclick: () => openOutside('obsidian', f) }, icon('external-link')),
      h('button', { class: 'icon-btn', type: 'button', title: t('Mostrar en la carpeta'), 'aria-label': t('Mostrar {title} en la carpeta', { title: f.title }), onclick: () => openOutside('folder', f) }, icon('folder-open'))))));
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
  const say = (msg) => { E.status = msg; status.textContent = msg; };
  const save = async (force = false) => {
    try {
      const r = await api(`/api/vault/raw/${E.dir}/${encodeURIComponent(E.slug)}`, { method: 'PUT', body: { text: E.text, since: E.since, force } });
      Object.assign(E, { since: r.updated, dirty: false });
      say(t('Guardado'));
      loadGraph().catch(() => {});
      if (S.note && S.note.slug === E.slug && E.dir === 'guias') openNote(E.slug, { quiet: true });
      if (S.concept && S.concept.slug === E.slug && E.dir === 'conceptos') openNote(E.slug, { quiet: true });
      if (E.dir === 'apuntes' && N.slug === E.slug) N.slug = ''; // reload the notes from the file next time
    } catch (err) {
      if (err.status === 409 && confirm(t('La nota cambió desde que la abriste (la app o Obsidian la reescribió). ¿Guardar tu versión encima?'))) return save(true);
      say(err.message);
    }
  };
  const back = () => {
    if (E.dirty && !confirm(t('Tienes cambios sin guardar. ¿Salir sin guardarlos?'))) return;
    VLT.edit = null;
    renderVault();
  };
  const mode = (m, label) => h('button', { type: 'button', 'aria-pressed': String(E.mode === m), onclick: () => { E.mode = m; drawEditor(); } }, label);
  let body;
  if (E.mode === 'edit') {
    body = h('textarea', { class: 'v-text', spellcheck: 'false', 'aria-label': t('Markdown de {title}', { title: E.title }) });
    body.value = E.text;
    body.addEventListener('input', () => { E.text = body.value; E.dirty = true; say(t('Sin guardar')); });
    body.addEventListener('keydown', (e) => { if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 's') { e.preventDefault(); save(); } });
  } else {
    const md = E.text.replace(/^---\n[\s\S]*?\n---\n*/, '');
    body = h('div', { class: 'notes-view' }, E.dir === 'apuntes' ? notesMd(md) : mdBlock(md, { breaks: E.dir !== 'guias' && E.dir !== 'conceptos' }));
  }
  root.replaceChildren(h('div', { class: 'vault v-editor' },
    h('button', { class: 'crumb', type: 'button', onclick: back }, icon('arrow-left'), t('Todas las notas')),
    h('p', { class: 'eyebrow' }, `${DIRS[E.dir] || E.dir} · ${E.dir}/${E.slug}.md`),
    h('h2', {}, E.title),
    h('div', { class: 'notes-bar' },
      h('div', { class: 'seg', role: 'group', 'aria-label': t('Modo') }, mode('edit', t('Editar')), mode('view', t('Ver'))),
      h('button', { class: 'btn small primary', type: 'button', onclick: () => save() }, icon('save'), t('Guardar')),
      h('button', { class: 'btn small ghost', type: 'button', onclick: () => openOutside('obsidian', E) }, icon('external-link'), 'Obsidian'),
      status),
    body,
    h('p', { class: 'hint notes-foot' }, E.dir === 'guias' || E.dir === 'conceptos'
      ? t('Lo de arriba, entre las líneas ---, son las propiedades (YAML) que la app lee: cámbialas con cuidado. Si pides escribir esta nota de nuevo, la app la reemplaza; tus apuntes no se tocan. Ctrl+S guarda.')
      : t('Ctrl+S guarda.'))));
  if (E.mode === 'edit') body.focus({ preventScroll: true });
}
$('#btn-vault').addEventListener('click', () => { VLT.edit = null; show('vault'); });

/* ---------- Progreso: XP, streak, freezes, calendar & area stats ---------- */
const AREA_NAME = {
  matematicas: t('Matemáticas'),
  fisica: t('Física'),
  electronica: t('Electrónica'),
  quimica: t('Química'),
  biologia: t('Biología'),
  medicina: t('Medicina'),
  informatica: t('Informática'),
  musica: t('Música'),
  historia: t('Historia'),
  'ciencias-sociales': t('Ciencias sociales'),
  economia: t('Economía'),
  lenguas: t('Lenguas'),
  arte: t('Arte'),
  filosofia: t('Filosofía'),
  general: t('General'),
};
const EV_KIND = {
  guide: t('Guía generada'),
  read: t('Guía leída'),
  quiz: t('Prueba completada'),
  feynman: t('Explicación Feynman'),
  concept: t('Concepto explicado'),
  doubt: t('Duda resuelta'),
  flashcard: t('Repaso de tarjetas'),
  activity: t('Actividad completada'),
};
const fmtMinutes = (mins) => {
  const m = Math.max(0, Math.round(mins || 0));
  if (m >= 60) return t('{h} h {m} min', { h: Math.floor(m / 60), m: m % 60 });
  return t('{m} min', { m });
};

async function loadStats() {
  try {
    const prevMet = !!(S.stats && S.stats.goalMet);
    const hadStats = !!S.stats;
    S.stats = await api('/api/stats');
    drawStreakPill();
    if (hadStats && !prevMet && S.stats.goalMet) flashNarval('celebrando', 8000);
    else renderNarval();
    if (S.dock && S.dock.getPanel('progress')) drawProgress();
  } catch { /* non-fatal */ }
}

function drawStreakPill() {
  const st = S.stats;
  const btn = $('#btn-progress');
  if (!st || !btn) return;
  $('#streak-count').textContent = String(st.streak || 0);
  const pct = Math.min(100, Math.round(((st.todayXP || 0) / Math.max(1, st.dailyGoal || 50)) * 100));
  $('#streak-bar').style.width = `${pct}%`;
  btn.classList.toggle('met', !!st.goalMet);
  btn.classList.toggle('risk', !!st.atRisk);
  btn.title = `${t('Progreso, racha y XP')} · ${st.todayXP}/${st.dailyGoal} XP`;
}

async function renderProgress() {
  if (!S.stats) await loadStats();
  else { drawProgress(); loadStats(); }
}

function drawProgress() {
  const root = PANE_EL.progress;
  const st = S.stats;
  if (!root || !st) return;

  const pct = Math.min(100, Math.round(((st.todayXP || 0) / Math.max(1, st.dailyGoal || 50)) * 100));
  const left = Math.max(0, (st.dailyGoal || 50) - (st.todayXP || 0));
  const streakSub = st.atRisk
    ? h('span', { class: 'prog-warn' }, t('Racha en riesgo hoy'))
    : st.lostStreak
      ? h('span', { class: 'prog-warn' }, t('Tu racha se reinició ayer'))
      : h('span', {}, t('Mejor racha: {n}', { n: st.longest || 0 }));

  const freezeIcons = [];
  for (let i = 0; i < (st.maxFreezes || 2); i++) {
    freezeIcons.push(h('span', { class: `freeze-pip${i < (st.freezes || 0) ? ' on' : ''}`, 'aria-hidden': 'true' }, icon('snowflake')));
  }

  const kpis = h('div', { class: 'prog-grid' },
    h('div', { class: `prog-card${st.goalMet ? ' met' : ''}` },
      h('span', { class: 'prog-label' }, t('Meta de hoy')),
      h('strong', { class: 'prog-val' }, `${st.todayXP} / ${st.dailyGoal} XP`),
      h('div', { class: 'prog-track' }, h('i', { style: `width:${pct}%` })),
      h('small', {}, st.goalMet ? t('Meta cumplida') : t('Faltan {xp} XP', { xp: left }))),
    h('div', { class: `prog-card${st.atRisk ? ' risk' : ''}` },
      h('span', { class: 'prog-label' }, t('Racha')),
      h('strong', { class: 'prog-val prog-flame' }, icon('flame'), tn(st.streak || 0, '{n} día', '{n} días')),
      h('small', {}, streakSub)),
    h('div', { class: 'prog-card' },
      h('span', { class: 'prog-label' }, t('Congeladores')),
      h('strong', { class: 'prog-val prog-freezes' }, ...freezeIcons, h('span', {}, `${st.freezes || 0} / ${st.maxFreezes || 2}`)),
      h('small', {}, st.nextFreezeIn > 0
        ? tn(st.nextFreezeIn, 'Falta {n} día para ganar otro', 'Faltan {n} días para ganar otro')
        : t('Al máximo'))),
    h('div', { class: 'prog-card' },
      h('span', { class: 'prog-label' }, t('Total')),
      h('strong', { class: 'prog-val' }, `${st.totalXP || 0} XP`),
      h('small', { class: 'prog-time' }, icon('clock'), fmtMinutes(st.totalMinutes))),
  );

  // GitHub-style calendar heatmap (18 weeks = 126 days, columns = weeks, rows = days)
  const calDays = st.calendar || [];
  const cells = [];
  for (const d of calDays) {
    const tip = d.frozen
      ? t('{date} · protegido con congelador', { date: d.date })
      : t('{date} · {xp} XP', { date: d.date, xp: d.xp });
    cells.push(h('span', {
      class: `cal-cell l${d.level || 0}${d.frozen ? ' frozen' : ''}`,
      title: tip,
      'aria-label': tip,
    }));
  }
  const calendarSec = h('section', { class: 'prog-sec' },
    h('div', { class: 'prog-sec-head' },
      h('h3', {}, t('Actividad')),
      h('span', { class: 'hint' }, t('Últimas 18 semanas'))),
    h('div', { class: 'cal-wrap' }, h('div', { class: 'cal-grid', role: 'img', 'aria-label': t('Actividad') }, ...cells)),
    h('div', { class: 'cal-legend' },
      h('span', {}, t('Menos')),
      ...[0, 1, 2, 3, 4].map((lv) => h('i', { class: `cal-cell l${lv}` })),
      h('span', {}, t('Más')),
      h('span', { class: 'cal-frozen-key' }, h('i', { class: 'cal-cell l0 frozen' }), t('Protegido con congelador'))),
  );

  // Per-area horizontal bars
  const areas = st.areas || [];
  const maxXP = Math.max(1, ...areas.map((a) => a.xp || 0));
  const areaBody = areas.length === 0
    ? h('p', { class: 'hint' }, t('Empieza una guía o una prueba en el chat para ver tu progreso aquí.'))
    : h('ul', { class: 'area-list' }, areas.map((a, idx) => {
      const w = a.xp > 0 ? Math.max(4, Math.round((a.xp / maxXP) * 100)) : 0;
      const meta = [];
      if (a.guides) meta.push(tn(a.guides, '{n} guía', '{n} guías'));
      if (a.masteredGuides) meta.push(tn(a.masteredGuides, '{n} dominada', '{n} dominadas'));
      if (a.avgMastery) meta.push(t('Dominio medio {score}%', { score: a.avgMastery }));
      if (a.concepts) meta.push(tn(a.concepts, '{n} concepto', '{n} conceptos'));
      if (a.minutes) meta.push(fmtMinutes(a.minutes));
      return h('li', { class: 'area-row' },
        h('div', { class: 'area-head' },
          h('strong', {}, AREA_NAME[a.area] || a.area),
          h('span', { class: 'area-xp' }, `${a.xp} XP`)),
        h('div', { class: 'area-track' }, h('i', { style: `width:${w}%;background:var(--l${idx % 8})` })),
        meta.length ? h('small', { class: 'area-meta' }, meta.join(' · ')) : null);
    }));
  const areasSec = h('section', { class: 'prog-sec' },
    h('div', { class: 'prog-sec-head' }, h('h3', {}, t('Progreso por área'))),
    areaBody);

  // Recent XP events
  const recent = st.recent || [];
  const recentSec = recent.length === 0 ? null : h('section', { class: 'prog-sec' },
    h('div', { class: 'prog-sec-head' }, h('h3', {}, t('Actividad reciente'))),
    h('ul', { class: 'recent-list' }, recent.map((ev) => {
      const label = EV_KIND[ev.kind] || ev.kind;
      const areaLabel = AREA_NAME[ev.area] || ev.area || '';
      const clickable = ev.topic && nodeOf(ev.topic);
      const titleEl = clickable
        ? h('button', { class: 'recent-link', type: 'button', onclick: () => openNote(ev.topic) }, ev.title || ev.topic)
        : h('span', {}, ev.title || ev.topic || label);
      return h('li', {},
        h('div', { class: 'recent-main' },
          titleEl,
          h('small', {}, [label, areaLabel, ev.score ? `${ev.score}/100` : '', ev.date].filter(Boolean).join(' · '))),
        h('span', { class: 'recent-xp' }, `+${ev.xp} XP`));
    })));

  // Collectible flashcards summary in Progreso
  const cs = st.cards;
  const rarOrder = ['common', 'rare', 'epic', 'legendary'];
  const cardsSec = !cs || !cs.total ? null : h('section', { class: 'prog-sec' },
    h('div', { class: 'prog-sec-head' },
      h('h3', {}, t('Colección de tarjetas')),
      h('span', { class: 'hint' }, t('{got} de {total} coleccionadas', { got: cs.collected || 0, total: cs.total }))),
    h('div', { class: 'rar-summary' }, rarOrder.map((r) => h('div', { class: `rar-pill rar-${r}` },
      h('span', { class: 'rar-name' }, RARITY_LABEL[r]),
      h('strong', {}, `${(cs.byRarity && cs.byRarity[r]) || 0} / ${(cs.totals && cs.totals[r]) || 0}`)))));

  root.replaceChildren(h('div', { class: 'prog' },
    h('div', { class: 'prog-head' },
      h('h2', {}, t('Progreso')),
      h('button', { class: 'btn small ghost', type: 'button', onclick: () => show('settings') }, icon('settings'), t('Meta diaria de XP'))),
    kpis,
    cardsSec,
    calendarSec,
    areasSec,
    recentSec));
}

/* ---------- Practicar & Collectible FSRS Flashcards ---------- */
const RARITY_LABEL = {
  common: t('Común'),
  rare: t('Rara'),
  epic: t('Épica'),
  legendary: t('Legendaria'),
};
const RARITY_XP = { common: 5, rare: 8, epic: 12, legendary: 18 };
const CARDS = { data: null, scope: 'guide', revealed: false, busy: false, expanded: null };

function fmtInterval(mins) {
  if (!mins || mins < 60) return t('{m} min', { m: Math.max(1, Math.round(mins || 10)) });
  if (mins < 60 * 24) return t('{h} h', { h: Math.round(mins / 60) });
  return tn(Math.round(mins / (60 * 24)), '{n} día', '{n} días');
}

const STEP_RANK = { new: 0, prepared: 1, tested: 2, explained: 3 };
function renderPractice() {
  const root = PANE_EL.practice;
  if (!root) return;
  root.replaceChildren();
  const n = S.note;
  const fcHost = h('div', { id: 'fc-host' });
  if (!n) {
    root.append(h('div', { class: 'practice' },
      h('div', { class: 'panel-empty', style: 'padding-top:12px;padding-bottom:18px' }, h('h3', {}, t('Practicar y tarjetas')),
        h('p', {}, t('Abre una guía para preguntas y método Feynman, o repasa tus tarjetas coleccionables e importa mazos de Anki.')), guideList() || ''),
      fcHost));
    loadFlashcards(fcHost, null);
    return;
  }
  const m = n.meta;
  const rank = STEP_RANK[m.status || 'new'] ?? 0;
  const title = m.title || n.slug;
  const nInput = h('input', { type: 'number', min: 1, max: 30, value: 5, 'aria-label': t('Número de preguntas') });
  const diff = h('select', { 'aria-label': t('Dificultad') }, ['básica', 'media', 'avanzada'].map((d) => h('option', { value: d, selected: d === 'media' }, t(d))));
  const steps = [
    { label: t('Leer la guía'), done: rank >= 1 },
    { label: t('Responder preguntas'), done: rank >= 2 },
    { label: t('Explicarlo con tus palabras'), done: rank >= 3 },
  ];
  const current = steps.findIndex((s) => !s.done);
  root.append(h('div', { class: 'practice' },
    h('header', { class: 'c-head' }, h('p', { class: 'eyebrow' }, t('Practicar')), h('h2', {}, title),
      m.mastery ? h('div', { class: 'mastery' }, h('div', { class: 'row' }, h('span', {}, t('Lo que llevas')), h('span', {}, `${m.mastery}/100`)), h('div', { class: 'bar' }, h('i', { style: `width:${m.mastery}%` }))) : null),
    h('ol', { class: 'sequence', 'aria-label': t('Cómo afianzarlo') }, steps.map((s, i) => h('li', {},
      h('span', { class: `step${i === current ? ' go' : ''}`, 'data-state': s.done ? 'done' : i === current ? 'current' : 'todo' }, h('span', { class: 'step-n' }, s.done ? '✓' : String(i + 1)), s.label)))),
    fcHost,
    h('section', { class: 'p-card' },
      h('h3', {}, t('Preguntas')),
      h('p', { class: 'hint' }, t('Piden razonar o predecir, no memorizar. Se corrigen al final y el resultado queda en la guía.')),
      h('div', { class: 'quiz-opts' }, nInput, 'preguntas, dificultad', diff,
        h('button', { class: 'btn primary small', type: 'button', onclick: () => {
          const q = Number(nInput.value) || 5;
          chatAction({ action: 'quiz', n: q, difficulty: diff.value }, t('Quiz de «{title}»: {n} preguntas, {difficulty}', { title, n: q, difficulty: t(diff.value) }));
        } }, t('Empezar')))),
    h('section', { class: 'p-card' },
      h('h3', {}, t('Explícamelo tú')),
      h('p', { class: 'hint' }, t('Me lo explicas como a alguien que no sabe nada. Te señalo los huecos y te pregunto lo que preguntaría un novato.')),
      h('button', { class: 'btn primary small', type: 'button', onclick: () => chatAction({ action: 'feynman' }, t('Quiero explicar «{title}»', { title })) }, t('Empezar a explicar'))),
    h('section', { class: 'p-card' },
      h('h3', {}, t('Simulación')),
      h('p', { class: 'hint' }, t('Una página interactiva para jugar con el tema, aparte de los visuales de la guía.')),
      n.hasSimulation
        ? h('button', { class: 'btn small', type: 'button', onclick: () => openSim(n.slug, title) }, t('Abrir simulación'))
        : h('button', { class: 'btn small', type: 'button', onclick: () => chatAction({ action: 'interactive', brief: '' }, t('Simulación de «{title}»', { title })) }, t('Generar simulación')))));
  loadFlashcards(fcHost, n);
}

async function loadFlashcards(host, note) {
  const deckParam = note && CARDS.scope === 'guide' ? `?deck=${encodeURIComponent(note.slug)}` : '';
  try {
    CARDS.data = await api(`/api/cards${deckParam}`);
    CARDS.revealed = false;
    drawFlashcards(host, note);
  } catch (err) {
    host.replaceChildren(h('p', { class: 'hint' }, err.message));
  }
}

function drawFlashcards(host, note) {
  const data = CARDS.data;
  if (!data || !host) return;
  const queue = data.queue || [];
  const cards = data.cards || [];
  const decks = data.decks || [];
  const activeDeck = note ? decks.find((d) => d.slug === note.slug || d.topic === note.slug) : null;
  const collected = cards.filter((c) => c.collected).length;

  const importInput = h('input', { type: 'file', accept: '.txt,.tsv,.csv', hidden: true });
  importInput.addEventListener('change', async () => {
    const f = importInput.files && importInput.files[0];
    importInput.value = '';
    if (!f) return;
    const fd = new FormData();
    fd.append('file', f, f.name);
    try {
      const r = await api('/api/cards/import', { method: 'POST', body: fd });
      toast(tn(r.total, 'Importada {n} tarjeta desde Anki.', 'Importadas {n} tarjetas desde Anki.'));
      CARDS.scope = 'all';
      await loadFlashcards(host, note);
      loadStats();
    } catch (err) { toast(err.message); }
  });

  const genBtn = note ? h('button', {
    class: 'btn small primary', type: 'button', disabled: CARDS.busy,
    onclick: async () => {
      CARDS.busy = true;
      drawFlashcards(host, note);
      try {
        const r = await api('/api/cards/generate', { method: 'POST', body: { slug: note.slug } });
        toast(tn(r.count, 'Se generó {n} tarjeta coleccionable.', 'Se generaron {n} tarjetas coleccionables.'));
        CARDS.scope = 'guide';
        await loadFlashcards(host, note);
        loadStats();
      } catch (err) { toast(err.message); } finally {
        CARDS.busy = false;
        drawFlashcards(host, note);
      }
    },
  }, icon('sparkles'), CARDS.busy ? t('Generando tarjetas…') : activeDeck ? t('Regenerar tarjetas') : t('Generar tarjetas')) : null;

  const exportHref = activeDeck && CARDS.scope === 'guide'
    ? `/api/cards/export?deck=${encodeURIComponent(activeDeck.slug)}`
    : '/api/cards/export';

  const scopeSeg = note ? h('div', { class: 'seg', role: 'group', 'aria-label': t('Mazo') },
    h('button', { type: 'button', 'aria-pressed': String(CARDS.scope === 'guide'), onclick: () => { CARDS.scope = 'guide'; loadFlashcards(host, note); } }, t('Esta guía')),
    h('button', { type: 'button', 'aria-pressed': String(CARDS.scope === 'all'), onclick: () => { CARDS.scope = 'all'; loadFlashcards(host, note); } }, t('Todos los mazos'))) : null;

  // Review stage (FSRS queue)
  let stage;
  if (queue.length > 0) {
    const c = queue[0];
    const rar = c.rarity || 'common';
    const ivls = c.intervals || {};
    const rate = async (rating) => {
      try {
        const res = await api('/api/cards/review', { method: 'POST', body: { deck: c.deck, cardId: c.id, rating } });
        if (res.newlyCollected) {
          toast(t('¡Tarjeta {rarity} coleccionada! +{xp} XP', { rarity: RARITY_LABEL[rar] || rar, xp: res.xpGained }));
          flashNarval(rar === 'legendary' || rar === 'epic' ? 'celebrando' : 'feliz', 5000);
        }
        await loadFlashcards(host, note);
        loadStats();
      } catch (err) { toast(err.message); }
    };
    stage = h('div', { class: `flash fc-card rar-${rar}` },
      h('div', { class: 'fc-top' },
        h('span', { class: `rar-badge rar-${rar}` }, RARITY_LABEL[rar] || rar, ` · +${RARITY_XP[rar] || 5} XP`),
        h('span', { class: 'fc-meta' }, `${c.deckTitle} · ${tn(queue.length, '{n} para hoy', '{n} para hoy')}`)),
      h('div', { class: 'fc-front' }, mdBlock(c.front)),
      CARDS.revealed
        ? h('div', { class: 'fc-back' },
          h('hr'),
          mdBlock(c.back),
          h('div', { class: 'fc-ratings' },
            h('button', { class: 'btn small fc-r1', type: 'button', onclick: () => rate(1) }, h('strong', {}, t('Otra vez')), h('small', {}, fmtInterval(ivls[1]))),
            h('button', { class: 'btn small fc-r2', type: 'button', onclick: () => rate(2) }, h('strong', {}, t('Difícil')), h('small', {}, fmtInterval(ivls[2]))),
            h('button', { class: 'btn small fc-r3', type: 'button', onclick: () => rate(3) }, h('strong', {}, t('Bien')), h('small', {}, fmtInterval(ivls[3]))),
            h('button', { class: 'btn small fc-r4', type: 'button', onclick: () => rate(4) }, h('strong', {}, t('Fácil')), h('small', {}, fmtInterval(ivls[4])))))
        : h('div', { class: 'actions' },
          h('button', { class: 'btn small primary', type: 'button', onclick: () => { CARDS.revealed = true; drawFlashcards(host, note); } }, t('Dar la vuelta'))));
  } else if (cards.length > 0) {
    stage = h('p', { class: 'hint fc-done' }, t('¡Al día! No tienes tarjetas pendientes por ahora. Vuelve cuando toque el próximo repaso.'));
  } else {
    stage = h('p', { class: 'hint' }, t('Genera tarjetas coleccionables desde la guía o importa un mazo de Anki (.txt) para empezar a repasar con FSRS.'));
  }

  // Collectible Album grid for current scope
  const albumSec = cards.length === 0 ? null : h('div', { class: 'fc-album' },
    h('div', { class: 'fc-album-head' },
      h('strong', {}, t('Colección')),
      h('span', { class: 'hint' }, t('{got} de {total} coleccionadas', { got: collected, total: cards.length }))),
    h('div', { class: 'fc-grid' }, cards.map((c) => {
      const rar = c.rarity || 'common';
      const open = CARDS.expanded === `${c.deck}:${c.id}`;
      return h('button', {
        type: 'button',
        class: `fc-tile rar-${rar}${c.collected ? ' unlocked' : ' locked'}${open ? ' open' : ''}`,
        onclick: () => {
          if (!c.collected) return;
          CARDS.expanded = open ? null : `${c.deck}:${c.id}`;
          drawFlashcards(host, note);
        },
      },
      h('span', { class: `rar-badge rar-${rar}` }, RARITY_LABEL[rar] || rar),
      c.collected
        ? h('div', { class: 'fc-tile-body' },
          h('strong', {}, c.front),
          open ? h('p', { class: 'fc-tile-ans' }, c.back) : null)
        : h('div', { class: 'fc-tile-locked' }, h('span', {}, '?'), h('small', {}, t('Por coleccionar'))));
    })));

  // Imported / unguided decks -> one-click "Crear guía" (with concepts left in pending iceberg!)
  const unguided = decks.filter((d) => !d.hasGuide);
  const ankiGuidesSec = unguided.length === 0 ? null : h('div', { class: 'fc-anki-decks' },
    h('strong', {}, t('Mazos sin guía')),
    h('p', { class: 'hint' }, t('Crea una guía a partir de tus tarjetas importadas; los conceptos previos quedarán bajo el agua en el iceberg para explicarlos cuando quieras.')),
    h('ul', { class: 'fc-deck-list' }, unguided.map((d) => h('li', {},
      h('div', {}, h('strong', {}, d.title), h('small', {}, tn(d.total, '{n} tarjeta', '{n} tarjetas'))),
      h('button', {
        class: 'btn small primary', type: 'button',
        onclick: () => chatAction({ action: 'deck-guide', deck: d.slug }, t('Crear guía desde el mazo «{title}»', { title: d.title })),
      }, icon('sparkles'), t('Crear guía'))))));

  host.replaceChildren(h('section', { class: 'p-card fc-studio' },
    h('div', { class: 'fc-head' },
      h('h3', {}, t('Tarjetas coleccionables')),
      scopeSeg),
    h('div', { class: 'actions fc-tools' },
      genBtn,
      h('button', { class: 'btn small', type: 'button', onclick: () => importInput.click() }, icon('download'), t('Importar Anki')),
      decks.length ? h('a', { class: 'btn small ghost', href: exportHref, download: '' }, icon('external-link'), t('Exportar a Anki')) : null,
      importInput),
    stage,
    albumSec,
    ankiGuidesSec));
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
    // a concept several guides stand on: one node, ringed, with an edge to each guide
    { selector: 'node[shared > 1]', style: { width: 15, height: 15, 'outline-width': 2, 'outline-color': muted, 'outline-offset': 2, 'outline-opacity': .8 } },
    { selector: '.gone', style: { display: 'none' } },
    { selector: 'edge', style: {
      width: 3, 'line-color': lineOf(-1), 'curve-style': 'bezier', 'line-cap': 'round', opacity: .75,
      'z-index-compare': 'manual', 'z-index': 1, 'overlay-opacity': 0 } },
    { selector: 'edge[kind="related"]', style: { 'line-style': 'dashed', width: 1.2, opacity: .45, 'line-color': muted } },
    { selector: 'edge[pend = 1]', style: { 'line-style': 'dotted', width: 1.5, 'line-color': line, opacity: 1 } },
    // from a concept to a guide drawn in another block: bowed, so it does not run over the nodes between
    { selector: 'edge[cross = 1]', style: { 'curve-style': 'unbundled-bezier', 'control-point-distances': 70, 'control-point-weights': .5 } },
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
  renderMapHide();
  const emptyChat = S.mapScope === 'chat' && !g.nodes.length;
  $('#map-empty').hidden = g.nodes.length > 0;
  $('#map-empty-title').textContent = emptyChat ? t('Esta conversación aún no tiene guías') : t('Tu mapa está vacío');
  $('#map-empty-text').textContent = emptyChat
    ? t('Cuando esta conversación tenga una guía o explique un concepto, aparecerán aquí con lo que hay debajo.')
    : t('Pide una guía en el chat. Cada guía y sus conceptos aparecerán aquí, conectados.');
  $('#map-see-all').hidden = !(emptyChat && S.graph.nodes.length);
  const sig = `${S.mapScope}:${chatID}:${graphSignature(g)}`;
  const same = S.mapSig === sig;
  S.mapSig = sig;
  const lineOf = (k) => (S.lines.has(k) ? S.lines.get(k) : -1);
  const lineOfNode = new Map(g.nodes.map((n) => [n.id, lineOf(n.group)]));
  const kindOf = new Map(g.nodes.map((n) => [n.id, n.kind]));
  const groupOf = new Map(g.nodes.map((n) => [n.id, n.group]));
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
    for (const n of g.nodes) S.cy.getElementById(n.id).data({ status: n.status, label: n.label, line: lineOfNode.get(n.id), shared: n.shared || 0, foreign: n.foreign ? 1 : 0 });
  } else {
    S.cy.elements().remove();
    const { pos, labels } = layeredLayout(g);
    S.cy.add([
      ...g.nodes.map((n) => ({ group: 'nodes', data: { id: n.id, label: n.label, status: n.status, kind: n.kind, grp: n.group, line: lineOfNode.get(n.id), shared: n.shared || 0, foreign: n.foreign ? 1 : 0 }, position: pos[n.id] })),
      ...labels.map((l) => ({ group: 'nodes', data: { id: `gl:${l.group}`, label: l.label, grp: l.group, line: lineOf(l.group) }, position: { x: l.x, y: l.y }, classes: 'grouplabel', selectable: false, grabbable: false })),
      ...g.edges.map((e, i) => ({ group: 'edges', data: { id: `e${i}`, source: e.from, target: e.to, kind: e.kind, line: lineOfNode.get(e.to), pend: kindOf.get(e.from) === 'pending' || kindOf.get(e.to) === 'pending' ? 1 : 0, cross: groupOf.get(e.from) !== groupOf.get(e.to) ? 1 : 0 } })),
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

// What the map leaves out: the concepts still to explain, and (in a chat's map) the concepts
// another conversation wrote that this one only links.
S.mapHide = { pending: false, foreign: false, ...store.get('mapHide', {}) };
function renderMapHide() {
  for (const b of $$('#map-hide button')) {
    b.setAttribute('aria-pressed', String(!!S.mapHide[b.dataset.hide]));
    if (b.dataset.hide === 'foreign') b.hidden = S.mapScope !== 'chat';
  }
}
for (const b of $$('#map-hide button')) {
  b.addEventListener('click', () => {
    S.mapHide[b.dataset.hide] = !S.mapHide[b.dataset.hide];
    store.set('mapHide', S.mapHide);
    renderMapHide();
    applyFilters();
  });
}

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
  box.append(chip('', t('Todas')), ...keys.map((k) => chip(k, groupTitle(k))));
}

function applyFilters() {
  if (!S.cy) return;
  const q = slugify($('#search').value);
  S.cy.batch(() => {
    S.cy.nodes().not('.grouplabel').forEach((n) => {
      const okQ = !q || slugify(n.data('label')).includes(q) || n.id().includes(q);
      const okR = !S.filter || n.data('grp') === S.filter;
      n.toggleClass('dim', !(okQ && okR));
      n.toggleClass('gone', (S.mapHide.pending && n.data('kind') === 'pending') || (S.mapHide.foreign && S.mapScope === 'chat' && n.data('foreign') === 1));
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
  const box = $('#transcript');
  if (T.auto) box.scrollTop = box.scrollHeight;
}
$('#transcript').addEventListener('scroll', () => {
  const box = $('#transcript');
  T.auto = box.scrollHeight - box.scrollTop - box.clientHeight < 80;
});

function resetTranscript() {
  clearTimeout(T.timer);
  T.anim?.disable(); // a new conversation replaces the old one at once, without every message flying out
  $('#transcript').replaceChildren();
  Object.assign(T, { raw: '', bubble: null, progress: null, plan: null, choiceCard: null, echo: null, auto: true });
  requestAnimationFrame(() => T.anim?.enable());
}

/* ---------- Narval mascot state & onboarding tutorial ---------- */
const NARVAL = { mood: 'normal', flash: '', flashTimer: 0, sleeping: false, idleTimer: 0, phase: '' };
const IDLE_SLEEP_MS = 4 * 60 * 1000;

function pickNarvalMood() {
  if (NARVAL.flash) return NARVAL.flash;
  if (S.working) return (NARVAL.phase === 'quiz' || NARVAL.phase === 'feynman') ? 'concentrado' : 'estudiando';
  if (NARVAL.sleeping) return 'dormido';
  const st = S.stats;
  if (st) {
    if (st.goalMet) return 'feliz';
    if (st.atRisk) return 'preocupado';
    if (st.lostStreak) return 'triste';
  }
  return (S.graph && S.graph.nodes && S.graph.nodes.length === 0) ? 'mirando' : 'normal';
}

function renderNarval() {
  if (!window.Narval) return;
  const mood = pickNarvalMood();
  NARVAL.mood = mood;
  const hdr = $('#hdr-narval');
  if (hdr) hdr.replaceChildren(Narval.svg(mood));
  const wel = $('#welcome-narval');
  if (wel) wel.replaceChildren(Narval.svg(mood));
}

function flashNarval(mood, ms = 7000) {
  clearTimeout(NARVAL.flashTimer);
  NARVAL.flash = mood;
  renderNarval();
  NARVAL.flashTimer = setTimeout(() => { NARVAL.flash = ''; renderNarval(); }, ms);
}

function resetNarvalIdle() {
  clearTimeout(NARVAL.idleTimer);
  if (NARVAL.sleeping) { NARVAL.sleeping = false; renderNarval(); }
  NARVAL.idleTimer = setTimeout(() => {
    if (!S.working) { NARVAL.sleeping = true; renderNarval(); }
  }, IDLE_SLEEP_MS);
}
for (const ev of ['pointerdown', 'keydown']) window.addEventListener(ev, resetNarvalIdle, { passive: true });
resetNarvalIdle();

const TUTORIAL_STEPS = () => [
  {
    mood: 'feliz',
    title: t('¡Hola! Soy tu compañero de estudio'),
    text: t('Pregúntame cualquier tema en el chat o pídeme una guía. También puedes adjuntar apuntes o imágenes y armo guías con visuales interactivos.'),
  },
  {
    mood: 'mirando',
    title: t('El iceberg de conceptos'),
    text: t('Cada guía mapea lo que da por sabido, nivel por nivel. Elige cuántos niveles escribir al inicio y usa «Bajar un nivel más» cuando necesites ir más hondo.'),
  },
  {
    mood: 'estudiando',
    title: t('Un mapa que se conecta solo'),
    text: t('Todas tus guías y conceptos aparecen conectados en el panel Mapa. Si dos temas comparten una base, se reutiliza la misma nota automáticamente.'),
  },
  {
    mood: 'concentrado',
    title: t('Practica y explícalo con tus palabras'),
    text: t('En Practicar tienes preguntas de razonamiento y el método Feynman. En un roadmap, aprobar un tema desbloquea el siguiente.'),
  },
  {
    mood: 'celebrando',
    title: t('Racha, XP y congeladores'),
    text: t('Cada guía, lectura, concepto y práctica suma XP hacia tu meta diaria. Cada 7 días seguidos ganas un congelador por si algún día faltas.'),
  },
  {
    mood: 'normal',
    title: t('Ordena los paneles a tu gusto'),
    text: t('Los paneles están fijos para que nada se mueva por accidente. Cuando quieras dividir la pantalla o mover pestañas, abre el menú de paneles y pulsa «Editar diseño».'),
  },
];

let tutIndex = 0;
function drawTutorialStep() {
  const steps = TUTORIAL_STEPS();
  tutIndex = Math.max(0, Math.min(steps.length - 1, tutIndex));
  const st = steps[tutIndex];
  $('#tut-step-label').textContent = t('Paso {n} de {total}', { n: tutIndex + 1, total: steps.length });
  $('#tut-art').replaceChildren(window.Narval ? Narval.svg(st.mood) : '');
  $('#tut-title').textContent = st.title;
  $('#tut-text').textContent = st.text;
  $('#tut-dots').replaceChildren(...steps.map((_, i) => h('i', { class: i === tutIndex ? 'on' : null })));
  $('#tut-prev').hidden = tutIndex === 0;
  $('#tut-next').textContent = tutIndex === steps.length - 1 ? t('Empezar') : t('Siguiente');
}
function closeTutorial() {
  const dlg = $('#tutorial-dialog');
  if (dlg && dlg.open) dlg.close();
  if (S.stats) S.stats.tutorialSeen = true;
  api('/api/tutorial/seen', { method: 'POST', body: {} }).catch(() => {});
}
function openTutorial(force = false) {
  const dlg = $('#tutorial-dialog');
  if (!dlg) return;
  if (!force && S.stats && S.stats.tutorialSeen) return;
  tutIndex = 0;
  drawTutorialStep();
  if (!dlg.open) dlg.showModal();
}
$('#tut-prev').addEventListener('click', () => { tutIndex--; drawTutorialStep(); });
$('#tut-next').addEventListener('click', () => {
  if (tutIndex >= TUTORIAL_STEPS().length - 1) closeTutorial();
  else { tutIndex++; drawTutorialStep(); }
});
$('#tut-skip').addEventListener('click', closeTutorial);
$('#tutorial-dialog').addEventListener('cancel', (e) => { e.preventDefault(); closeTutorial(); });

function emptyState() {
  const pet = () => {
    const reactions = ['feliz', 'celebrando', 'mirando'];
    flashNarval(reactions[Math.floor(Math.random() * reactions.length)], 3200);
  };
  const narvalBtn = h('button', {
    class: 'welcome-narval', type: 'button', id: 'welcome-narval',
    'aria-label': t('Saludar al narval'), title: t('Saludar al narval'), onclick: pet,
  }, window.Narval ? Narval.svg(pickNarvalMood()) : null);
  return h('div', { class: 'chat-empty' },
    narvalBtn,
    h('p', { class: 'eyebrow' }, 'StudyHomie'),
    h('h1', {}, Phrases.welcome()),
    h('p', { class: 'lead' }, t('Pregunta lo que sea o pide una guía: te explico el tema y lo que da por sabido, hasta lo básico.')),
  );
}

// the + of the composer: the actions that start something bigger than a question
function closePlus() { $('#plus-menu').hidden = true; $('#btn-plus').setAttribute('aria-expanded', 'false'); }
function dailyForm() {
  // today's challenge: an optional theme; empty draws from your interests
  const field = h('input', { type: 'text', name: 'field', list: 'daily-fields', autocomplete: 'off', placeholder: t('Cualquier tema, o vacío para sorprenderte'), 'aria-label': t('Temática del reto') });
  const form = h('form', { class: 'daily-pick', onsubmit: (e) => {
    e.preventDefault();
    const f = field.value.trim();
    closePlus();
    chatAction({ action: 'daily', field: f }, f ? t('El reto de hoy, sobre {field}', { field: f }) : t('El reto de hoy'));
  } },
    h('label', {}, t('Reto de hoy sobre')),
    field,
    h('datalist', { id: 'daily-fields' }, (S.info?.interests || []).map((i) => h('option', { value: i }))),
    h('button', { class: 'btn small primary', type: 'submit' }, t('Sortear')));
  $('#plus-menu').replaceChildren(form);
  field.focus();
}
$('#btn-plus').addEventListener('click', (e) => {
  e.stopPropagation();
  const menu = $('#plus-menu');
  if (!menu.hidden) { closePlus(); return; }
  menu.replaceChildren(
    h('button', { type: 'button', onclick: (ev) => { ev.stopPropagation(); dailyForm(); } }, icon('sparkles'), t('El reto de hoy')),
    h('button', { type: 'button', onclick: () => { closePlus(); askAbout(t('Quiero aprender toda una materia:') + ' '); } }, icon('map'), t('Planear una materia')),
    h('button', { type: 'button', onclick: () => { closePlus(); $('#material-input').click(); } }, icon('file'), t('Subir apuntes')));
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
  S.working = !!on;
  if (!on) NARVAL.phase = '';
  $('#working').hidden = !on;
  if (text) $('#working-text').textContent = text;
  // a long wait reads better when the phrase changes now and then
  clearInterval(workingTimer);
  if (on && Phrases.has('thinking', $('#working-text').textContent)) {
    workingTimer = setInterval(() => { $('#working-text').textContent = Phrases.pick('thinking'); }, 6000);
  }
  $('#btn-cancel').hidden = !on;
  $('#session-dot').hidden = !on;
  renderNarval();
}

// Questions with a closed set of answers are asked with buttons instead of a text box.
function quickOptions(e) {
  const label = e.key || e.label || ''; // the Spanish question tells which one it is
  if (e.kind !== 'line') return null;
  if (/\(s\/n\)\s*$/i.test(label)) {
    const [yes, no] = /aceptas/i.test(label) ? [t('Aceptar'), t('Otro tema')]
      : /respondemos|comprobamos|prueba/i.test(label) ? [t('Responder preguntas'), t('Ahora no')]
        : /explicas|feynman/i.test(label) ? [t('Explicar'), t('Terminar aquí')]
          : /genero/i.test(label) ? [t('Generar'), t('Omitir')]
            : /empezamos/i.test(label) ? [t('Empezar'), t('Ahora no')] : [t('Sí'), t('No')];
    return [{ label: yes, reply: 's', primary: true }, { label: no, reply: 'n' }];
  }
  if (/dificultad/i.test(label)) return ['básica', 'media', 'avanzada'].map((d) => ({ label: t(d)[0].toUpperCase() + t(d).slice(1), reply: d, primary: d === 'media' }));
  if (/cuántas preguntas/i.test(label)) return [3, 5, 8, 10].map((n) => ({ label: t('{n} preguntas', { n }), reply: String(n), primary: n === 5 }));
  return null;
}

// The composer follows what the run waits for: a chat message, a short answer, an explanation, or nothing.
function setComposer(mode, e = {}) {
  const form = $('#composer'), ta = $('#reply');
  form.dataset.kind = mode;
  // a yes/no question gets buttons: its "(s/n)" ending, in whatever language, is not shown
  const label = (e.label || '').replace(/\s*\([^()]{1,9}\)\s*$/, (m) => (/\(s\/n\)\s*$/i.test(e.key || e.label || '') ? '' : m)).trim();
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
    chat: t('Pregunta lo que quieras, o pide una guía: «Explícame la transformada de Fourier, enfocada en audio»'),
    line: e.def ? t('Enter para usar «{answer}»', { answer: e.def }) : t('Escribe tu respuesta'),
    block: t('Explícalo con tus palabras, como si el otro no supiera nada…'),
    busy: t('Espera a que termine, o detenlo.'),
  }[mode];
  $('#composer-hint').textContent = mode === 'block' ? t('Ctrl + Enter para enviar') : '';
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
  if (run && !run.done) { toast(t('Espera a que termine lo que está en curso, o detenlo.')); return false; }
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
  if (mode === 'block' && !text.trim()) { toast(t('Escribe tu explicación antes de enviar.')); return; }
  ta.value = '';
  sendReply(text, false);
});
$('#reply').addEventListener('keydown', (e) => {
  if (e.key !== 'Enter' || e.isComposing) return;
  const block = $('#composer').dataset.kind === 'block';
  if (block ? (e.ctrlKey || e.metaKey) : !e.shiftKey) { e.preventDefault(); $('#composer').requestSubmit(); }
});
$('#btn-finish').addEventListener('click', () => { const text = $('#reply').value; $('#reply').value = ''; sendReply(text, true); });
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
  if (STAGED.some((f) => !f.id)) { toast(t('Espera a que terminen de subir los adjuntos.')); return null; }
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
    await chatSend({ text: '', files: up.files, material: true, label: t('Mis apuntes: {files}', { files: [...files].map((f) => f.name).join(', ') }) });
  } catch (err) { toast(err.message); setWorking(false); } finally { e.target.value = ''; }
});

// "write now": 0 (the guide alone) to 4 levels of concept notes; the iceberg maps that far, and digging goes further
function renderDepth() {
  $('#depth-stations').replaceChildren(...DEPTH.map((label, d) => h('button', {
    type: 'button', role: 'radio', 'aria-checked': String(d === S.depth), class: d <= S.depth ? 'on' : null,
    title: `${d}: ${label}`, 'aria-label': t('Escribir ahora: {label}', { label }),
    onclick: () => { S.depth = d; store.set('depth', d); renderDepth(); },
  }, String(d))));
  $('#depth-text').textContent = DEPTH[S.depth];
}

/* ---------- cards: structured output drawn as elements ---------- */
function scoreTone(score) { return score >= 70 ? 'good' : score >= 40 ? 'mid' : 'low'; }

function renderCard(c, run) {
  switch (c.kind) {
    case 'phase':
      if (!run.replay) { NARVAL.phase = c.phase || ''; renderNarval(); }
      break;
    case 'status': if (!run.replay && !run.done) setWorking(true, c.detail); break;
    case 'plan': T.plan = planCard(c.plan, run); addMsg('card', T.plan.el); break;
    case 'progress': progressCard(c.items); break;
    case 'guide':
      T.progress = null;
      addMsg('card', h('div', { class: 'rich rich-guide' },
        h('div', { class: 'rich-eyebrow' }, t('Guía lista')),
        h('h3', {}, c.topic),
        h('div', { class: 'actions' },
          h('button', { class: 'btn small primary', type: 'button', onclick: () => openNote(c.slug) }, icon('book-open'), t('Leer la guía')),
          h('button', { class: 'btn small', type: 'button', onclick: async () => { await openNote(c.slug, { quiet: true }); show('concepts'); } }, icon('mountain-snow'), t('Ver el iceberg')))));
      if (!run.replay) {
        flashNarval('celebrando', 7000);
        loadGraph().catch(() => {}).then(() => openNote(c.slug));
      }
      break;
    case 'roadmap': {
      const rm = { slug: c.slug, title: c.topic, pass: c.score, steps: c.steps || [] };
      addMsg('card', h('div', { class: 'rich rich-roadmap' },
        h('div', { class: 'rich-eyebrow' }, icon('map'), `Ruta · ${rm.steps.length} temas`),
        h('h3', {}, rm.title),
        h('p', { class: 'hint' }, t('Se estudia en orden: cada tema se desbloquea con {pass}/100 en el anterior, y su guía se escribe entonces.', { pass: rm.pass })),
        roadmapPath(rm, { open: false })));
      if (!run.replay) loadGraph().catch(() => {});
      break;
    }
    case 'concept':
      addMsg('card', h('div', { class: 'rich' }, h('div', { class: 'rich-eyebrow' }, t('Nota de concepto')), h('h3', {}, c.topic),
        h('button', { class: 'btn small', type: 'button', onclick: () => openNote(c.slug) }, t('Abrir la nota'))));
      if (!run.replay) loadGraph().catch(() => {}).then(() => reloadGuide());
      break;
    case 'daily':
      addMsg('card', h('div', { class: 'rich' },
        h('div', { class: 'rich-eyebrow' }, t('Para descubrir hoy'), c.field && h('span', { class: 'chip' }, c.field)),
        h('h3', {}, c.topic), c.hook && h('p', { class: 'hint' }, c.hook)));
      break;
    case 'question': addMsg('card', questionCard(c, run)); break;
    case 'score': {
      const tone = scoreTone(c.score);
      const verdict = tone === 'good' ? t('Lo entendiste bien') : tone === 'mid' ? t('Vas por buen camino') : t('Buen punto de partida: quedan cosas por aclarar');
      addMsg('card', h('div', { class: `rich rich-score tone-${tone}` },
        h('div', { class: 'score-ring', style: `--p:${Math.max(0, Math.min(100, c.score))}`, role: 'img', 'aria-label': t('{score} de 100', { score: c.score }) }, h('span', {}, c.score)),
        h('div', {}, h('strong', {}, `${c.label}: ${verdict}`), c.detail && h('div', { class: 'hint' }, c.detail))));
      if (!run.replay) {
        flashNarval(tone === 'good' ? 'celebrando' : tone === 'mid' ? 'feliz' : 'desanimado', 7000);
        loadGraph().catch(() => {}).then(() => reloadGuide()); // the score may unlock the next topic
      }
      break;
    }
    case 'resources':
      addMsg('card', h('details', { class: 'rich rich-resources' },
        h('summary', { class: 'rich-eyebrow' }, t('Para seguir explorando ({n})', { n: (c.resources || []).length })),
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
  const addInput = h('input', { type: 'text', maxlength: 80, placeholder: t('Añadir un concepto…'), 'aria-label': t('Añadir un concepto') });
  const add = () => {
    const name = addInput.value.trim();
    if (!name) return;
    added.push(name);
    addList.append(h('li', {}, `+ ${name}`));
    addInput.value = '';
    update();
  };
  addInput.addEventListener('keydown', (e) => { if (e.key === 'Enter') { e.preventDefault(); add(); } });
  const count = h('p', { class: 'hint' });
  const go = h('button', { class: 'btn primary', type: 'button' });
  const fresh = (p.concepts || []).filter((c) => !c.exists);
  function update() {
    const kept = fresh.filter((c) => boxes.get(c.id).checked).length + [...promote.values()].filter((cb) => cb.checked).length + added.length;
    go.textContent = kept ? tn(kept, 'Escribir la guía y {n} nota', 'Escribir la guía y {n} notas') : t('Escribir solo la guía');
    const linked = (p.concepts || []).filter((c) => c.exists && boxes.get(c.id).checked).length;
    const under = (p.pending || []).length - [...promote.values()].filter((cb) => cb.checked).length;
    count.textContent = [linked ? tn(linked, '{n} ya está en tu vault: se enlaza.', '{n} ya están en tu vault: se enlazan.') : '',
      under > 0 ? tn(under, '{n} queda bajo el agua: en gris, para explicar cuando quieras.', '{n} quedan bajo el agua: en gris, para explicar cuando quieras.') : ''].filter(Boolean).join(' ');
  }
  const item = (c, checked, map, underWater) => {
    const cb = h('input', { type: 'checkbox', checked: checked || null });
    cb.addEventListener('change', update);
    map.set(c.id, cb);
    return h('li', {}, h('label', { class: `plan-item${c.exists ? ' exists' : ''}${underWater ? ' under' : ''}` }, cb,
      h('span', {}, h('strong', {}, c.title), c.exists ? h('em', {}, t(' · ya en tu vault')) : null, c.summary ? h('small', {}, c.summary) : null)));
  };
  const levelRows = (levels, under) => levels.map((lv, i) => lv && h('li', { class: under ? 'under' : null },
    h('span', { class: 'plan-level' }, i === 0 ? t('Directo') : t('Nivel {n}', { n: i + 1 })),
    h('ul', {}, lv.map((c) => item(c, !under, under ? promote : boxes, under)))));
  const el = h('div', { class: 'rich rich-plan' },
    h('div', { class: 'rich-eyebrow' }, icon('mountain-snow'), tn(p.depth, 'Plan · escribir ahora {n} nivel', 'Plan · escribir ahora {n} niveles')),
    h('h3', {}, p.topic),
    h('p', { class: 'hint' }, t('Desmarca lo que ya sabes; marca algo de abajo si lo quieres escrito ya.')),
    h('ol', { class: 'plan-levels' }, levelRows(above, false)),
    (p.pending || []).length ? h('p', { class: 'plan-water' }, t('Bajo el agua: mapeado, sin escribir')) : null,
    (p.pending || []).length ? h('ol', { class: 'plan-levels' }, levelRows(below, true)) : null,
    addList,
    h('div', { class: 'plan-add' }, addInput, h('button', { class: 'btn small', type: 'button', onclick: add }, icon('plus'), t('Añadir'))),
    h('div', { class: 'actions' }, go,
      h('button', { class: 'btn', type: 'button', onclick: () => answer('g', t('Solo la guía')) }, t('Solo la guía')),
      h('button', { class: 'btn ghost small', type: 'button', onclick: () => answer('n', t('Cancelar')) }, t('Cancelar'))),
    count);
  const state = { el, live: false, answered: false };
  // once answered the plan folds to its title: what matters next is the guide being written
  const fold = () => {
    el.classList.add('answered');
    el.querySelector('.rich-eyebrow').after(h('button', { class: 'btn small ghost plan-toggle', type: 'button',
      onclick: (e) => { const open = el.classList.toggle('expanded'); e.currentTarget.textContent = open ? t('Ocultar el plan') : t('Ver el plan'); } }, t('Ver el plan')));
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
  const parts = [guide ? t('guía') : '', notes ? tn(notes, '{n} nota', '{n} notas') : ''].filter(Boolean);
  return t('Listo · {what}', { what: parts.join(` ${t('y')} `) });
}

// One card per guide being written, redrawn in place as each note finishes.
function progressCard(items) {
  const state = { pending: t('en cola'), working: t('escribiendo…'), done: t('lista'), linked: t('ya estaba'), error: t('falló') };
  const done = items.filter((i) => i.state !== 'pending' && i.state !== 'working').length;
  const failed = items.filter((i) => i.state === 'error').length;
  const list = h('ul', { class: 'progress-list' }, items.map((it) => h('li', { 'data-state': it.state, class: it.kind },
    h('span', { class: 'p-dot', 'aria-hidden': 'true' }),
    h('span', { class: 'p-title' }, it.title, it.kind === 'guide' ? h('small', {}, t(' · guía')) : null),
    h('small', { class: 'p-state' }, state[it.state] || it.state))));
  // once everything is written the card folds to one line; it stays open if something failed
  const body = done === items.length
    ? [h('details', { open: failed ? true : null },
      h('summary', { class: 'rich-eyebrow' }, icon(failed ? 'x' : 'check'),
        failed ? tn(failed, 'Escrito · {n} falló', 'Escrito · {n} fallaron') : writtenSummary(items)),
      list)]
    : [h('div', { class: 'rich-eyebrow' }, t('Escribiendo · {done} de {total}', { done, total: items.length })),
      h('div', { class: 'bar' }, h('i', { style: `width:${Math.round((100 * done) / items.length)}%` })),
      list];
  if (T.progress && T.progress.isConnected) { T.progress.replaceChildren(...body); return; }
  T.progress = h('div', { class: 'rich rich-progress' }, ...body);
  addMsg('card', T.progress);
}

function questionCard(c, run) {
  const card = h('div', { class: 'rich rich-question' },
    h('div', { class: 'rich-eyebrow' }, c.total ? `Pregunta ${c.n} de ${c.total}` : t('Pregunta')),
    h('p', { class: 'q-text' }, c.question));
  if (!c.choices || !c.choices.length) return card;
  const state = { answered: false, buttons: [] };
  const list = h('div', { class: 'choices', role: 'group', 'aria-label': t('Opciones') });
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
  const run = { id, done: false, replay, waiting: '', title: title || t('Chat'), es: new EventSource(`/api/runs/${id}/events?from=0`) };
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
      const shown = known ? known.label : /^\{.*"go":true/.test(e.text) ? t('Escribir con estos cambios') : e.text;
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
      if (e.kind === 'line' && T.plan && !T.plan.answered && /genero la guía/i.test(e.key || e.label || '')) {
        T.plan.live = true;
        setComposer('busy');
        $('#reply').placeholder = t('Elige en el plan de arriba.');
        T.plan.el.scrollIntoView({ behavior: smoothness(), block: 'nearest' });
        break;
      }
      if (e.kind === 'line' && T.choiceCard && !T.choiceCard.answered && /^respuesta$/i.test(e.key || e.label || '')) {
        setComposer('busy');
        $('#reply').placeholder = t('Elige una opción de la pregunta.');
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
  if (e.error) {
    addMsg('bot error', t(e.error));
    if (!run.replay) flashNarval('sorprendido', 7000);
  }
  if (!$('#transcript').children.length) $('#transcript').append(emptyState());
  setComposer('chat'); // writing again continues this chat
  if (!run.replay) { loadChats(); refresh(); }
}

/* ---------- chat history ---------- */
const when = (iso) => {
  const d = new Date(iso), now = new Date();
  return d.toDateString() === now.toDateString() ? d.toLocaleTimeString(LOCALE, { timeStyle: 'short' }) : d.toLocaleDateString(LOCALE, { day: 'numeric', month: 'short' });
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
  if (!list.length) { ul.append(h('li', { class: 'hint' }, S.chats.length ? t('Ninguna conversación coincide.') : t('Tus conversaciones aparecerán aquí.'))); return; }
  for (const r of list) {
    const node = r.slug && nodeOf(r.slug);
    ul.append(h('li', { class: `chat-item${S.run && S.run.id === r.id ? ' on' : ''}`, 'data-id': r.id, style: node ? `--lc: ${lineColor(node.group)}` : null },
      h('button', { class: 'ci-open', type: 'button', onclick: () => openChat(r) },
        h('span', { class: 'ci-title' }, r.title || t('Chat')),
        h('span', { class: 'ci-meta' }, r.status === 'running' ? t('En curso') : when(r.started), node ? ` · ${node.label}` : '')),
      r.status !== 'running' && h('button', { class: 'ci-del', type: 'button', 'aria-label': t('Eliminar «{title}»', { title: r.title }), title: t('Eliminar'), onclick: async () => {
        if (!confirm(t('¿Eliminar la conversación «{title}»? Tus notas se conservan.', { title: r.title }))) return;
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
  $('#chat-title').textContent = t('Nueva conversación');
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
  await loadStats();
}

// The first time, the app speaks the browser's language (one of those it knows) and remembers it.
async function pickLanguage() {
  if (!window.I18N || window.I18N.chosen) return;
  const known = (window.I18N.langs || []).map((l) => l.code);
  const want = (navigator.languages || [navigator.language || '']).map((l) => String(l).toLowerCase().split(/[-_]/)[0]).find((c) => known.includes(c)) || 'es';
  try {
    await api('/api/settings/language', { method: 'POST', body: { language: want } });
    if (want !== LANG) location.reload();
  } catch { /* it stays in the language it has */ }
}

async function boot() {
  pickLanguage();
  createDock();
  applyTheme();
  renderDepth();
  newChat();
  try {
    S.info = await api('/api/state');
    $('#btn-vault').title = t('Vault: {name} (tus notas y tus vaults)', { name: String(S.info.vault || '').split(/[\\/]/).filter(Boolean).pop() || 'vault' });
    $('#provider-badge').textContent = S.info.provider;
    if (S.info.warning) { $('#banner').textContent = S.info.warning; $('#banner').hidden = false; }
    checkUpdate();
    setInterval(checkUpdate, 60 * 60 * 1000);
    await refresh();
    if (S.stats && !S.stats.tutorialSeen && !/^#open\//.test(location.hash)) openTutorial(false);
    await loadChats();
    if (S.info.activeRun) {
      const me = S.chats.find((r) => r.id === S.info.activeRun);
      attachRun(S.info.activeRun, me ? me.title : t('Chat'));
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
