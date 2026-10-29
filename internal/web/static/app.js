'use strict';

/* ---------- helpers ---------- */
const $ = (sel, root = document) => root.querySelector(sel);
const $$ = (sel, root = document) => [...root.querySelectorAll(sel)];

function h(tag, attrs, ...kids) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs || {})) {
    if (v == null || v === false) continue;
    if (k === 'class') el.className = v;
    else if (k.startsWith('on')) el.addEventListener(k.slice(2), v);
    else el.setAttribute(k, v === true ? '' : v);
  }
  for (const kid of kids.flat()) {
    if (kid == null || kid === false) continue;
    el.append(kid.nodeType ? kid : document.createTextNode(String(kid)));
  }
  return el;
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
  if (!res.ok) throw new Error((data && data.error) || `Error ${res.status}`);
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

const STATUS = { new: 'Por descubrir', prepared: 'Guía lista', tested: 'Comprobado', explained: 'Explicado' };

// Each roadmap is a metro line with its own colour (--l0 … --l7). The colour comes from the slug,
// probing forward on a clash, so a roadmap keeps its colour as long as the others do not change.
const LINES = 8;
function assignLines(roadmaps) {
  const taken = new Set(), out = new Map();
  for (const slug of roadmaps.map((r) => r.slug).sort()) {
    let h32 = 2166136261;
    for (const ch of slug) h32 = Math.imul(h32 ^ ch.codePointAt(0), 16777619) >>> 0;
    let i = h32 % LINES;
    for (let tries = 0; tries < LINES && taken.has(i); tries++) i = (i + 1) % LINES;
    taken.add(i);
    out.set(slug, i);
  }
  return out;
}
// CSS colour of a roadmap's line, or the neutral grey for a topic that belongs to none
const lineColor = (slug) => `var(--${S.lines.has(slug) ? `l${S.lines.get(slug)}` : 'l-none'})`;
const roadmapTitle = (slug) => (S.roadmaps.find((r) => r.slug === slug) || {}).title || slug;
const SECTION = { 'Guía': 'La guía', 'Conceptos base': 'Conceptos base', 'Dudas': 'Dudas', 'Prueba': 'Preguntas', 'Feynman': 'Explicación', 'Conexiones': 'Conexiones' };
const TYPE = { video: 'Video', web: 'Web', book: 'Libro', paper: 'Paper', interactive: 'Interactivo' };
const cssVar = (n) => getComputedStyle(document.documentElement).getPropertyValue(n).trim();

