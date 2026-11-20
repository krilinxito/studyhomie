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
const foldSlug = (s) => s.normalize('NFD').replace(/[̀-ͯ]/g, '');

// per-browser conveniences (open guide, tab, depth); the app works the same without them
const store = {
  get(k, d) { try { const v = localStorage.getItem(`sh.${k}`); return v == null ? d : JSON.parse(v); } catch { return d; } },
  set(k, v) { try { localStorage.setItem(`sh.${k}`, JSON.stringify(v)); } catch { /* private mode */ } },
};

const STATUS = { new: 'Por descubrir', prepared: 'Guía lista', tested: 'Comprobado', explained: 'Explicado' };
const SECTION = { 'Guía': 'La guía', 'Conceptos base': 'Conceptos base', 'Dudas': 'Dudas', 'Prueba': 'Preguntas', 'Feynman': 'Explicación', 'Conexiones': 'Conexiones' };
const TYPE = { video: 'Video', web: 'Web', book: 'Libro', paper: 'Paper', interactive: 'Interactivo' };
const DEPTH = ['Solo el tema', 'Lo inmediato', 'Dos niveles', 'Tres niveles', 'Desde lo básico'];
const cssVar = (n) => getComputedStyle(document.documentElement).getPropertyValue(n).trim();
const smoothness = () => (matchMedia('(prefers-reduced-motion: reduce)').matches ? 'auto' : 'smooth');
const isPhone = () => matchMedia('(max-width: 880px)').matches;

// Every guide is a metro line with its own colour (--l0 … --l7); a guide inside a roadmap shares the
// roadmap's line, and its concepts ride on it. The colour comes from the line's key, probing forward
// on a clash, so a line keeps its colour as long as the others do not change.
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
  if (!(opts && opts.streaming)) Viz.enhance(d, opts && opts.viz);
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
  guide: store.get('guide', ''), note: null, concept: null, tab: store.get('tab', 'guide'),
  run: null, depth: store.get('depth', 1), cy: null, needsFit: false, filter: '', chats: [],
};

/* ---------- layout: three columns on a desk, one pane at a time on a phone ---------- */
function setLayout(layout) {
  document.body.dataset.layout = layout;
  $('#btn-show-panel').hidden = layout !== 'chat';
  $('#btn-wide').textContent = layout === 'panel' ? 'Reducir' : 'Ampliar';
  store.set('layout', layout);
  fitMap();
}
$('#btn-wide').addEventListener('click', () => setLayout(document.body.dataset.layout === 'panel' ? 'both' : 'panel'));
$('#btn-hide-panel').addEventListener('click', () => { if (isPhone()) setPane('chat'); else setLayout('chat'); });
$('#btn-show-panel').addEventListener('click', () => setLayout('both'));

function setPane(pane) {
  document.body.dataset.pane = pane;
  $$('.panes button').forEach((b) => b.setAttribute('aria-pressed', String(b.dataset.pane === pane)));
  fitMap();
}
$$('.panes button').forEach((b) => b.addEventListener('click', () => setPane(b.dataset.pane)));

// shows the study panel, wherever it is
function revealPanel() {
  if (isPhone()) setPane('panel');
  else if (document.body.dataset.layout === 'chat') setLayout('both');
}

function setTab(tab) {
  S.tab = tab;
  store.set('tab', tab);
  $$('.ptabs button').forEach((b) => b.setAttribute('aria-selected', String(b.dataset.tab === tab)));
  for (const t of ['guide', 'concepts', 'map', 'practice']) $(`#p-${t}`).hidden = t !== tab;
  if (tab === 'map') fitMap();
  if (tab === 'concepts') renderConcepts();
  if (tab === 'practice') renderPractice();
}
$$('.ptabs button').forEach((b) => b.addEventListener('click', () => setTab(b.dataset.tab)));

/* ---------- opening notes ---------- */
// Opens a guide (Guía tab) or a concept (Conceptos tab) in the panel.
async function openNote(slug, { quiet = false, reveal = true } = {}) {
  try {
    const n = await api(`/api/notes/${encodeURIComponent(slug)}`);
    if (n.kind === 'concept') {
      S.concept = n;
      if (!quiet) setTab('concepts'); else if (S.tab === 'concepts') renderConcepts();
      $('#p-concepts').scrollTop = 0;
    } else {
      setGuide(n);
      if (!quiet) setTab('guide');
    }
    if (reveal && !quiet) revealPanel();
  } catch (err) { toast(err.message); }
}

