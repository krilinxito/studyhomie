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
  const init = { method: opts.method || 'GET', headers: {} };
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

const STATUS = { new: 'Nuevo', prepared: 'Guía lista', tested: 'Probado', explained: 'Explicado' };
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
  return d;
}

/* ---------- state ---------- */
const S = { cy: null, graph: null, selected: null, note: null, roadmaps: [], info: null, run: null };

/* ---------- views ---------- */
function showView(name) {
  for (const v of ['map', 'session']) {
    $(`#view-${v}`).hidden = v !== name;
    $(`#tab-${v}`).setAttribute('aria-selected', String(v === name));
  }
  if (name === 'map' && S.cy) S.cy.resize();
}
$$('.tabs button').forEach((b) => b.addEventListener('click', () => showView(b.dataset.view)));

/* ---------- start forms ---------- */
$$('.seg button').forEach((b) => b.addEventListener('click', () => {
  $$('.seg button').forEach((x) => x.setAttribute('aria-selected', String(x === b)));
  for (const f of ['topic', 'roadmap', 'material']) $(`#form-${f}`).hidden = f !== b.dataset.form;
}));

$('#form-topic').addEventListener('submit', (e) => {
  e.preventDefault();
  const topic = $('#in-topic').value.trim();
  if (topic) startRun({ cmd: 'study', topic }, `Estudiando: ${topic}`);
});
$('#form-roadmap').addEventListener('submit', (e) => {
  e.preventDefault();
  const subject = $('#in-subject').value.trim();
  if (subject) startRun({ cmd: 'roadmap', subject }, `Roadmap: ${subject}`);
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
    await startRun({ cmd: 'material', files: up.files }, `Material: ${files[0].name}`);
  } catch (err) { toast(err.message); } finally { btn.disabled = false; }
});

/* ---------- daily ---------- */
async function loadDaily() {
  const d = await api('/api/daily');
  const box = $('#daily-body');
  box.replaceChildren();
  if (d.topic) {
    const done = d.status === 'explained';
    box.append(...[
      h('p', { class: 'daily-topic' }, d.topic),
      h('div', { class: 'daily-meta' },
        d.field && h('span', { class: 'chip' }, d.field),
        h('span', { class: 'chip status', style: `--c: var(--s-${d.status || 'new'})` }, STATUS[d.status] || 'Nuevo')),
      h('div', { class: 'actions' },
        done
          ? h('button', { class: 'btn', type: 'button', onclick: () => selectNode(d.slug, true) }, 'Ver mi nota')
          : h('button', { class: 'btn primary', type: 'button', onclick: () => startRun({ cmd: 'daily' }, `Reto de hoy: ${d.topic}`) }, 'Continuar')),
      done && h('p', { class: 'hint' }, 'Completado. Mañana habrá un tema nuevo.'),
    ].filter(Boolean)); // append() would print a literal "false"
    return;
  }
  const sel = h('select', { id: 'daily-field', 'aria-label': 'Campo del reto' },
    h('option', { value: '' }, 'Cualquiera de mis intereses'),
    ((S.info && S.info.interests) || []).map((f) => h('option', { value: f }, f)));
  box.append(h('div', { class: 'daily-pick' },
    h('p', { class: 'hint' }, 'Un tema aleatorio para ampliar tu cultura polímata, y un día para entenderlo.'),
    sel,
    h('button', { class: 'btn primary', type: 'button', onclick: () => startRun({ cmd: 'daily', field: sel.value }, 'Reto de hoy') }, 'Sortear reto')));
}

/* ---------- roadmaps ---------- */
async function loadRoadmaps() {
  S.roadmaps = await api('/api/roadmaps');
  const ul = $('#roadmaps');
  ul.replaceChildren();
  const filter = $('#filter-rm');
  const cur = filter.value;
  filter.replaceChildren(h('option', { value: '' }, 'Todos los temas'),
    ...S.roadmaps.map((r) => h('option', { value: r.slug }, r.title))); // native DOM methods do not take arrays
  filter.value = S.roadmaps.some((r) => r.slug === cur) ? cur : '';
  for (const r of S.roadmaps) {
    const pct = r.total ? Math.round((100 * r.done) / r.total) : 0;
    ul.append(h('li', { class: 'rm-item' },
      h('button', { class: 'rm-title', type: 'button', onclick: () => { filter.value = r.slug; applyFilters(); showView('map'); } }, r.title),
      h('div', { class: 'bar', role: 'progressbar', 'aria-valuenow': pct, 'aria-valuemin': 0, 'aria-valuemax': 100, 'aria-label': `Progreso de ${r.title}` }, h('i', { style: `width:${pct}%` })),
      h('div', { class: 'rm-foot' },
        h('span', {}, `${r.done}/${r.total} dominados`),
        r.next
          ? h('button', { class: 'btn small primary', type: 'button', title: r.next.title, onclick: () => startRun({ cmd: 'next', roadmap: r.slug }, `Siguiente: ${r.next.title}`) }, 'Siguiente')
          : h('span', {}, r.total ? 'Completo' : ''))));
  }
}