/* ---------- markdown (+ math, wikilinks) ---------- */
DOMPurify.addHook('afterSanitizeAttributes', (node) => {
  if (node.tagName !== 'A') return;
  const href = node.getAttribute('href') || '';
  if (/^https?:\/\//i.test(href)) {
    node.setAttribute('target', '_blank');
    node.setAttribute('rel', 'noopener noreferrer');
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
  return d;
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
        startOnLoad: false, securityLevel: 'strict', theme: 'neutral',
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

/* ---------- state ---------- */
const S = {
  cy: null, graph: null, selected: null, note: null, roadmaps: [], info: null, run: null,
  lines: new Map(), view: 'home', readerSlug: null, filterRm: '',
};

/* ---------- views ---------- */
const VIEWS = ['home', 'map', 'note', 'session'];
function showView(name) {
  S.view = name;
  for (const v of VIEWS) $(`#view-${v}`).hidden = v !== name;
  // the reading page belongs to the map, so that tab stays lit
  const tab = name === 'note' ? 'map' : name;
  for (const b of $$('.tabs button')) b.setAttribute('aria-selected', String(b.dataset.view === tab));
  if (name === 'map') fitMap();
  if (name === 'session' && !S.run) showHistory();
}

// Cytoscape cannot fit a graph into a hidden container, so a new layout waits until the map is on screen.
function fitMap() {
  if (!S.cy || S.view !== 'map') return;
  S.cy.resize();
  if (S.needsFit) { S.cy.fit(undefined, 50); S.needsFit = false; }
}
$$('.tabs button').forEach((b) => b.addEventListener('click', () => {
  // leaving a finished session open would hide the history behind it
  if (b.dataset.view === 'session' && S.run && S.run.done && S.view === 'session') leaveSession();
  showView(b.dataset.view);
}));
$('.brand').addEventListener('click', (e) => { e.preventDefault(); showView('home'); });

/* ---------- start dialog ---------- */
const startDialog = $('#start-dialog');
function openStart(form = 'topic') {
  if (S.run && !S.run.done) { toast('Ya hay una sesión en curso. Termínala o cancélala primero.'); showView('session'); return; }
  pickForm(form);
  startDialog.showModal();
  const first = { daily: '#daily-field', roadmap: '#in-subject', material: '#in-files', topic: '#in-topic' }[form];
  $(first).focus();
}
function pickForm(form) {
  $$('.seg button').forEach((x) => x.setAttribute('aria-selected', String(x.dataset.form === form)));
  for (const f of ['daily', 'material', 'roadmap', 'topic']) $(`#form-${f}`).hidden = f !== form;
  if (form === 'daily') $('#form-daily-body').replaceChildren(dailyPicker());
}
// The three ways to start live in the top bar. A daily topic already drawn for today is picked up, not drawn again.
$('#btn-daily').addEventListener('click', () => {
  const d = S.daily;
  if (!d || !d.topic) return openStart('daily');
  if (d.status === 'explained') return openNote(d.slug);
  if (S.run && !S.run.done) return openStart('daily'); // explains that a session is running
  startRun({ cmd: 'daily', title: d.topic }, d.topic);
});
$('#btn-material').addEventListener('click', () => openStart('material'));
$('#btn-roadmap').addEventListener('click', () => openStart('roadmap'));
$('#link-topic').addEventListener('click', () => openStart('topic'));
$$('.seg button').forEach((b) => b.addEventListener('click', () => pickForm(b.dataset.form)));
startDialog.addEventListener('click', (e) => { if (e.target === startDialog) startDialog.close(); }); // a click on the backdrop

$('#form-topic').addEventListener('submit', (e) => {
  e.preventDefault();
  const topic = $('#in-topic').value.trim();
  if (!topic) return;
  startDialog.close();
  $('#in-topic').value = '';
  startRun({ cmd: 'study', topic, title: topic }, topic);
});
$('#form-roadmap').addEventListener('submit', (e) => {
  e.preventDefault();
  const subject = $('#in-subject').value.trim();
  if (!subject) return;
  startDialog.close();
  $('#in-subject').value = '';
  startRun({ cmd: 'roadmap', subject, title: subject }, `Línea: ${subject}`);
});
$('#form-material').addEventListener('submit', async (e) => {
  e.preventDefault();
  const files = $('#in-files').files;
  if (!files.length) return;
  const fd = new FormData();
  for (const f of files) fd.append('files', f);
  const btn = $('button[type=submit]', e.target);
  btn.disabled = true;
  try {
    const up = await api('/api/upload', { method: 'POST', body: fd });
    startDialog.close();
    $('#in-files').value = '';
    await startRun({ cmd: 'material', files: up.files, title: `Apuntes: ${files[0].name}` }, `Apuntes: ${files[0].name}`);
  } catch (err) { toast(err.message); } finally { btn.disabled = false; }
});

/* ---------- note drawer ---------- */
const drawer = $('#drawer');
function setDrawer(open) {
  drawer.classList.toggle('open', open);
  drawer.toggleAttribute('inert', !open);
  drawer.setAttribute('aria-hidden', String(!open));
  $('#scrim').hidden = !open;
  $('#btn-notes').setAttribute('aria-expanded', String(open));
  if (open) { renderDrawer(); $('#drawer-search').focus(); } else $('#btn-notes').focus({ preventScroll: true });
}
$('#btn-notes').addEventListener('click', () => setDrawer(!drawer.classList.contains('open')));
$('#drawer-close').addEventListener('click', () => setDrawer(false));
$('#scrim').addEventListener('click', () => setDrawer(false));
document.addEventListener('keydown', (e) => { if (e.key === 'Escape' && drawer.classList.contains('open') && !startDialog.open) setDrawer(false); });
$('#drawer-search').addEventListener('input', renderDrawer);

// every note, grouped by the line (roadmap) it belongs to
function renderDrawer() {
  const body = $('#drawer-body');
  body.replaceChildren();
  const nodes = (S.graph && S.graph.nodes) || [];
  const q = slugify($('#drawer-search').value);
  const shown = nodes.filter((n) => !q || slugify(n.label).includes(q) || n.id.includes(q));
  if (!shown.length) {
    body.append(h('p', { class: 'drawer-empty' }, nodes.length ? 'Ninguna nota coincide.' : 'Aún no tienes notas. Cada tema que explores quedará guardado aquí.'));
    return;
  }
  const groups = new Map();
  for (const n of [...shown].sort((a, b) => (a.order || 0) - (b.order || 0) || a.label.localeCompare(b.label))) {
    const k = n.roadmap || '';
    if (!groups.has(k)) groups.set(k, []);
    groups.get(k).push(n);
  }
  const keys = [...groups.keys()].filter(Boolean).sort((a, b) => roadmapTitle(a).localeCompare(roadmapTitle(b)));
  if (groups.has('')) keys.push('');
  for (const k of keys) {
    body.append(h('section', { class: 'dg', style: `--lc: ${lineColor(k)}` },
      h('h3', {}, k ? roadmapTitle(k) : 'Temas sueltos'),
      h('ul', {}, groups.get(k).map((n) => h('li', {},
        h('button', { class: `dn${n.id === S.readerSlug && S.view === 'note' ? ' on' : ''}`, type: 'button', onclick: () => { setDrawer(false); openNote(n.id); } },
          h('i', { class: `st st-${n.status}`, title: STATUS[n.status] || n.status }), n.label))))));
  }
}

/* ---------- daily ---------- */
async function loadDaily() {
  const d = await api('/api/daily');
  S.daily = d;
  const box = $('#daily-body');
  box.replaceChildren();
  if (d.topic) {
    const done = d.status === 'explained';
    box.append(...[
      h('h1', { class: 'daily-topic' }, d.topic),
      h('div', { class: 'daily-meta' },
        d.field && h('span', { class: 'chip' }, d.field),
        h('span', { class: 'chip status', style: `--c: var(--s-${d.status || 'new'})` }, STATUS[d.status] || STATUS.new)),
      h('div', { class: 'actions' },
        done
          ? h('button', { class: 'btn primary', type: 'button', onclick: () => openNote(d.slug) }, 'Leer mi nota')
          : h('button', { class: 'btn green', type: 'button', onclick: () => startRun({ cmd: 'daily', title: d.topic }, d.topic) }, 'Seguir con esto')),
      done && h('p', { class: 'hint' }, 'Hecho por hoy. Mañana habrá otra cosa por descubrir.'),
    ].filter(Boolean)); // append() would print a literal "false"
    return;
  }
  box.append(h('div', { class: 'daily-pick' },
    h('p', { class: 'lead' }, '¿Qué te gustaría descubrir hoy?'),
    h('p', { class: 'hint' }, 'Un tema al azar para ampliar tu cultura, sin examen ni prisa. Solo porque sí.'),
    dailyPicker()));
}

// the field chooser and the draw button, shared by the home page and the Daily dialog
function dailyPicker() {
  const OTHER = '\0other'; // cannot collide with a field name typed by the user
  const sel = h('select', { id: 'daily-field', 'aria-label': 'Campo del reto' },
    h('option', { value: '' }, 'Cualquiera de mis intereses'),
    h('option', { value: '*' }, 'Totalmente aleatorio (todos los campos)'),
    ((S.info && S.info.interests) || []).map((f) => h('option', { value: f }, f)),
    h('option', { value: OTHER }, 'Otro campo…'));
  const custom = h('input', { id: 'daily-custom', type: 'text', maxlength: 80, placeholder: 'Ej.: Arquitectura, Astronomía, Música…', 'aria-label': 'Campo del conocimiento' });
  custom.hidden = true;
  sel.addEventListener('change', () => {
    custom.hidden = sel.value !== OTHER;
    if (!custom.hidden) custom.focus();
  });
  const draw = () => {
    const field = sel.value === OTHER ? custom.value.trim() : sel.value;
    if (sel.value === OTHER && !field) { toast('Escribe el campo del conocimiento.'); custom.focus(); return; }
    if (startDialog.open) startDialog.close();
    startRun({ cmd: 'daily', field, title: 'Lo de hoy' }, 'Lo de hoy');
  };
  custom.addEventListener('keydown', (e) => { if (e.key === 'Enter') draw(); });
  return h('div', { class: 'daily-fields' }, sel, custom, h('button', { class: 'btn green', type: 'button', onclick: draw }, 'Sorpréndeme'));
}

/* ---------- roadmaps (lines) ---------- */
async function loadRoadmaps() {
  S.roadmaps = await api('/api/roadmaps');
  S.lines = assignLines(S.roadmaps);
  if (S.filterRm && !S.roadmaps.some((r) => r.slug === S.filterRm)) S.filterRm = '';
  const ul = $('#roadmaps');
  ul.replaceChildren();
  for (const r of S.roadmaps) {
    const pct = r.total ? Math.round((100 * r.done) / r.total) : 0;
    ul.append(h('li', { class: 'rm-item', style: `--lc: ${lineColor(r.slug)}` },
      h('span', { class: 'rm-bullet', 'aria-hidden': 'true' }),
      h('button', { class: 'rm-title', type: 'button', onclick: () => { S.filterRm = r.slug; renderChips(); applyFilters(); showView('map'); } }, r.title),
      h('div', { class: 'rm-foot' },
        h('div', { class: 'bar', role: 'progressbar', 'aria-valuenow': pct, 'aria-valuemin': 0, 'aria-valuemax': 100, 'aria-label': `Progreso de ${r.title}` }, h('i', { style: `width:${pct}%` })),
        h('span', {}, `${r.done} de ${r.total}`)),
      r.next
        ? h('button', { class: 'btn small primary', type: 'button', title: r.next.title, onclick: () => startRun({ cmd: 'next', roadmap: r.slug, title: r.next.title }, r.next.title) }, 'Siguiente')
        : h('span', { class: 'hint' }, r.total ? 'Completa' : '')));
  }
  renderChips();
}

// one chip per line filters the map
function renderChips() {
  const box = $('#filter-rm');
  box.replaceChildren();
  if (S.roadmaps.length < 1) return;
  const chip = (slug, label) => h('button', {
    class: `line-chip${slug ? '' : ' all'}`, type: 'button', 'aria-pressed': String(S.filterRm === slug),
    style: slug ? `--lc: ${lineColor(slug)}` : null,
    onclick: () => { S.filterRm = S.filterRm === slug ? '' : slug; renderChips(); applyFilters(); },
  }, label);
  box.append(chip('', 'Todas'), ...S.roadmaps.map((r) => chip(r.slug, r.title)));
}

// topics you started but have not finished: where to go next
function renderRecent() {
  const ul = $('#recent');
  ul.replaceChildren();
  const next = { new: 'Estudiarlo', prepared: 'Responder preguntas', tested: 'Explicarlo' };
  const nodes = ((S.graph && S.graph.nodes) || []).filter((n) => n.status === 'prepared' || n.status === 'tested');
  for (const n of nodes.slice(0, 7)) {
    ul.append(h('li', {}, h('button', { class: 'recent-item', type: 'button', onclick: () => openNote(n.id) },
      h('i', { class: `st st-${n.status}` }),
      h('span', { class: 'recent-title' }, n.label),
      h('span', { class: 'recent-meta' }, next[n.status]))));
  }
}

/* ---------- graph ---------- */
// A transit map: each roadmap is a thick coloured line, each topic a station on it.
// A station is an empty ring (to discover), half filled (guide ready), filled (checked), filled with an outer ring (explained).
function cyStyle() {
  const ink = cssVar('--ink'), bg = cssVar('--bg'), muted = cssVar('--muted');
  const lineOf = (i) => cssVar(i < 0 ? '--l-none' : `--l${i}`);
  const s = [
    { selector: 'node', style: {
      'background-color': bg, 'border-width': 4, 'border-color': lineOf(-1), shape: 'ellipse', width: 20, height: 20,
      label: 'data(label)', color: ink, 'font-size': 13, 'font-weight': 600, 'font-family': cssVar('--font'),
      'text-valign': 'bottom', 'text-margin-y': 8, 'text-wrap': 'wrap', 'text-max-width': 130,
      'text-background-color': bg, 'text-background-opacity': .9, 'text-background-padding': 2,
      'z-index-compare': 'manual', 'z-index': 10, 'overlay-opacity': 0,
      'transition-property': 'opacity', 'transition-duration': '150ms' } },
    { selector: 'edge', style: {
      width: 7, 'line-color': lineOf(-1), 'curve-style': 'taxi', 'taxi-direction': 'horizontal', 'taxi-radius': 26,
      'line-cap': 'round', 'z-index-compare': 'manual', 'z-index': 1, 'overlay-opacity': 0 } },
    { selector: 'edge[kind="related"]', style: { 'curve-style': 'bezier', 'line-style': 'dashed', width: 2, opacity: .45, 'line-color': muted } },
    { selector: 'node.grouplabel', style: {
      width: 1, height: 1, 'background-opacity': 0, 'border-width': 0, label: 'data(label)', 'font-size': 16, 'font-weight': 700,
      color: ink, 'text-halign': 'right', 'text-valign': 'center', 'text-margin-x': 6, 'text-wrap': 'none', 'text-background-opacity': 0, events: 'no' } },
  ];
  for (let i = -1; i < LINES; i++) {
    const c = lineOf(i);
    s.push(
      { selector: `node[line = ${i}]`, style: { 'border-color': c } },
      { selector: `node[line = ${i}][status = "prepared"]`, style: { 'pie-size': '100%', 'pie-1-background-color': c, 'pie-1-background-size': 50, 'pie-1-background-opacity': 1 } },
      { selector: `node[line = ${i}][status = "tested"]`, style: { 'background-color': c } },
      { selector: `node[line = ${i}][status = "explained"]`, style: { 'background-color': c, 'outline-width': 3, 'outline-color': c, 'outline-offset': 3, 'outline-opacity': 1 } },
      { selector: `edge[kind = "requires"][line = ${i}]`, style: { 'line-color': c } },
      { selector: `node.grouplabel[line = ${i}]`, style: { color: c } },
    );
  }
  s.push(
    { selector: 'node:selected', style: { 'outline-width': 3, 'outline-color': ink, 'outline-offset': 4, 'outline-opacity': 1 } },
    { selector: 'edge.hl', style: { width: 9 } },
    { selector: '.dim', style: { opacity: .12 } },
  );
  return s;
}

const GX = 200, GY = 72; // horizontal gap between layers, vertical gap between topics

// Layered layout: each roadmap is a block, prerequisites flow left to right, and
// topics that belong to no roadmap sit in a grid at the bottom.
function layeredLayout(g) {
  const pos = {}, labels = [];
  const groups = new Map();
  for (const n of [...g.nodes].sort((a, b) => (a.order || 0) - (b.order || 0))) { // roadmap order breaks ties
    const k = n.roadmap || '';
    if (!groups.has(k)) groups.set(k, []);
    groups.get(k).push(n);
  }
  const keys = [...groups.keys()].filter(Boolean).sort((a, b) => groups.get(b).length - groups.get(a).length);
  if (groups.has('')) keys.push('');
  const prereqs = new Map();
  for (const e of g.edges) if (e.kind === 'requires') {
    if (!prereqs.has(e.to)) prereqs.set(e.to, []);
    prereqs.get(e.to).push(e.from);
  }
  let y0 = 0;
  for (const key of keys) {
    const nodes = groups.get(key);
    labels.push({ id: `g:${key}`, label: key ? roadmapTitle(key) : 'Temas sueltos', roadmap: key, x: 0, y: y0 - 46 });
    if (!key) {
      nodes.forEach((n, i) => { pos[n.id] = { x: (i % 5) * GX, y: y0 + Math.floor(i / 5) * GY }; });
      y0 += Math.ceil(nodes.length / 5) * GY + 120;
      continue;
    }
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
    nodes.forEach((n) => { (layers[layerOf.get(n.id)] = layers[layerOf.get(n.id)] || []).push(n); });
    const row = new Map();
    const bary = (n) => {
      const ps = (prereqs.get(n.id) || []).filter((p) => row.has(p));
      return ps.length ? ps.reduce((s, p) => s + row.get(p), 0) / ps.length : Infinity;
    };
    layers.forEach((l, li) => {
      l.sort((a, b) => (li ? bary(a) - bary(b) : 0) || 0); // one barycenter pass to cut crossings
      l.forEach((n, i) => row.set(n.id, i));
    });
    const tallest = Math.max(...layers.map((l) => l.length));
    layers.forEach((l, li) => l.forEach((n, i) => {
      pos[n.id] = { x: li * GX, y: y0 + ((tallest - l.length) * GY) / 2 + i * GY };
    }));
    y0 += tallest * GY + 130;
  }
  return { pos, labels };
}

function graphSignature(g) {
  return g.nodes.map((n) => n.id).sort().join('|') + '#' + g.edges.map((e) => `${e.from}>${e.to}`).sort().join('|');
}

async function loadGraph() {
  const g = await api('/api/graph');
  const same = S.graph && graphSignature(S.graph) === graphSignature(g);
  S.graph = g;
  $('#map-empty').hidden = g.nodes.length > 0;
  const lineOfRm = (slug) => (S.lines.has(slug) ? S.lines.get(slug) : -1);
  const lineOfNode = new Map(g.nodes.map((n) => [n.id, lineOfRm(n.roadmap || '')]));
  if (!S.cy) {
    S.cy = cytoscape({ container: $('#cy'), elements: [], style: cyStyle(), minZoom: .15, maxZoom: 3 });
    S.cy.on('tap', 'node', (ev) => selectNode(ev.target.id()));
    S.cy.on('tap', (ev) => { if (ev.target === S.cy) closePanel(); });
  }
  if (same) {
    for (const n of g.nodes) {
      const el = S.cy.getElementById(n.id);
      el.data({ status: n.status, mastery: n.mastery, label: n.label, line: lineOfNode.get(n.id) });
    }
    S.cy.edges().forEach((e) => e.data('line', lineOfNode.get(e.target().id())));
    S.cy.nodes('.grouplabel').forEach((l) => l.data('line', lineOfRm(l.data('roadmap'))));
  } else {
    S.cy.elements().remove();
    const { pos, labels } = layeredLayout(g);
    S.cy.add([
      ...g.nodes.map((n) => ({ group: 'nodes', data: { id: n.id, label: n.label, status: n.status, mastery: n.mastery, roadmap: n.roadmap || '', line: lineOfNode.get(n.id) }, position: pos[n.id] })),
      ...labels.map((l) => ({ group: 'nodes', data: { id: l.id, label: l.label, roadmap: l.roadmap, line: lineOfRm(l.roadmap) }, position: { x: l.x, y: l.y }, classes: 'grouplabel', selectable: false, grabbable: false })),
      ...g.edges.map((e, i) => ({ group: 'edges', data: { id: `e${i}`, source: e.from, target: e.to, kind: e.kind, line: lineOfNode.get(e.to) } })),
    ]);
    S.needsFit = g.nodes.length > 0;
    fitMap();
  }
  applyFilters();
}

function applyFilters() {
  if (!S.cy) return;
  const q = slugify($('#search').value);
  const rm = S.filterRm;
  S.cy.batch(() => {
    S.cy.nodes().not('.grouplabel').forEach((n) => {
      const okQ = !q || slugify(n.data('label')).includes(q) || n.id().includes(q);
      const okR = !rm || n.data('roadmap') === rm;
      n.toggleClass('dim', !(okQ && okR));
    });
    S.cy.edges().forEach((e) => e.toggleClass('dim', e.source().hasClass('dim') || e.target().hasClass('dim')));
  });
}
$('#search').addEventListener('input', applyFilters);
$('#search').addEventListener('keydown', (e) => {
  if (e.key !== 'Enter' || !S.cy) return;
  const hit = S.cy.nodes().not('.grouplabel').filter((n) => !n.hasClass('dim'))[0];
  if (hit) { selectNode(hit.id(), true); }
});

/* ---------- station card (map) ---------- */
function closePanel() {
  $('#note-panel').hidden = true;
  S.selected = null;
  if (S.cy) { S.cy.elements().unselect(); S.cy.edges().removeClass('hl'); }
}

// Selects a station on the map and shows its card.
async function selectNode(slug, center = false, quiet = false) {
  if (!quiet) showView('map');
  try {
    const n = await api(`/api/notes/${encodeURIComponent(slug)}`);
    S.selected = slug;
    S.note = n;
    renderPanel(n);
    if (S.cy) {
      S.cy.elements().unselect();
      S.cy.edges().removeClass('hl');
      const el = S.cy.getElementById(slug);
      if (el.nonempty()) { el.select(); el.connectedEdges().addClass('hl'); if (center) S.cy.animate({ center: { eles: el }, zoom: Math.max(S.cy.zoom(), 1) }, { duration: 250 }); }
    }
  } catch (err) { toast(err.message); }
}

// Opens a note as a reading page.
// With quiet, the page is redrawn in place (after a session changed the note) instead of scrolled to the top.
async function openNote(slug, quiet = false) {
  try {
    const n = await api(`/api/notes/${encodeURIComponent(slug)}`);
    const top = $('#view-note').scrollTop;
    S.note = n;
    S.readerSlug = slug;
    renderReader(n);
    showView('note');
    $('#view-note').scrollTop = quiet ? top : 0;
  } catch (err) { toast(err.message); }
}

function stripSection(body, heading) {
  const re = new RegExp(`(^|\\n)## ${heading}\\n[\\s\\S]*?(?=\\n## |$)`);
  return body.replace(re, '\n');
}

function openSim(slug, title) {
  $('#sim-title').textContent = title || 'Simulación';
  $('#sim-frame').src = `/interactive/${encodeURIComponent(slug)}.html`;
  $('#sim-dialog').showModal();
}
$('#sim-close').addEventListener('click', () => $('#sim-dialog').close());
$('#sim-dialog').addEventListener('close', () => { $('#sim-frame').src = 'about:blank'; });

// How a note is studied, in order: study it, answer questions, explain it. Shared by the station card
// and the reading page. The step to do next is highlighted; finished steps can be done again.
// Extending into base concepts is not a step: it is an optional extra for anyone who needs more background.
const STEP_RANK = { new: 0, prepared: 1, tested: 2, explained: 3 };
function noteControls(n, title, status, compact = false) {
  const rank = STEP_RANK[status] ?? 0;
  const nInput = h('input', { type: 'number', min: 1, max: 30, value: 5, 'aria-label': 'Número de preguntas' });
  const diff = h('select', { 'aria-label': 'Dificultad' },
    ['básica', 'media', 'avanzada'].map((d) => h('option', { value: d, selected: d === 'media' }, d)));
  const steps = [
    { label: 'Estudiar', done: rank >= 1, enabled: true, run: () => {
      if (rank >= 1 && !confirm('¿Volver a escribir la guía? Reemplaza la que tienes.')) return;
      startRun(rank >= 1 ? { cmd: 'prepare', topic: title, title } : { cmd: 'study', topic: title, title }, title);
    } },
    { label: 'Responder preguntas', done: rank >= 2, enabled: rank >= 1,
      run: () => startRun({ cmd: 'quiz', slug: n.slug, n: Number(nInput.value) || 5, difficulty: diff.value, title }, `Preguntas: ${title}`) },
    { label: 'Explicar', done: rank >= 3, enabled: rank >= 1, run: () => startRun({ cmd: 'feynman', slug: n.slug, title }, `Explicar: ${title}`) },
  ];
  const current = steps.findIndex((s) => !s.done);
  const list = h('ol', { class: `sequence${compact ? ' compact' : ''}`, 'aria-label': 'Orden para estudiar este tema' }, steps.map((s, i) => {
    const state = s.done ? 'done' : i === current ? 'current' : 'todo';
    return h('li', {}, h('button', {
      class: `step${state === 'current' ? ' go' : ''}`, type: 'button', 'data-state': state, disabled: !s.enabled,
      'aria-current': state === 'current' ? 'step' : null,
      title: s.done ? 'Ya lo hiciste. Pulsa para repetirlo.' : null, onclick: s.run,
    }, h('span', { class: 'step-n', 'aria-hidden': 'true' }, s.done ? '✓' : String(i + 1)), s.label));
  }));

  // Extending and doubts work on the article itself, so only the reading page has them
  const extras = [];
  if (rank >= 1 && !compact) {
    const has = /^## Conceptos base\s*$/m.test(n.body || '');
    extras.push(
      h('button', {
        class: 'btn small yellow', type: 'button',
        title: 'Explica desde cero lo que la guía da por sabido, y lo añade al artículo. Es opcional.',
        onclick: (e) => extendInline(n, e.currentTarget),
      }, has ? 'Rehacer los conceptos base' : 'Extender: conceptos base'),
      h('button', {
        class: 'btn small blue', type: 'button',
        title: 'Cuéntame qué no entendiste y lo hablamos. La respuesta queda en el artículo.',
        onclick: focusDoubts,
      }, 'Tengo una duda'));
  }
  const sim = n.hasSimulation
    ? h('button', { class: 'btn small', type: 'button', onclick: () => openSim(n.slug, title) }, 'Abrir simulación')
    : rank >= 1 && h('button', { class: 'btn small', type: 'button', onclick: () => startRun({ cmd: 'interactive', slug: n.slug, title }, `Simulación: ${title}`) }, 'Generar simulación');
  if (sim) extras.push(sim);

  return h('div', { class: 'controls' },
    list,
    rank >= 1 ? h('div', { class: 'quiz-opts' }, 'Preguntas:', nInput, 'en total, dificultad', diff) : null,
    extras.length ? h('div', { class: 'extras' }, extras) : null);
}

const statusChip = (status) => h('span', { class: 'chip status', style: `--c: var(--s-${status})` }, STATUS[status] || status);

function renderPanel(n) {
  const p = $('#note-panel');
  const m = n.meta;
  const status = m.status || 'new';
  const title = m.title || n.slug;
  p.replaceChildren();
  p.hidden = false;
  p.style.setProperty('--lc', lineColor(m.roadmap || ''));
  p.append(...[
    h('div', { class: 'station-line' }),
    h('div', { class: 'station-head' }, h('h3', {}, title),
      h('button', { class: 'btn small', type: 'button', 'aria-label': 'Cerrar', onclick: closePanel }, '✕')),
    h('div', { class: 'chips' },
      statusChip(status),
      m.roadmap && h('span', { class: 'chip' }, roadmapTitle(m.roadmap)),
      m.field && h('span', { class: 'chip' }, m.field)),
    m.mastery ? h('div', { class: 'mastery' },
      h('div', { class: 'row' }, h('span', {}, 'Lo que llevas'), h('span', {}, `${m.mastery}/100`)),
      h('div', { class: 'bar', style: `--lc: ${lineColor(m.roadmap || '')}` }, h('i', { style: `width:${m.mastery}%` }))) : null,
    status !== 'new' && h('div', { class: 'actions' }, h('button', { class: 'btn small blue', type: 'button', onclick: () => openNote(n.slug) }, 'Leer la guía')),
    noteControls(n, title, status, true),
  ].filter(Boolean)); // native append() would print a literal "false"
  p.scrollTop = 0;
}

// Drops emojis from a guide. Fenced code is kept as it is.
function stripEmoji(md) {
  return md.split(/(```[\s\S]*?```)/g).map((part, i) => (i % 2 ? part : part
    .replace(/[\p{Extended_Pictographic}\u{FE0F}\u{200D}\u{20E3}]/gu, '')
    .replace(/^([ \t]*(?:#+|>|[-*]|\d+\.)) {2,}(?=\S)/gm, '$1 ') // a heading or list item that began with an emoji keeps a single space
    .replace(/[ \t]+$/gm, ''))).join('');
}

/* ---------- base concepts and doubts, inside the article ---------- */
// Where a section of the article ends: the heading that follows it, null when it runs to the end,
// undefined when the article has no such section.
function sectionEnd(md, heading) {
  const h2 = $$('h2', md).find((x) => x.textContent.trim() === heading);
  if (!h2) return undefined;
  let el = h2.nextElementSibling;
  while (el && el.tagName !== 'H2') el = el.nextElementSibling;
  return el;
}
const insertBefore = (md, node, before) => (before ? md.insertBefore(node, before) : md.append(node));
// A block that says the AI is working, with a button to stop it. Stopping aborts the request, and the server then stops the model.
const thinkingBlock = (text, onCancel) => h('div', { class: 'thinking', role: 'status' },
  h('span', { class: 'pulse', 'aria-hidden': 'true' }), text,
  h('button', { class: 'btn small red', type: 'button', onclick: onCancel }, 'Cancelar'));
const wasCancelled = (err) => err && err.name === 'AbortError';
const smoothness = () => (matchMedia('(prefers-reduced-motion: reduce)').matches ? 'auto' : 'smooth');

// The "Dudas" section always shows in an article that has a guide: its saved questions and, at the end, the box to ask another.
function placeDoubts(md, n) {
  let before = sectionEnd(md, 'Dudas');
  if (before === undefined) {
    // no doubts yet: the section goes after the base concepts, or else after the guide
    before = sectionEnd(md, 'Conceptos base');
    if (before === undefined) before = sectionEnd(md, 'La guía');
    if (before === undefined) before = null;
    insertBefore(md, h('h2', {}, 'Dudas'), before);
  }
  insertBefore(md, doubtsBox(n), before);
}

function doubtsBox(n) {
  const ta = h('textarea', {
    rows: 3, maxlength: 1500, 'aria-label': 'Tu duda',
    placeholder: 'Cuéntame qué entiendes y qué no. Por ejemplo: entiendo qué es una probabilidad, pero no por qué se multiplican.',
  });
  const send = h('button', { class: 'btn blue', type: 'submit' }, 'Preguntar');
  const form = h('form', { class: 'doubt-form' }, ta,
    h('div', { class: 'doubt-row' }, h('span', { class: 'hint' }, 'Pregunta como en un chat. Tu duda y la respuesta se guardan aquí. Ctrl + Enter para enviar.'), send));
  const box = h('div', { class: 'doubts' }, form);
  form.addEventListener('submit', (e) => { e.preventDefault(); askDoubt(n, box, ta, send); });
  ta.addEventListener('keydown', (e) => {
    if (e.key === 'Enter' && (e.ctrlKey || e.metaKey) && !e.isComposing) { e.preventDefault(); form.requestSubmit(); }
  });
  return box;
}

function focusDoubts() {
  const ta = $('#reader .doubts textarea');
  if (!ta) return;
  ta.scrollIntoView({ behavior: smoothness(), block: 'center' });
  ta.focus({ preventScroll: true });
}

// Sends a doubt and puts the question and its answer into the article, right above the box.
async function askDoubt(n, box, ta, send) {
  const question = ta.value.trim();
  if (!question) { toast('Escribe tu duda.'); ta.focus(); return; }
  ta.disabled = send.disabled = true;
  const bubble = h('div', { class: 'chat-q' }, h('small', {}, 'Tú'), question);
  const stop = new AbortController();
  const thinking = thinkingBlock('Pensando…', () => stop.abort());
  box.before(bubble, thinking);
  thinking.scrollIntoView({ behavior: smoothness(), block: 'nearest' });
  try {
    const ans = await api(`/api/notes/${encodeURIComponent(n.slug)}/doubt`, { method: 'POST', body: { question }, signal: stop.signal });
    const entry = mdBlock(stripEmoji(ans.entry));
    bubble.replaceWith(entry);
    thinking.remove();
    ta.value = '';
    offerConcepts(n, entry, ans.concepts || []);
  } catch (err) {
    bubble.remove();
    thinking.remove();
    toast(wasCancelled(err) ? 'Cancelado.' : err.message); // what they wrote stays in the box
  } finally {
    ta.disabled = send.disabled = false;
    ta.focus({ preventScroll: true });
  }
}

// When an answer explained something that could stand on its own, offer to add it to the vault as an article.
function offerConcepts(n, after, concepts) {
  let anchor = after;
  for (const c of concepts) {
    const row = h('div', { class: 'offer' });
    const draw = (inVault) => row.replaceChildren(
      h('span', {}, inVault ? `«${c.title}» ya está en tu vault.` : `Aquí se explicó «${c.title}». ¿Lo añado a tu vault como artículo?`),
      inVault
        ? h('button', { class: 'btn small', type: 'button', onclick: () => openNote(c.slug) }, 'Abrir artículo')
        : h('button', { class: 'btn small green', type: 'button', onclick: add }, 'Añadir al vault'));
    async function add(e) {
      const btn = e.currentTarget;
      btn.disabled = true;
      try {
        await api(`/api/notes/${encodeURIComponent(n.slug)}/concept`, { method: 'POST', body: { title: c.title, summary: c.summary } });
        draw(true);
        toast(`«${c.title}» está en tu vault. Estúdialo cuando quieras desde el mapa o el cajón de notas.`);
        loadGraph().then(renderRecent).catch(() => {}); // the map and the drawer learn about the new topic
      } catch (err) { btn.disabled = false; toast(err.message); }
    }
    draw(c.exists);
    anchor.after(row);
    anchor = row;
  }
}

// Writes the base concepts and puts them in the article. Where they will appear shows a "thinking" block meanwhile.
async function extendInline(n, btn) {
  btn.disabled = true;
  const md = $('#reader .r-article > .md');
  const stop = new AbortController();
  const thinking = thinkingBlock('Escribiendo los conceptos base…', () => stop.abort());
  if (md) {
    let before = sectionEnd(md, 'Conceptos base'); // when redoing them, at the end of the old ones
    if (before === undefined) before = sectionEnd(md, 'La guía');
    insertBefore(md, thinking, before === undefined ? null : before);
    thinking.scrollIntoView({ behavior: smoothness(), block: 'center' });
  }
  try {
    await api(`/api/notes/${encodeURIComponent(n.slug)}/extend`, { method: 'POST', body: {}, signal: stop.signal });
    await openNote(n.slug, true); // draws the article again, with its index
    const heading = $$('#reader .r-article h2').find((x) => x.textContent === 'Conceptos base');
    if (heading) heading.scrollIntoView({ behavior: smoothness(), block: 'start' });
  } catch (err) {
    thinking.remove();
    btn.disabled = false;
    toast(wasCancelled(err) ? 'Cancelado.' : err.message);
  }
}

// The reading page: the guide as an article. The note is a line and each section a station along it.
function renderReader(n) {
  const root = $('#reader');
  const m = n.meta;
  const status = m.status || 'new';
  const title = m.title || n.slug;
  root.replaceChildren();

  const wrap = h('div', { class: 'reader-wrap', style: `--line-c: ${lineColor(m.roadmap || '')}` });
  wrap.append(h('button', { class: 'crumb', type: 'button', onclick: () => { showView('map'); selectNode(n.slug, true); } }, '← Ver en el mapa'));
  wrap.append(h('header', { class: 'r-head' },
    h('p', { class: 'r-line' }, m.roadmap ? roadmapTitle(m.roadmap) : 'Tema suelto'),
    h('h1', {}, title),
    h('div', { class: 'chips' }, statusChip(status), m.field && h('span', { class: 'chip' }, m.field), m.source && m.source !== 'adhoc' && h('span', { class: 'chip' }, m.source)),
    m.mastery ? h('div', { class: 'mastery' },
      h('div', { class: 'row' }, h('span', {}, 'Lo que llevas'), h('span', {}, `${m.mastery}/100`)),
      h('div', { class: 'bar', style: 'background: var(--surface-2)' }, h('i', { style: `width:${m.mastery}%; background: var(--line-c)` }))) : null,
    noteControls(n, title, status)));

  const article = h('article', { class: 'r-article' });
  const toc = [];
  const addToc = (el, text, sub) => {
    el.id = `sec-${toc.length}`;
    toc.push({ id: el.id, text, sub });
  };

  const bodySrc = stripEmoji(stripSection(n.body, 'Recursos')).trim();
  if (bodySrc) {
    const md = mdBlock(bodySrc);
    for (const hd of $$('h2', md)) hd.textContent = SECTION[hd.textContent.trim()] || hd.textContent;
    if (status !== 'new') placeDoubts(md, n);
    let inDoubts = false; // each doubt is a heading of its own; the index lists the section, not every question
    for (const hd of $$('h2, h3', md)) {
      if (hd.tagName === 'H2') inDoubts = hd.textContent === 'Dudas';
      else if (inDoubts) continue;
      addToc(hd, hd.textContent, hd.tagName === 'H3');
    }
    article.append(md);
  } else {
    article.append(h('p', { class: 'r-empty' }, 'Esta nota aún no tiene guía. Empieza por «Explorarlo» y la prepararé para ti.'));
  }

  const res = m.resources || [];
  if (res.length) {
    const sec = h('section', { class: 'r-resources' }, h('h2', {}, 'Para seguir explorando'),
      h('ul', { class: 'res-list' }, res.map((r) => resourceCard(r, { slug: n.slug, title }))));
    addToc(sec.firstChild, 'Para seguir explorando', false);
    article.append(sec);
  }

  // neighbouring stations: what comes before and after on the line, and who links here
  const nodes = (S.graph && S.graph.nodes) || [];
  const titleOf = (id) => (nodes.find((x) => x.id === id) || {}).label || id;
  const after = ((S.graph && S.graph.edges) || []).filter((e) => e.kind === 'requires' && e.from === n.slug).map((e) => ({ slug: e.to, title: titleOf(e.to) }));
  const before = n.requires || [];
  const back = n.backlinks || [];
  if (before.length || after.length || back.length) {
    const nb = (label, l) => h('button', { class: 'neighbor', type: 'button', onclick: () => openNote(l.slug) }, h('small', {}, label), h('strong', {}, l.title));
    const sec = h('section', { class: 'r-next' }, h('h2', {}, 'En la línea'),
      before.map((l) => nb('Antes', l)),
      after.map((l) => nb('Después', l)),
      back.length ? h('div', { class: 'r-backlinks' }, h('h4', {}, 'Enlazan aquí'), h('div', { class: 'links' }, back.map((l) => h('button', { type: 'button', onclick: () => openNote(l.slug) }, l.title)))) : null);
    sec.querySelectorAll('h4').forEach((x) => { x.style.margin = '8px 0 6px'; });
    addToc(sec.firstChild, 'En la línea', false);
    article.append(sec);
  }

  const nav = h('nav', { class: 'toc', 'aria-label': 'En esta página' }, h('h3', {}, 'En esta página'),
    h('ol', {}, toc.map((t) => h('li', { class: t.sub ? 'sub' : null }, h('a', { href: `#${t.id}`, onclick: (e) => {
      e.preventDefault();
      document.getElementById(t.id).scrollIntoView({ behavior: matchMedia('(prefers-reduced-motion: reduce)').matches ? 'auto' : 'smooth', block: 'start' });
    } }, t.text)))));
  wrap.append(h('div', { class: 'r-body' }, toc.length > 1 ? nav : h('div'), article));
  root.append(wrap);
  watchToc(wrap);
}

// highlights the table-of-contents entry of the section being read
let tocObserver;
function watchToc(wrap) {
  tocObserver?.disconnect();
  const links = new Map($$('.toc a', wrap).map((a) => [a.getAttribute('href').slice(1), a]));
  if (!links.size || !('IntersectionObserver' in window)) return;
  tocObserver = new IntersectionObserver((entries) => {
    for (const e of entries) if (e.isIntersecting) {
      links.forEach((a) => a.classList.remove('on'));
      links.get(e.target.id)?.classList.add('on');
    }
  }, { root: $('#view-note'), rootMargin: '0px 0px -70% 0px' });
  for (const id of links.keys()) tocObserver.observe(document.getElementById(id));
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

// One recommended resource as a card. ctx.slug/title identify the note it belongs to (for simulations).
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
  if (local) {
    const slug = ctx.slug || local[1];
    return h('li', { class: 'res' }, head(h('button', { class: 'link', type: 'button', onclick: () => openSim(slug, ctx.title) }, r.title)), why);
  }
  if (r.type === 'interactive') {
    return h('li', { class: 'res' }, head(h('strong', {}, `${r.title} (propuesta)`)), why,
      ctx.slug && h('div', {}, h('button', { class: 'btn small', type: 'button', onclick: () => startRun({ cmd: 'interactive', slug: ctx.slug, brief: r.why || '' }, `Simulación: ${ctx.title}`) }, 'Generar')));
  }
  return h('li', { class: 'res' }, head(h('strong', {}, r.title)), why);
}

// wikilinks and simulation links rendered anywhere in the app
document.addEventListener('click', (e) => {
  const a = e.target.closest('a[href^="#note/"], a[href^="#sim/"]');
  if (!a) return;
  e.preventDefault();
  const href = a.getAttribute('href');
  if (href.startsWith('#note/')) {
    const slug = decodeURIComponent(href.slice(6));
    if (S.graph && !S.graph.nodes.some((n) => n.id === slug)) {
      // a link to a topic that does not exist yet is a gap in the map: offer to fill it
      const title = a.textContent.trim() || slug;
      if (confirm(`«${title}» aún no está en tu mapa. ¿Quieres estudiarlo ahora?`)) startRun({ cmd: 'study', topic: title, title }, title);
      return;
    }
    openNote(slug);
  } else openSim(decodeURIComponent(href.slice(5)), S.note && S.note.meta.title);
});

/* ---------- sessions ---------- */
const T = { raw: '', bubble: null, timer: null, auto: true };

function resetTranscript() {
  $('#transcript').replaceChildren();
  T.raw = ''; T.bubble = null; T.choiceCard = null;
  $('#stepper').hidden = true;
}

function scrollDown() {
  const t = $('#transcript');
  if (T.auto) t.scrollTop = t.scrollHeight;
}
$('#transcript').addEventListener('scroll', () => {
  const t = $('#transcript');
  T.auto = t.scrollHeight - t.scrollTop - t.clientHeight < 80;
});

function botText(text) {
  T.raw += text;
  if (!T.bubble) {
    T.bubble = h('div', { class: 'msg bot' });
    $('#transcript').append(T.bubble);
  }
  clearTimeout(T.timer);
  T.timer = setTimeout(flushBot, 60);
}

function flushBot() {
  clearTimeout(T.timer);
  if (!T.bubble) return;
  T.bubble.replaceChildren(mdBlock(T.raw, { breaks: true }));
  scrollDown();
}

function closeBubble() { flushBot(); T.raw = ''; T.bubble = null; }

function addMsg(cls, content) {
  closeBubble();
  const m = h('div', { class: `msg ${cls}` });
  if (typeof content === 'string') m.textContent = content; else m.append(content);
  $('#transcript').append(m);
  scrollDown();
  return m;
}

function setWorking(on, text) {
  $('#working').hidden = !on;
  if (text) $('#working-text').textContent = text;
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

function setAsk(e) {
  closeBubble();
  setWorking(false);
  const block = e.kind === 'block';
  const form = $('#composer');
  const ta = $('#reply');
  const label = (e.label || '').replace(/\s*\(s\/n\)\s*$/i, '').trim();

  // a multiple-choice question is answered with the buttons of its own card
  if (T.choiceCard && !T.choiceCard.answered && /^respuesta$/i.test(label)) {
    form.hidden = true;
    const first = T.choiceCard.buttons[0];
    if (first) first.focus({ preventScroll: true });
    scrollDown();
    return;
  }

  const opts = quickOptions(e);
  form.hidden = false;
  form.dataset.kind = e.kind;
  $('#composer-label').textContent = label;
  $('#composer-label').hidden = !label;
  $('#quick').replaceChildren(...(opts || []).map((o) => h('button', {
    type: 'button', class: `btn${o.primary ? ' primary' : ''}`, onclick: () => { T.echo = { reply: o.reply, label: o.label }; sendReply(o.reply, false); },
  }, o.label)));
  $('#quick').hidden = !opts;
  $('#freeform').hidden = !!opts;
  ta.value = '';
  ta.rows = block ? 7 : 1;
  ta.placeholder = block ? 'Explícalo con tus palabras, como si el otro no supiera nada…' : (e.def ? `Enter para usar «${e.def}»` : 'Escribe tu respuesta');
  $('#composer-hint').textContent = block ? 'Ctrl + Enter para enviar' : '';
  $('#btn-finish').hidden = !block;
  $('#btn-send').disabled = false;
  if (opts) $('#quick .primary').focus({ preventScroll: true }); else ta.focus();
  scrollDown();
}

/* ---------- cards: structured output drawn as elements ---------- */
const PHASES = ['prepare', 'quiz', 'feynman'];

function setPhase(phase) {
  const at = PHASES.indexOf(phase);
  if (at < 0) return;
  $('#stepper').hidden = false;
  $$('#stepper li').forEach((li, i) => {
    li.dataset.state = i < at ? 'done' : i === at ? 'current' : 'todo';
    if (i === at) li.setAttribute('aria-current', 'step'); else li.removeAttribute('aria-current');
  });
}

// a low score is a starting point, not a failure: it gets a neutral tone, never a red one
function scoreTone(score) { return score >= 70 ? 'good' : score >= 40 ? 'mid' : 'low'; }

function renderCard(c) {
  switch (c.kind) {
    case 'phase': setPhase(c.phase); break;
    case 'daily':
      addMsg('card', h('div', { class: 'rich rich-daily' },
        h('div', { class: 'rich-eyebrow' }, 'Para descubrir hoy', c.field && h('span', { class: 'chip' }, c.field)),
        h('h3', {}, c.topic),
        c.hook && h('p', {}, c.hook)));
      break;
    case 'question': addMsg('card', questionCard(c)); break;
    case 'score': {
      const tone = scoreTone(c.score);
      const verdict = tone === 'good' ? 'Lo entendiste bien' : tone === 'mid' ? 'Vas por buen camino' : 'Buen punto de partida: quedan cosas por aclarar';
      addMsg('card', h('div', { class: `rich rich-score tone-${tone}` },
        h('div', { class: 'score-ring', style: `--p:${Math.max(0, Math.min(100, c.score))}`, role: 'img', 'aria-label': `${c.score} de 100` }, h('span', {}, c.score)),
        h('div', {}, h('strong', {}, `${c.label}: ${verdict}`), c.detail && h('div', { class: 'hint' }, c.detail))));
      break;
    }
    case 'resources':
      addMsg('card', h('div', { class: 'rich rich-resources' },
        h('div', { class: 'rich-eyebrow' }, 'Para seguir explorando'),
        h('ul', { class: 'res-list' }, (c.resources || []).map((r) => resourceCard(r)))));
      break;
  }
}

function questionCard(c) {
  const card = h('div', { class: 'rich rich-question' },
    h('div', { class: 'rich-eyebrow' }, c.total ? `Pregunta ${c.n} de ${c.total}` : 'Pregunta'),
    h('p', { class: 'q-text' }, c.question));
  if (!c.choices || !c.choices.length) return card;
  const state = { answered: false, buttons: [] };
  const list = h('div', { class: 'choices', role: 'group', 'aria-label': 'Opciones' });
  for (const choice of c.choices) {
    const b = h('button', { type: 'button', class: 'choice', onclick: () => {
      if (state.answered || !S.run || S.run.done) return;
      state.answered = true;
      b.classList.add('picked');
      state.buttons.forEach((x) => { x.disabled = true; });
      sendReply(choice, false);
    } }, choice);
    state.buttons.push(b);
    list.append(b);
  }
  card.append(list);
  T.choiceCard = state;
  return card;
}

// once the learner has answered, or the run is over, an earlier question's buttons are no longer live
function closeChoices() {
  if (!T.choiceCard) return;
  T.choiceCard.answered = true;
  T.choiceCard.buttons.forEach((x) => { x.disabled = true; });
}

async function sendReply(text, finish = false) {
  if (!S.run || S.run.done) return;
  $('#composer').hidden = true;
  setWorking(true, 'Pensando…');
  try {
    await api(`/api/runs/${S.run.id}/input`, { method: 'POST', body: { text, finish } });
  } catch (err) {
    toast(err.message);
    $('#composer').hidden = false;
    setWorking(false);
  }
}

$('#composer').addEventListener('submit', (e) => {
  e.preventDefault();
  const ta = $('#reply');
  const block = $('#composer').dataset.kind === 'block';
  if (block && !ta.value.trim()) { toast('Escribe tu explicación antes de enviar.'); return; }
  sendReply(ta.value, false);
});
$('#reply').addEventListener('keydown', (e) => {
  const block = $('#composer').dataset.kind === 'block';
  if (e.key !== 'Enter' || e.isComposing) return;
  if (block ? (e.ctrlKey || e.metaKey) : !e.shiftKey) { e.preventDefault(); $('#composer').requestSubmit(); }
});
$('#btn-finish').addEventListener('click', () => sendReply($('#reply').value, true));

$('#btn-cancel').addEventListener('click', async () => {
  if (!S.run || S.run.done) return;
  try { await api(`/api/runs/${S.run.id}/cancel`, { method: 'POST', body: {} }); } catch (err) { toast(err.message); }
});
$('#btn-working-cancel').addEventListener('click', () => $('#btn-cancel').click());
$('#btn-back').addEventListener('click', leaveSession);
$('#btn-open-note').addEventListener('click', () => { if (S.run && S.run.slug) openNote(S.run.slug); });
$('#btn-resume').addEventListener('click', () => { if (S.run) resumeFrom(S.run.slug); });

// closes a finished session and goes back to the list
function leaveSession() {
  if (S.run && S.run.es) S.run.es.close();
  S.run = null;
  showHistory();
}

async function startRun(body, title) {
  try {
    const { id } = await api('/api/runs', { method: 'POST', body });
    attachRun(id, title);
  } catch (err) { toast(err.message); }
}

// Sessions run one phase at a time, so a stored one is picked up by phase: the next step depends on how far the note got.
const RESUME = {
  new: { label: 'Continuar: estudiarlo', run: (n) => ({ cmd: 'study', topic: n.label, title: n.label }) },
  prepared: { label: 'Continuar: responder preguntas', run: (n) => ({ cmd: 'quiz', slug: n.id, n: 5, difficulty: 'media', title: n.label }) },
  tested: { label: 'Continuar: explicarlo', run: (n) => ({ cmd: 'feynman', slug: n.id, title: n.label }) },
};
const RESUMABLE = new Set(['study', 'prepare', 'quiz', 'feynman', 'interactive']);
const sessionNode = (run) => run && run.slug && S.graph && S.graph.nodes.find((n) => n.id === run.slug);

function resumeFrom(slug) {
  const n = S.graph && S.graph.nodes.find((x) => x.id === slug);
  const step = n && RESUME[n.status];
  if (step) startRun(step.run(n), n.label);
}

// the buttons in the session header depend on whether it is running, over, and what its note looks like now
function updateSessionActions() {
  const run = S.run;
  if (!run) return;
  const node = sessionNode(run);
  const step = run.done && node && RESUMABLE.has(run.cmd) && RESUME[node.status];
  $('#btn-cancel').hidden = run.done;
  $('#btn-back').hidden = !run.done;
  $('#btn-open-note').hidden = !(run.done && node);
  $('#btn-resume').hidden = !step;
  if (step) $('#btn-resume').textContent = step.label;
}

/* ---------- session history ---------- */
const RUN_STATUS = { running: 'En curso', done: 'Terminada', error: 'Con error', cancelled: 'Cancelada', interrupted: 'Interrumpida' };
const RUN_COLOR = { running: 'var(--l0)', done: 'var(--s-explained)', error: 'var(--danger)', cancelled: 'var(--l-none)', interrupted: 'var(--l-none)' };
const when = (iso) => new Date(iso).toLocaleString('es', { dateStyle: 'medium', timeStyle: 'short' });

async function showHistory() {
  $('#history').hidden = false;
  $('#live').hidden = true;
  try { renderHistory(await api('/api/sessions')); } catch (err) { toast(err.message); }
}

function renderHistory(list) {
  const ul = $('#history-list');
  ul.replaceChildren();
  for (const r of list) {
    const open = () => attachRun(r.id, r.title, { replay: r.status !== 'running' });
    ul.append(h('li', { class: 'history-item', style: `--c: ${RUN_COLOR[r.status] || 'var(--l-none)'}` },
      h('span', { class: 'hi-bar', 'aria-hidden': 'true' }),
      h('button', { class: 'hi-title', type: 'button', onclick: open }, r.title || 'Sesión'),
      h('div', { class: 'hi-meta' }, `${when(r.started)} · ${RUN_STATUS[r.status] || r.status}`),
      h('div', { class: 'hi-actions' },
        h('button', { class: 'btn small', type: 'button', onclick: open }, r.status === 'running' ? 'Volver' : 'Ver'),
        r.status !== 'running' && h('button', { class: 'btn small', type: 'button', 'aria-label': `Eliminar la sesión «${r.title}»`, onclick: async () => {
          if (!confirm(`¿Eliminar la sesión «${r.title}»? Tus notas se conservan.`)) return;
          try { await api(`/api/sessions/${r.id}`, { method: 'DELETE' }); showHistory(); } catch (err) { toast(err.message); }
        } }, 'Eliminar'))));
  }
}

// Shows a session: a live one streams in, a stored one replays from its first event.
function attachRun(id, title, { replay = false } = {}) {
  if (S.run && S.run.es) S.run.es.close();
  resetTranscript();
  $('#session-title').textContent = title || 'Sesión';
  $('#history').hidden = true;
  $('#live').hidden = false;
  $('#composer').hidden = true;
  if (!replay) $('#session-dot').hidden = false;
  setWorking(!replay, 'Pensando…');

  const run = { id, done: false, replay, cmd: '', slug: '', es: new EventSource(`/api/runs/${id}/events?from=0`) };
  S.run = run;
  updateSessionActions();
  showView('session');
  run.es.onmessage = (ev) => handleEvent(run, JSON.parse(ev.data));
  run.es.onerror = () => { if (run.done) run.es.close(); }; // otherwise the browser reconnects with Last-Event-ID
  // which topic the session worked on, so it can offer to open or continue it
  api('/api/sessions').then((list) => {
    const me = list.find((r) => r.id === id);
    if (me && S.run === run) { run.cmd = me.cmd; run.slug = me.slug || ''; updateSessionActions(); }
  }).catch(() => {});
}

function handleEvent(run, e) {
  if (run !== S.run) return;
  switch (e.type) {
    case 'out': botText(e.text); break;
    case 'in': {
      closeChoices();
      // a button answers with a short code ("s", "media"); show what the learner actually clicked
      const known = (T.echo && T.echo.reply === e.text && T.echo) || ((run.lastAsk && quickOptions(run.lastAsk)) || []).find((o) => o.reply === e.text);
      T.echo = null;
      addMsg('user', mdBlock(known ? known.label : e.text, { breaks: true }));
      break;
    }
    case 'card': closeBubble(); renderCard(e.card); break;
    case 'ask':
      run.lastAsk = e;
      if (!run.replay) setAsk(e);
      break;
    case 'done': finishRun(run, e); break;
  }
}

async function finishRun(run, e) {
  run.done = true;
  run.es.close();
  closeBubble();
  closeChoices();
  const current = $('#stepper li[aria-current]');
  if (current && !e.error) current.dataset.state = 'done';
  setWorking(false);
  $('#composer').hidden = true;
  if (!run.replay) $('#session-dot').hidden = true;
  if (e.error) addMsg('bot error', e.error);
  else if (!run.replay) addMsg('note', 'Sesión terminada. Tus notas están guardadas.');
  updateSessionActions();
  if (!run.replay) { await refreshAll(); updateSessionActions(); }
}

/* ---------- boot ---------- */
async function refreshAll() {
  await Promise.allSettled([loadRoadmaps(), loadDaily()]); // roadmap titles and colours label the lines in the graph
  await loadGraph().catch((err) => toast(err.message));
  renderRecent();
  if (drawer.classList.contains('open')) renderDrawer();
  // refresh what is on screen without pulling the learner away from where they are
  if (S.view === 'map' && S.selected && !$('#note-panel').hidden) selectNode(S.selected, false, true);
  if (S.view === 'note' && S.readerSlug) openNote(S.readerSlug, true);
}

async function boot() {
  try {
    S.info = await api('/api/state');
    $('#provider-badge').textContent = S.info.provider;
    if (S.info.warning) { $('#banner').textContent = S.info.warning; $('#banner').hidden = false; }
    await refreshAll();
    if (S.info.activeRun) {
      const list = await api('/api/sessions').catch(() => []);
      const me = list.find((r) => r.id === S.info.activeRun);
      attachRun(S.info.activeRun, me ? me.title : 'Sesión en curso');
    }
  } catch (err) { toast(err.message); }
}
boot();