function setGuide(n) {
  const changed = !S.note || S.note.slug !== n.slug;
  S.note = n;
  S.guide = n.slug;
  store.set('guide', n.slug);
  if (changed) S.concept = null;
  const top = $('#p-guide').scrollTop;
  renderReader(n);
  $('#p-guide').scrollTop = changed ? 0 : top;
  if (S.tab === 'concepts') renderConcepts();
  if (S.tab === 'practice') renderPractice();
}

async function reloadGuide() {
  if (!S.guide) return;
  try { setGuide(await api(`/api/notes/${encodeURIComponent(S.guide)}`)); } catch { closeGuide(); }
}
function closeGuide() {
  S.note = null; S.guide = ''; S.concept = null;
  store.set('guide', '');
  renderReader(null);
  if (S.tab === 'concepts') renderConcepts();
  if (S.tab === 'practice') renderPractice();
}

// wikilinks and simulation links rendered anywhere in the app
document.addEventListener('click', (e) => {
  const a = e.target.closest('a[href^="#note/"], a[href^="#sim/"]');
  if (!a) return;
  e.preventDefault();
  const href = a.getAttribute('href');
  if (href.startsWith('#sim/')) { openSim(decodeURIComponent(href.slice(5)), S.note && S.note.meta.title); return; }
  const slug = decodeURIComponent(href.slice(6));
  const known = nodeOf(slug) || nodeOf(foldSlug(slug));
  if (known) { openNote(known.id); return; }
  // a link to something not in the vault yet is a gap: offer to fill it
  const title = a.textContent.trim() || slug;
  if (confirm(`«${title}» aún no está en tu vault. ¿Te preparo una guía?`)) chatAction({ action: 'guide', topic: title, depth: S.depth }, `Guía: ${title}`);
});

function openSim(slug, title) {
  $('#sim-title').textContent = title || 'Simulación';
  $('#sim-frame').src = `/interactive/${encodeURIComponent(slug)}.html`;
  $('#sim-dialog').showModal();
}
$('#sim-close').addEventListener('click', () => $('#sim-dialog').close());
$('#sim-dialog').addEventListener('close', () => { $('#sim-frame').src = 'about:blank'; });