/* ---------- graph ---------- */
function cyStyle() {
  const ink = cssVar('--ink'), muted = cssVar('--muted'), accent = cssVar('--accent');
  const st = { new: cssVar('--s-new'), prepared: cssVar('--s-prepared'), tested: cssVar('--s-tested'), explained: cssVar('--s-explained') };
  const s = [
    { selector: 'node', style: {
      'background-color': st.new, label: 'data(label)', color: ink, 'font-size': 13, 'font-family': cssVar('--font'),
      'text-valign': 'bottom', 'text-margin-y': 5, 'text-wrap': 'wrap', 'text-max-width': 120,
      width: 'mapData(mastery, 0, 100, 22, 40)', height: 'mapData(mastery, 0, 100, 22, 40)',
      'border-width': 2, 'border-color': cssVar('--surface'), 'transition-property': 'opacity', 'transition-duration': '150ms' } },
    { selector: 'edge', style: { width: 1.5, 'line-color': muted, 'curve-style': 'bezier', opacity: .5 } },
    { selector: 'edge[kind="requires"]', style: { 'target-arrow-shape': 'triangle', 'target-arrow-color': muted, 'arrow-scale': .9 } },
    { selector: 'edge[kind="related"]', style: { 'line-style': 'dashed', width: 1, opacity: .28, 'line-color': muted } },
    { selector: 'node:selected', style: { 'border-width': 4, 'border-color': accent } },
    { selector: 'edge.hl', style: { opacity: 1, width: 2.2, 'line-color': accent, 'target-arrow-color': accent } },
    { selector: 'node.grouplabel', style: {
      width: 1, height: 1, 'background-opacity': 0, 'border-width': 0, label: 'data(label)', 'font-size': 15, 'font-weight': 700,
      color: ink, 'text-halign': 'right', 'text-valign': 'center', 'text-margin-x': 4, 'text-wrap': 'none', events: 'no' } },
    { selector: '.dim', style: { opacity: .12 } },
  ];
  for (const k of Object.keys(st)) s.push({ selector: `node[status="${k}"]`, style: { 'background-color': st[k] } });
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
  const rmTitle = (k) => (S.roadmaps.find((r) => r.slug === k) || {}).title || k;
  let y0 = 0;
  for (const key of keys) {
    const nodes = groups.get(key);
    labels.push({ id: `g:${key}`, label: key ? rmTitle(key) : 'Sin roadmap', x: 0, y: y0 - 46 });
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
  if (!S.cy) {
    S.cy = cytoscape({ container: $('#cy'), elements: [], style: cyStyle(), minZoom: .15, maxZoom: 3 });
    S.cy.on('tap', 'node', (ev) => selectNode(ev.target.id()));
    S.cy.on('tap', (ev) => { if (ev.target === S.cy) closePanel(); });
    matchMedia('(prefers-color-scheme: dark)').addEventListener('change', () => S.cy.style(cyStyle()));
  }
  if (same) {
    for (const n of g.nodes) {
      const el = S.cy.getElementById(n.id);
      el.data({ status: n.status, mastery: n.mastery, label: n.label });
    }
  } else {
    S.cy.elements().remove();
    const { pos, labels } = layeredLayout(g);
    S.cy.add([
      ...g.nodes.map((n) => ({ group: 'nodes', data: { id: n.id, label: n.label, status: n.status, mastery: n.mastery, roadmap: n.roadmap || '' }, position: pos[n.id] })),
      ...labels.map((l) => ({ group: 'nodes', data: { id: l.id, label: l.label }, position: { x: l.x, y: l.y }, classes: 'grouplabel', selectable: false, grabbable: false })),
      ...g.edges.map((e, i) => ({ group: 'edges', data: { id: `e${i}`, source: e.from, target: e.to, kind: e.kind } })),
    ]);
    if (g.nodes.length) S.cy.fit(undefined, 50);
  }
  applyFilters();
}

function applyFilters() {
  if (!S.cy) return;
  const q = slugify($('#search').value);
  const rm = $('#filter-rm').value;
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
$('#filter-rm').addEventListener('change', applyFilters);

/* ---------- note panel ---------- */
function closePanel() {
  $('#note-panel').hidden = true;
  S.selected = null;
  if (S.cy) { S.cy.elements().unselect(); S.cy.edges().removeClass('hl'); }
}

async function selectNode(slug, center = false) {
  showView('map');
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

function renderPanel(n) {
  const p = $('#note-panel');
  const m = n.meta;
  const status = m.status || 'new';
  const title = m.title || n.slug;
  p.replaceChildren();
  p.hidden = false;

  const nInput = h('input', { type: 'number', min: 1, max: 30, value: 5, 'aria-label': 'Número de preguntas' });
  const diff = h('select', { 'aria-label': 'Dificultad' },
    ['básica', 'media', 'avanzada'].map((d) => h('option', { value: d, selected: d === 'media' }, d)));
  const quiz = () => startRun({ cmd: 'quiz', slug: n.slug, n: Number(nInput.value) || 5, difficulty: diff.value }, `Prueba: ${title}`);
  const feyn = () => startRun({ cmd: 'feynman', slug: n.slug }, `Feynman: ${title}`);
  const btn = (label, fn, primary) => h('button', { class: `btn small${primary ? ' primary' : ''}`, type: 'button', onclick: fn }, label);

  const actions = [];
  if (status === 'new') actions.push(btn('Preparar y estudiar', () => startRun({ cmd: 'study', topic: title }, `Estudiando: ${title}`), true));
  if (status === 'prepared') actions.push(btn('Hacer la prueba', quiz, true), btn('Explicar (Feynman)', feyn));
  if (status === 'tested') actions.push(btn('Explicar (Feynman)', feyn, true), btn('Repetir prueba', quiz));
  if (status === 'explained') actions.push(btn('Repetir Feynman', feyn), btn('Repetir prueba', quiz));
  if (status !== 'new') actions.push(btn('Rehacer guía', () => startRun({ cmd: 'prepare', topic: title }, `Guía: ${title}`)));
  if (n.hasSimulation) actions.push(btn('Abrir simulación', () => openSim(n.slug, title)));
  else if (status !== 'new') actions.push(btn('Generar simulación', () => startRun({ cmd: 'interactive', slug: n.slug }, `Simulación: ${title}`)));

  p.append(...[
    h('div', { class: 'panel-head' }, h('h3', {}, title),
      h('button', { class: 'btn small', type: 'button', 'aria-label': 'Cerrar panel', onclick: closePanel }, '✕')),
    h('div', { class: 'chips' },
      h('span', { class: 'chip status', style: `--c: var(--s-${status})` }, STATUS[status] || status),
      m.roadmap && h('span', { class: 'chip' }, `Roadmap: ${(S.roadmaps.find((r) => r.slug === m.roadmap) || {}).title || m.roadmap}`),
      m.field && h('span', { class: 'chip' }, m.field),
      m.source && h('span', { class: 'chip' }, m.source)),
    m.mastery ? h('div', { class: 'mastery' },
      h('div', { class: 'row' }, h('span', {}, 'Dominio'), h('span', {}, `${m.mastery}/100`)),
      h('div', { class: 'bar' }, h('i', { style: `width:${m.mastery}%` }))) : null,
    h('div', { class: 'actions' }, actions),
    status !== 'new' ? h('div', { class: 'quiz-opts' }, 'Prueba:', nInput, 'preguntas', diff) : null,
  ].filter(Boolean)); // native append() would print a literal "null"

  const res = m.resources || [];
  if (res.length) {
    p.append(h('h4', {}, 'Material recomendado'), h('ul', { class: 'res-list' }, res.map((r) => resourceItem(r, n, title))));
  }
  p.append(h('h4', {}, 'Nota'), mdBlock(stripSection(n.body, 'Recursos')));
  if (n.requires && n.requires.length) p.append(h('h4', {}, 'Requiere'), linkChips(n.requires));
  if (n.backlinks && n.backlinks.length) p.append(h('h4', {}, 'Enlazan aquí'), linkChips(n.backlinks));
  p.scrollTop = 0;
}

function resourceItem(r, n, title) {
  const type = h('span', { class: 'type' }, TYPE[r.type] || r.type);
  const why = r.why && h('span', { class: 'why' }, r.why);
  if (r.url && /^https?:\/\//i.test(r.url)) {
    return h('li', { class: 'res' }, type, h('a', { href: r.url, target: '_blank', rel: 'noopener noreferrer' }, r.title), why);
  }
  if (r.url && r.url.startsWith('interactive/')) {
    return h('li', { class: 'res' }, type, h('button', { class: 'link', type: 'button', onclick: () => openSim(n.slug, title) }, r.title), why);
  }
  if (r.type === 'interactive') {
    return h('li', { class: 'res' }, type, h('strong', {}, `${r.title} (propuesta)`), why,
      h('div', {}, h('button', { class: 'btn small', type: 'button', onclick: () => startRun({ cmd: 'interactive', slug: n.slug, brief: r.why || '' }, `Simulación: ${title}`) }, 'Generar')));
  }
  return h('li', { class: 'res' }, type, h('strong', {}, r.title), why);
}

function linkChips(list) {
  return h('div', { class: 'links' }, list.map((l) => h('button', { type: 'button', onclick: () => selectNode(l.slug, true) }, l.title)));
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
      if (confirm(`«${title}» aún no está en tu mapa. ¿Quieres estudiarlo ahora?`)) startRun({ cmd: 'study', topic: title }, `Estudiando: ${title}`);
      return;
    }
    selectNode(slug, true);
  } else openSim(decodeURIComponent(href.slice(5)), S.note && S.note.meta.title);
});