/* ---------- Guía tab: the guide as an article ---------- */
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
  return h('div', { class: 'guide-list' }, keys.map((k) => h('section', { class: 'dg', style: k ? `--lc: ${lineColor(k)}` : null },
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
      h('p', {}, 'Pide en el chat lo que quieras entender. La profundidad, junto al cuadro de texto, decide cuántos niveles de conceptos base se escriben como notas aparte.'),
      guideList() || ''));
    return;
  }
  const m = n.meta;
  const status = m.status || 'new';
  const title = m.title || n.slug;
  const node = nodeOf(n.slug);
  const concepts = n.concepts || [];
  const wrap = h('div', { class: 'reader-wrap', style: `--line-c: ${lineColor(node ? node.group : '')}` });
  wrap.append(h('header', { class: 'r-head' },
    h('button', { class: 'crumb', type: 'button', onclick: closeGuide }, '← Todas las guías'),
    h('p', { class: 'r-line' }, m.roadmap ? groupTitle(m.roadmap) : 'Guía'),
    h('h1', {}, title),
    h('div', { class: 'chips' }, statusChip(status),
      m.depth ? h('span', { class: 'chip' }, `Profundidad ${m.depth}`) : null,
      m.focus ? h('span', { class: 'chip' }, `Foco: ${m.focus}`) : null,
      m.field ? h('span', { class: 'chip' }, m.field) : null),
    m.mastery ? h('div', { class: 'mastery' },
      h('div', { class: 'row' }, h('span', {}, 'Lo que llevas'), h('span', {}, `${m.mastery}/100`)),
      h('div', { class: 'bar' }, h('i', { style: `width:${m.mastery}%; background: var(--line-c)` }))) : null,
    h('div', { class: 'actions' },
      concepts.length ? h('button', { class: 'btn small yellow', type: 'button', onclick: () => setTab('concepts') }, `${concepts.length} conceptos base`) : null,
      status !== 'new' ? h('button', { class: 'btn small green', type: 'button', onclick: () => setTab('practice') }, 'Practicar') : null,
      h('button', { class: 'btn small blue', type: 'button', onclick: () => askAbout(`Sobre «${title}»: `) }, 'Preguntar en el chat'))));

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
    article.append(h('section', { class: 'r-next' }, h('h2', {}, 'En la línea'),
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

/* ---------- Conceptos tab: the guide's concept notes, level by level ---------- */
// level 1 is what the topic needs directly, then what those need, and so on
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
  const out = [];
  for (const c of list) (out[level.get(c.slug) - 1] ||= []).push(c);
  return out.filter(Boolean);
}
const levelName = (l, last) => (l === 1 ? 'Lo que el tema usa directamente' : last ? 'La base de todo' : `Base de la base (nivel ${l})`);

function renderConcepts() {
  const root = $('#p-concepts');
  root.replaceChildren();
  if (S.concept) { root.append(conceptReader(S.concept)); return; }
  const n = S.note;
  const box = h('div', { class: 'concepts-wrap' });
  if (n && (n.concepts || []).length) {
    const node = nodeOf(n.slug);
    const lc = lineColor(node ? node.group : '');
    const levels = conceptLevels(n);
    box.append(
      h('header', { class: 'c-head' },
        h('p', { class: 'eyebrow' }, 'Conceptos base'),
        h('h2', {}, n.meta.title),
        h('p', { class: 'hint' }, 'Cada nivel sostiene al de arriba. Si algo de arriba no se entiende, baja un nivel.')),
      h('ol', { class: 'levels', style: `--lc: ${lc}` }, levels.map((lv, i) => h('li', { class: 'level' },
        h('h3', {}, levelName(i + 1, i === levels.length - 1 && i > 0)),
        h('ul', {}, lv.map((c) => h('li', {}, h('button', { class: `station-btn${c.exists ? '' : ' missing'}`, type: 'button', disabled: !c.exists, onclick: () => openNote(c.slug) },
          h('i', { class: 'st st-concept', 'aria-hidden': 'true' }),
          h('span', {}, h('strong', {}, c.title), c.summary ? h('small', {}, c.summary) : null, !c.exists ? h('small', {}, 'No se pudo escribir') : null)))))))));
  } else if (n) {
    box.append(h('div', { class: 'panel-empty' }, h('h3', {}, 'Esta guía no tiene notas de concepto'),
      h('p', {}, 'Pide en el chat una guía con más profundidad, o la nota de un concepto concreto: «hazme una nota sobre la varianza».')));
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
    box.append(h('div', { class: 'panel-empty' }, h('h3', {}, 'Aún no hay conceptos'), h('p', {}, 'Las guías con profundidad 1 o más escriben aquí sus conceptos base, cada uno como una nota aparte.')));
  }
  root.append(box);
}

function conceptReader(c) {
  const m = c.meta;
  const node = nodeOf(c.slug);
  const body = stripEmoji(c.body.replace(/^# .*\n+/, '')).trim();
  return h('div', { class: 'reader-wrap concept', style: `--line-c: ${lineColor(node ? node.group : '')}` },
    h('header', { class: 'r-head' },
      h('button', { class: 'crumb', type: 'button', onclick: () => { S.concept = null; renderConcepts(); } }, S.note ? `← Conceptos de ${S.note.meta.title}` : '← Conceptos'),
      h('p', { class: 'r-line' }, ['Concepto', m.category].filter(Boolean).join(' · ')),
      h('h1', {}, m.title || c.slug),
      (m.aliases || []).length ? h('div', { class: 'chips' }, m.aliases.map((a) => h('span', { class: 'chip' }, a))) : null,
      (c.requires || []).length ? h('div', { class: 'c-req' }, h('span', { class: 'hint' }, 'Se apoya en'), h('div', { class: 'links' }, c.requires.map((l) => h('button', { type: 'button', onclick: () => openNote(l.slug) }, l.title)))) : null,
      h('div', { class: 'actions' }, h('button', { class: 'btn small blue', type: 'button', onclick: () => askAbout(`Sobre «${m.title}»: `) }, 'Preguntar en el chat'))),
    h('article', { class: 'r-article' }, mdBlock(stripSection(body, 'Aparece en'), { viz: vizCtx(c.slug) }),
      (c.guides || []).length ? h('section', { class: 'r-next' }, h('h2', {}, 'Aparece en'),
        c.guides.map((g) => h('button', { class: 'neighbor', type: 'button', onclick: () => openNote(g.slug) }, h('small', {}, 'Guía'), h('strong', {}, g.title)))) : null));
}

function askAbout(prefix) {
  if (isPhone()) setPane('chat');
  const ta = $('#reply');
  ta.value = prefix;
  ta.focus();
  ta.setSelectionRange(ta.value.length, ta.value.length);
}

/* ---------- Practicar tab ---------- */
const STEP_RANK = { new: 0, prepared: 1, tested: 2, explained: 3 };
function renderPractice() {
  const root = $('#p-practice');
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
      h('span', { class: `step static${i === current ? ' go' : ''}`, 'data-state': s.done ? 'done' : i === current ? 'current' : 'todo' }, h('span', { class: 'step-n' }, s.done ? '✓' : String(i + 1)), s.label)))),
    h('section', { class: 'p-card' },
      h('h3', {}, 'Preguntas'),
      h('p', { class: 'hint' }, 'Piden razonar o predecir, no memorizar. Se corrigen al final y el resultado queda en la guía.'),
      h('div', { class: 'quiz-opts' }, nInput, 'preguntas, dificultad', diff,
        h('button', { class: 'btn green', type: 'button', onclick: () => {
          const q = Number(nInput.value) || 5;
          chatAction({ action: 'quiz', n: q, difficulty: diff.value }, `Quiz de «${title}»: ${q} preguntas, ${diff.value}`);
        } }, 'Empezar'))),
    h('section', { class: 'p-card' },
      h('h3', {}, 'Explícamelo tú'),
      h('p', { class: 'hint' }, 'Me lo explicas como a alguien que no sabe nada. Te señalo los huecos y te pregunto lo que preguntaría un novato.'),
      h('button', { class: 'btn blue', type: 'button', onclick: () => chatAction({ action: 'feynman' }, `Quiero explicar «${title}»`) }, 'Empezar a explicar')),
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
  function show() {
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
      card.replaceChildren(h('p', { class: 'eyebrow' }, c.title), mdBlock(text || '_Sin resumen._'),
        h('div', { class: 'actions' },
          h('button', { class: 'btn small green', type: 'button', onclick: () => next(true) }, 'Lo sabía'),
          h('button', { class: 'btn small', type: 'button', onclick: () => next(false) }, 'A repasar'),
          h('button', { class: 'btn small', type: 'button', onclick: () => openNote(c.slug) }, 'Abrir la nota')));
    }
  }
  function next(ok) { seen++; if (ok) known++; i = (i + 1) % deck.length; show(); }
  sec.append(h('p', { class: 'hint' }, 'Di la idea en una frase antes de dar la vuelta a la tarjeta.'), card, score);
  show();
  return sec;
}