/* ---------- sessions ---------- */
const T = { raw: '', bubble: null, timer: null, auto: true };

function resetTranscript() {
  $('#transcript').replaceChildren();
  T.raw = ''; T.bubble = null;
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

function setAsk(e) {
  closeBubble();
  setWorking(false);
  const block = e.kind === 'block';
  const form = $('#composer');
  const ta = $('#reply');
  form.hidden = false;
  form.dataset.kind = e.kind;
  ta.value = '';
  ta.rows = block ? 7 : 1;
  ta.placeholder = block ? 'Explícalo con tus palabras, como si el otro no supiera nada…' : (e.def ? `Enter para usar «${e.def}»` : 'Escribe tu respuesta');
  $('#composer-hint').textContent = block ? 'Ctrl + Enter para enviar' : '';
  $('#btn-finish').hidden = !block;
  $('#quick').hidden = !/\(s\/n\)/i.test(e.label || '');
  $('#btn-send').disabled = false;
  ta.focus();
  scrollDown();
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
$$('#quick button').forEach((b) => b.addEventListener('click', () => sendReply(b.dataset.reply, false)));

$('#btn-cancel').addEventListener('click', async () => {
  if (!S.run || S.run.done) return;
  try { await api(`/api/runs/${S.run.id}/cancel`, { method: 'POST', body: {} }); } catch (err) { toast(err.message); }
});
$('#btn-back').addEventListener('click', () => showView('map'));

async function startRun(body, title) {
  try {
    const { id } = await api('/api/runs', { method: 'POST', body });
    attachRun(id, title);
  } catch (err) { toast(err.message); }
}

function attachRun(id, title) {
  if (S.run && S.run.es) S.run.es.close();
  resetTranscript();
  $('#session-title').textContent = title || 'Sesión';
  $('#btn-cancel').hidden = false;
  $('#btn-back').hidden = true;
  $('#composer').hidden = true;
  $('#session-dot').hidden = false;
  setWorking(true, 'Pensando…');
  showView('session');

  const run = { id, done: false, es: new EventSource(`/api/runs/${id}/events?from=0`) };
  S.run = run;
  run.es.onmessage = (ev) => handleEvent(run, JSON.parse(ev.data));
  run.es.onerror = () => { if (run.done) run.es.close(); }; // otherwise the browser reconnects with Last-Event-ID
}

function handleEvent(run, e) {
  if (run !== S.run) return;
  switch (e.type) {
    case 'out': botText(e.text); break;
    case 'in': addMsg('user', mdBlock(e.text, { breaks: true })); break;
    case 'ask': setAsk(e); break;
    case 'done': finishRun(run, e); break;
  }
}

async function finishRun(run, e) {
  run.done = true;
  run.es.close();
  closeBubble();
  setWorking(false);
  $('#composer').hidden = true;
  $('#btn-cancel').hidden = true;
  $('#btn-back').hidden = false;
  $('#session-dot').hidden = true;
  if (e.error) addMsg('bot error', e.error);
  else addMsg('note', 'Sesión terminada. Tus notas están guardadas en el vault.');
  await refreshAll();
}

/* ---------- boot ---------- */
async function refreshAll() {
  await Promise.allSettled([loadRoadmaps(), loadDaily()]); // roadmap titles label the groups in the graph
  await loadGraph().catch((err) => toast(err.message));
  if (S.selected && !$('#note-panel').hidden) selectNode(S.selected);
}

async function boot() {
  try {
    S.info = await api('/api/state');
    $('#provider-badge').textContent = S.info.provider;
    if (S.info.warning) { $('#banner').textContent = S.info.warning; $('#banner').hidden = false; }
    await refreshAll();
    if (S.info.activeRun) attachRun(S.info.activeRun, 'Sesión en curso');
  } catch (err) { toast(err.message); }
}
boot();