/* ---------- Mapa tab ---------- */
// A transit map: each guide is a line, its concepts are stations leading into it.
function cyStyle() {
  const ink = cssVar('--ink'), bg = cssVar('--bg'), muted = cssVar('--muted');
  const lineOf = (i) => cssVar(i < 0 ? '--l-none' : `--l${i}`);
  const s = [
    { selector: 'node', style: {
      'background-color': bg, 'border-width': 4, 'border-color': lineOf(-1), shape: 'ellipse', width: 22, height: 22,
      label: 'data(label)', color: ink, 'font-size': 13, 'font-weight': 700, 'font-family': cssVar('--font'),
      'text-valign': 'bottom', 'text-margin-y': 8, 'text-wrap': 'wrap', 'text-max-width': 130,
      'text-background-color': bg, 'text-background-opacity': .9, 'text-background-padding': 2,
      'z-index-compare': 'manual', 'z-index': 10, 'overlay-opacity': 0 } },
    { selector: 'node[kind = "concept"]', style: { width: 13, height: 13, 'border-width': 3, 'font-size': 11.5, 'font-weight': 600 } },
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
      { selector: `node[line = ${i}][kind = "concept"]`, style: { 'background-color': c } },
      { selector: `node[line = ${i}][status = "prepared"]`, style: { 'pie-size': '100%', 'pie-1-background-color': c, 'pie-1-background-size': 50, 'pie-1-background-opacity': 1 } },
      { selector: `node[line = ${i}][status = "tested"]`, style: { 'background-color': c } },
      { selector: `node[line = ${i}][status = "explained"]`, style: { 'background-color': c, 'outline-width': 3, 'outline-color': c, 'outline-offset': 3, 'outline-opacity': 1 } },
      { selector: `edge[kind = "requires"][line = ${i}]`, style: { 'line-color': c } },
      { selector: `node.grouplabel[line = ${i}]`, style: { color: c } },
    );
  }
  s.push(
    { selector: 'node:selected', style: { 'outline-width': 3, 'outline-color': ink, 'outline-offset': 4, 'outline-opacity': 1 } },
    { selector: '.dim', style: { opacity: .12 } },
  );
  return s;
}

const GX = 190, GY = 66; // gap between layers, gap between stations
// Layered layout: each line is a block, and prerequisites flow left to right into the guide.
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
    y0 += tallest * GY + 130;
  }
  return { pos, labels };
}

const graphSignature = (g) => g.nodes.map((n) => n.id).sort().join('|') + '#' + g.edges.map((e) => `${e.from}>${e.to}`).sort().join('|');

async function loadGraph() {
  const [g, rms] = await Promise.all([api('/api/graph'), api('/api/roadmaps').catch(() => [])]);
  const same = S.graph && graphSignature(S.graph) === graphSignature(g);
  S.graph = g;
  S.roadmaps = rms;
  S.lines = assignLines(new Set(g.nodes.map((n) => n.group)));
  $('#map-empty').hidden = g.nodes.length > 0;
  const lineOf = (k) => (S.lines.has(k) ? S.lines.get(k) : -1);
  const lineOfNode = new Map(g.nodes.map((n) => [n.id, lineOf(n.group)]));
  if (!S.cy) {
    S.cy = cytoscape({ container: $('#cy'), elements: [], style: cyStyle(), minZoom: .15, maxZoom: 3 });
    S.cy.on('tap', 'node', (ev) => { if (!ev.target.hasClass('grouplabel')) openNote(ev.target.id(), { reveal: false }); });
  }
  if (same) {
    for (const n of g.nodes) S.cy.getElementById(n.id).data({ status: n.status, label: n.label, line: lineOfNode.get(n.id) });
  } else {
    S.cy.elements().remove();
    const { pos, labels } = layeredLayout(g);
    S.cy.add([
      ...g.nodes.map((n) => ({ group: 'nodes', data: { id: n.id, label: n.label, status: n.status, kind: n.kind, grp: n.group, line: lineOfNode.get(n.id) }, position: pos[n.id] })),
      ...labels.map((l) => ({ group: 'nodes', data: { id: `gl:${l.group}`, label: l.label, grp: l.group, line: lineOf(l.group) }, position: { x: l.x, y: l.y }, classes: 'grouplabel', selectable: false, grabbable: false })),
      ...g.edges.map((e, i) => ({ group: 'edges', data: { id: `e${i}`, source: e.from, target: e.to, kind: e.kind, line: lineOfNode.get(e.to) } })),
    ]);
    S.needsFit = g.nodes.length > 0;
    fitMap();
  }
  renderChips();
  applyFilters();
}

// Cytoscape cannot fit a graph into a hidden container, so a new layout waits until the map is on screen.
function fitMap() {
  if (!S.cy || S.tab !== 'map' || $('#p-map').hidden) return;
  requestAnimationFrame(() => {
    S.cy.resize();
    if (S.needsFit) { S.cy.fit(undefined, 40); S.needsFit = false; }
  });
}

function renderChips() {
  const box = $('#filter-rm');
  box.replaceChildren();
  const keys = [...S.lines.keys()].filter((k) => !k.startsWith('c:'));
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
const T = { raw: '', bubble: null, timer: null, auto: true, progress: null, plan: null, echo: null, choiceCard: null };

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
  $('#transcript').replaceChildren();
  Object.assign(T, { raw: '', bubble: null, progress: null, plan: null, choiceCard: null, echo: null, auto: true });
}

function emptyState() {
  const ex = [
    'Explícame la transformada de Fourier, enfocada en audio',
    '¿Por qué la desviación estándar divide entre n − 1?',
    'Quiero entender los autovalores desde lo más básico',
  ];
  return h('div', { class: 'chat-empty' },
    h('p', { class: 'eyebrow' }, 'StudyHomie'),
    h('h1', {}, '¿Qué quieres entender hoy?'),
    h('p', { class: 'lead' }, 'Pregunta lo que sea o pide una guía. La guía trae sus conceptos base como notas aparte, tan hondo como marque la profundidad, y visuales para mover y probar.'),
    h('div', { class: 'suggest' },
      h('button', { class: 'btn small green', type: 'button', onclick: () => chatAction({ action: 'daily', field: '' }, 'El reto de hoy') }, 'El reto de hoy'),
      h('button', { class: 'btn small blue', type: 'button', onclick: () => $('#attach-input').click() }, 'Subir apuntes'),
      h('button', { class: 'btn small red', type: 'button', onclick: () => askAbout('Quiero aprender toda una materia: ') }, 'Planear una materia')),
    h('ul', { class: 'examples' }, ex.map((t) => h('li', {}, h('button', { type: 'button', onclick: () => { $('#reply').value = t; $('#reply').focus(); } }, t)))));
}

function botText(text) {
  T.raw += text;
  if (!T.bubble) {
    T.bubble = h('div', { class: 'msg bot' });
    $('#transcript').append(T.bubble);
  }
  clearTimeout(T.timer);
  T.timer = setTimeout(() => flushBot(true), 60);
}
function flushBot(streaming) {
  clearTimeout(T.timer);
  if (!T.bubble) return;
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

function setWorking(on, text) {
  $('#working').hidden = !on;
  if (text) $('#working-text').textContent = text;
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
  $('#btn-finish').hidden = mode !== 'block';
  ta.disabled = $('#btn-send').disabled = mode === 'busy';
  ta.rows = mode === 'block' ? 6 : 2;
  ta.placeholder = {
    chat: 'Pregunta lo que quieras, o pide una guía: «Explícame la transformada de Fourier, enfocada en audio»',
    line: e.def ? `Enter para usar «${e.def}»` : 'Escribe tu respuesta',
    block: 'Explícalo con tus palabras, como si el otro no supiera nada…',
    busy: 'Espera a que termine, o detenlo.',
  }[mode];
  $('#composer-hint').textContent = mode === 'block' ? 'Ctrl + Enter para enviar' : mode === 'chat' ? 'Enter envía · Shift + Enter, nueva línea' : '';
  if (mode !== 'busy' && !isPhone()) (opts ? $('#quick .primary') : ta)?.focus({ preventScroll: true });
}

// What the learner sends goes to the live chat, or starts one (continuing the chat on screen, if any).
async function chatSend(body) {
  const run = S.run;
  body = { depth: S.depth, guide: S.guide || '', ...body };
  if (run && !run.done && run.waiting === 'chat') {
    run.waiting = '';
    setComposer('busy');
    setWorking(true, 'Pensando…');
    try { await api(`/api/runs/${run.id}/input`, { method: 'POST', body }); return true; } catch (err) {
      toast(err.message); run.waiting = 'chat'; setComposer('chat'); setWorking(false); return false;
    }
  }
  if (run && !run.done) { toast('Espera a que termine lo que está en curso, o detenlo.'); return false; }
  try {
    const req = { cmd: 'chat', chat: body, guide: S.guide || '' };
    if (run && run.done) req.resume = run.id;
    setComposer('busy');
    setWorking(true, 'Pensando…');
    const { id } = await api('/api/runs', { method: 'POST', body: req });
    attachRun(id, run && run.done ? run.title : null);
    loadChats();
    return true;
  } catch (err) { toast(err.message); setComposer('chat'); setWorking(false); return false; }
}

// a button that asks the chat to do something, without going through the model
function chatAction(action, label) {
  if (isPhone()) setPane('chat');
  chatSend({ text: '', action, label });
}

async function sendReply(text, finish = false) {
  const run = S.run;
  if (!run || run.done) return;
  run.waiting = '';
  setComposer('busy');
  setWorking(true, 'Pensando…');
  try { await api(`/api/runs/${run.id}/input`, { method: 'POST', body: { text, finish } }); } catch (err) { toast(err.message); setWorking(false); }
}

$('#composer').addEventListener('submit', async (e) => {
  e.preventDefault();
  const ta = $('#reply');
  const mode = $('#composer').dataset.kind || 'chat';
  const text = ta.value;
  if (mode === 'busy') return;
  if (mode === 'chat') {
    if (!text.trim()) return;
    ta.value = '';
    if (!(await chatSend({ text }))) ta.value = text;
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

// attachments: uploaded, then sent to the chat, which builds a study line from them
$('#btn-attach').addEventListener('click', () => $('#attach-input').click());
$('#attach-input').addEventListener('change', async (e) => {
  const files = e.target.files;
  if (!files.length) return;
  const fd = new FormData();
  for (const f of files) fd.append('files', f);
  try {
    setWorking(true, 'Subiendo…');
    const up = await api('/api/upload', { method: 'POST', body: fd });
    await chatSend({ text: '', files: up.files, label: `Mis apuntes: ${[...files].map((f) => f.name).join(', ')}` });
  } catch (err) { toast(err.message); setWorking(false); } finally { e.target.value = ''; }
});

// depth: five stations on a line, from 0 (the topic alone) to 4 (down to the basics)
function renderDepth() {
  $('#depth-stations').replaceChildren(...DEPTH.map((label, d) => h('button', {
    type: 'button', role: 'radio', 'aria-checked': String(d === S.depth), class: d <= S.depth ? 'on' : null,
    title: `${d}: ${label}`, 'aria-label': `Profundidad ${d}: ${label}`,
    onclick: () => { S.depth = d; store.set('depth', d); renderDepth(); },
  })));
  $('#depth-text').textContent = DEPTH[S.depth];
}

/* ---------- cards: structured output drawn as elements ---------- */
function scoreTone(score) { return score >= 70 ? 'good' : score >= 40 ? 'mid' : 'low'; }

function renderCard(c, run) {
  switch (c.kind) {
    case 'phase': break;
    case 'plan': T.plan = planCard(c.plan, run); addMsg('card', T.plan.el); break;
    case 'progress': progressCard(c.items); break;
    case 'guide':
      T.progress = null;
      addMsg('card', h('div', { class: 'rich rich-guide' },
        h('div', { class: 'rich-eyebrow' }, 'Guía lista'),
        h('h3', {}, c.topic),
        h('div', { class: 'actions' },
          h('button', { class: 'btn small blue', type: 'button', onclick: () => openNote(c.slug) }, 'Leer la guía'),
          h('button', { class: 'btn small yellow', type: 'button', onclick: async () => { await openNote(c.slug, { quiet: true }); setTab('concepts'); revealPanel(); } }, 'Ver conceptos'))));
      if (!run.replay) loadGraph().catch(() => {}).then(() => openNote(c.slug));
      break;
    case 'concept':
      addMsg('card', h('div', { class: 'rich' }, h('div', { class: 'rich-eyebrow' }, 'Nota de concepto'), h('h3', {}, c.topic),
        h('button', { class: 'btn small', type: 'button', onclick: () => openNote(c.slug) }, 'Abrir la nota')));
      if (!run.replay) loadGraph().catch(() => {});
      break;
    case 'daily':
      addMsg('card', h('div', { class: 'rich rich-daily' },
        h('div', { class: 'rich-eyebrow' }, 'Para descubrir hoy', c.field && h('span', { class: 'chip' }, c.field)),
        h('h3', {}, c.topic), c.hook && h('p', {}, c.hook)));
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

// The plan of concept notes: a tree to prune or extend before anything is written.
function planCard(p, run) {
  const levels = [];
  for (const c of p.concepts || []) (levels[c.level - 1] ||= []).push(c);
  const boxes = new Map();
  const added = [];
  const addList = h('ul', { class: 'plan-added' });
  const addInput = h('input', { type: 'text', maxlength: 80, placeholder: 'Añadir un concepto…', 'aria-label': 'Añadir un concepto' });
  const add = () => {
    const t = addInput.value.trim();
    if (!t) return;
    added.push(t);
    addList.append(h('li', {}, t));
    addInput.value = '';
    update();
  };
  addInput.addEventListener('keydown', (e) => { if (e.key === 'Enter') { e.preventDefault(); add(); } });
  const count = h('p', { class: 'hint' });
  const go = h('button', { class: 'btn green', type: 'button' });
  const fresh = (p.concepts || []).filter((c) => !c.exists);
  function update() {
    const kept = fresh.filter((c) => boxes.get(c.id).checked).length + added.length;
    go.textContent = kept ? `Escribir la guía y ${kept} ${kept === 1 ? 'nota' : 'notas'}` : 'Escribir solo la guía';
    const linked = (p.concepts || []).filter((c) => c.exists && boxes.get(c.id).checked).length;
    count.textContent = linked ? `${linked} ya ${linked === 1 ? 'está' : 'están'} en tu vault: se enlazan, no se reescriben.` : '';
  }
  const el = h('div', { class: 'rich rich-plan' },
    h('div', { class: 'rich-eyebrow' }, `Plan de conceptos · profundidad ${p.depth}`),
    h('h3', {}, p.topic),
    h('p', { class: 'hint' }, 'Desmarca lo que ya sabes o no te interesa: lo que dependía solo de eso también se va.'),
    h('ol', { class: 'plan-levels' }, levels.map((lv, i) => lv && h('li', {},
      h('span', { class: 'plan-level' }, i === 0 ? 'Directo' : `Nivel ${i + 1}`),
      h('ul', {}, lv.map((c) => {
        const cb = h('input', { type: 'checkbox', checked: true });
        cb.addEventListener('change', update);
        boxes.set(c.id, cb);
        return h('li', {}, h('label', { class: `plan-item${c.exists ? ' exists' : ''}` }, cb,
          h('span', {}, h('strong', {}, c.title), c.exists ? h('em', {}, ' · ya en tu vault') : null, c.summary ? h('small', {}, c.summary) : null)));
      }))))),
    addList,
    h('div', { class: 'plan-add' }, addInput, h('button', { class: 'btn small', type: 'button', onclick: add }, 'Añadir')),
    h('div', { class: 'actions' }, go,
      h('button', { class: 'btn', type: 'button', onclick: () => answer('g', 'Solo la guía') }, 'Solo la guía'),
      h('button', { class: 'btn small', type: 'button', onclick: () => answer('n', 'Cancelar') }, 'Cancelar')),
    count);
  const state = { el, live: false, answered: false };
  function answer(reply, label) {
    if (state.answered || S.run !== run || run.done || !state.live) return;
    state.answered = true;
    $$('button, input', el).forEach((x) => { x.disabled = true; });
    T.echo = { reply, label };
    sendReply(reply);
  }
  go.addEventListener('click', () => {
    const skip = [...boxes].filter(([, cb]) => !cb.checked).map(([id]) => id);
    if (!skip.length && !added.length) { answer('s', go.textContent); return; }
    answer(JSON.stringify({ skip, add: added, go: true }), go.textContent);
  });
  update();
  if (run.replay) $$('button, input', el).forEach((x) => { x.disabled = true; });
  return state;
}

// One card per guide being written, redrawn in place as each note finishes.
function progressCard(items) {
  const state = { pending: 'en cola', working: 'escribiendo…', done: 'lista', linked: 'ya estaba', error: 'falló' };
  const done = items.filter((i) => i.state !== 'pending' && i.state !== 'working').length;
  const body = [
    h('div', { class: 'rich-eyebrow' }, `Escribiendo · ${done} de ${items.length}`),
    h('div', { class: 'bar' }, h('i', { style: `width:${Math.round((100 * done) / items.length)}%` })),
    h('ul', { class: 'progress-list' }, items.map((it) => h('li', { 'data-state': it.state, class: it.kind },
      h('span', { class: 'p-dot', 'aria-hidden': 'true' }),
      h('span', { class: 'p-title' }, it.title, it.kind === 'guide' ? h('small', {}, ' · guía') : null),
      h('small', { class: 'p-state' }, state[it.state] || it.state))))];
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
  setComposer('busy');
  setWorking(!replay, 'Pensando…');
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
      if (!run.replay) setWorking(true, 'Pensando…');
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
  if (!list.length) { ul.append(h('li', { class: 'hint' }, S.chats.length ? 'Ningún chat coincide.' : 'Tus chats aparecerán aquí.')); return; }
  for (const r of list) {
    const node = r.slug && nodeOf(r.slug);
    ul.append(h('li', { class: `chat-item${S.run && S.run.id === r.id ? ' on' : ''}`, 'data-id': r.id, style: `--lc: ${node ? lineColor(node.group) : 'var(--line)'}` },
      h('button', { class: 'ci-open', type: 'button', onclick: () => openChat(r) },
        h('span', { class: 'ci-title' }, r.title || 'Chat'),
        h('span', { class: 'ci-meta' }, r.status === 'running' ? 'En curso' : when(r.started), node ? ` · ${node.label}` : '')),
      r.status !== 'running' && h('button', { class: 'ci-del', type: 'button', 'aria-label': `Eliminar «${r.title}»`, title: 'Eliminar', onclick: async () => {
        if (!confirm(`¿Eliminar el chat «${r.title}»? Tus notas se conservan.`)) return;
        try {
          await api(`/api/sessions/${r.id}`, { method: 'DELETE' });
          if (S.run && S.run.id === r.id) newChat();
          loadChats();
        } catch (err) { toast(err.message); }
      } }, '✕')));
  }
}
$('#chat-search').addEventListener('input', renderChats);
function markActiveChat() { $$('.chat-item').forEach((li) => li.classList.toggle('on', !!S.run && String(S.run.id) === li.dataset.id)); }

function openChat(r) {
  attachRun(r.id, r.title, { replay: r.status !== 'running' });
  if (r.slug && r.slug !== S.guide && nodeOf(r.slug)) openNote(r.slug, { reveal: false });
  if (isPhone()) setPane('chat');
}

function newChat() {
  if (S.run && S.run.es) S.run.es.close();
  S.run = null;
  resetTranscript();
  $('#transcript').append(emptyState());
  $('#chat-title').textContent = 'Nuevo chat';
  setWorking(false);
  setComposer('chat');
  markActiveChat();
  if (isPhone()) setPane('chat');
}
$('#btn-new').addEventListener('click', newChat);
$('#brand').addEventListener('click', (e) => { e.preventDefault(); newChat(); });

/* ---------- boot ---------- */
async function refresh() {
  await loadGraph().catch((err) => toast(err.message));
  if (S.guide) await reloadGuide(); else renderReader(null);
  if (S.tab === 'concepts') renderConcepts();
  if (S.tab === 'practice') renderPractice();
  renderChats();
}

async function boot() {
  renderDepth();
  setLayout(store.get('layout', 'both'));
  setTab(S.tab);
  newChat();
  try {
    S.info = await api('/api/state');
    $('#provider-badge').textContent = S.info.provider;
    if (S.info.warning) { $('#banner').textContent = S.info.warning; $('#banner').hidden = false; }
    await refresh();
    await loadChats();
    if (S.info.activeRun) {
      const me = S.chats.find((r) => r.id === S.info.activeRun);
      attachRun(S.info.activeRun, me ? me.title : 'Chat');
    }
    // a link straight to a note: #open/<slug>[/<tab>]
    const m = /^#open\/([^/]+)(?:\/(guide|concepts|map|practice))?$/.exec(location.hash);
    if (m) {
      await openNote(decodeURIComponent(m[1]), { quiet: !!m[2] });
      if (m[2]) setTab(m[2]);
    }
  } catch (err) { toast(err.message); }
}
boot();
