'use strict';

/* ---------- interactive widgets: ```viz blocks inside notes ----------
 * The model writes a JSON spec; the Go side (internal/viz) has already checked it.
 * plot and transform2d draw with JSXGraph, loaded the first time a note needs it.
 * steps, sample and predict are plain DOM/SVG. */
const Viz = (() => {
  // the page's t() (i18n.js), under a name the token and time variables below do not hide
  const tr = (msg, vars) => window.t(msg, vars);
  /* ---------- expressions (same language as internal/viz/expr.go) ---------- */
  const FUNCS = {
    sin: Math.sin, cos: Math.cos, tan: Math.tan, asin: Math.asin, acos: Math.acos, atan: Math.atan,
    sinh: Math.sinh, cosh: Math.cosh, tanh: Math.tanh, exp: Math.exp, log: Math.log, ln: Math.log,
    log10: Math.log10, log2: Math.log2, sqrt: Math.sqrt, abs: Math.abs, floor: Math.floor, ceil: Math.ceil,
    round: Math.round, sign: Math.sign, min: Math.min, max: Math.max, atan2: Math.atan2, pow: Math.pow,
    mod: (a, b) => ((a % b) + b) % b,
  };
  const CONSTS = { pi: Math.PI, e: Math.E };

  function tokenize(s) {
    const out = [];
    const re = /\s*(?:(\d*\.?\d+(?:[eE][-+]?\d+)?)|([A-Za-z_][A-Za-z0-9_]*)|(\*\*|[-+*/^(),]))/y;
    let i = 0;
    while (i < s.length) {
      if (/\s/.test(s[i])) { i++; continue; }
      re.lastIndex = i;
      const m = re.exec(s);
      if (!m) throw new Error(tr('símbolo inesperado en «{text}»', { text: s }));
      out.push(m[1] !== undefined ? { num: parseFloat(m[1]) } : m[2] !== undefined ? { id: m[2] } : { op: m[3] === '**' ? '^' : m[3] });
      i = re.lastIndex;
    }
    return out;
  }

  // compile turns an expression into a function of an environment object ({x, a, b…}).
  function compile(src, names) {
    const toks = tokenize(String(src));
    let p = 0;
    const peek = () => toks[p];
    const isOp = (t, o) => t && t.op === o;
    const startsPrimary = (t) => t && (t.num !== undefined || t.id !== undefined || t.op === '(');
    function sum() {
      let f = product();
      while (isOp(peek(), '+') || isOp(peek(), '-')) {
        const o = toks[p++].op, a = f, b = product();
        f = o === '+' ? (v) => a(v) + b(v) : (v) => a(v) - b(v);
      }
      return f;
    }
    function product() {
      let f = unary();
      for (;;) {
        const t = peek();
        let o;
        if (isOp(t, '*') || isOp(t, '/')) { o = t.op; p++; } else if (startsPrimary(t)) o = '*'; else return f;
        const a = f, b = unary();
        f = o === '*' ? (v) => a(v) * b(v) : (v) => a(v) / b(v);
      }
    }
    function unary() {
      if (isOp(peek(), '-')) { p++; const a = unary(); return (v) => -a(v); }
      if (isOp(peek(), '+')) { p++; return unary(); }
      return power();
    }
    function power() {
      const a = primary();
      if (isOp(peek(), '^')) { p++; const b = unary(); return (v) => Math.pow(a(v), b(v)); }
      return a;
    }
    function primary() {
      const t = toks[p++];
      if (!t) throw new Error(tr('la expresión termina antes de tiempo'));
      if (t.num !== undefined) return () => t.num;
      if (isOp(t, '(')) {
        const a = sum();
        if (!isOp(toks[p++], ')')) throw new Error('falta )');
        return a;
      }
      if (t.id !== undefined) {
        if (FUNCS[t.id]) {
          if (!isOp(toks[p++], '(')) throw new Error(tr('{name} necesita paréntesis', { name: t.id }));
          const args = [sum()];
          while (isOp(peek(), ',')) { p++; args.push(sum()); }
          if (!isOp(toks[p++], ')')) throw new Error(`falta ) en ${t.id}`);
          const fn = FUNCS[t.id];
          return (v) => fn(...args.map((a) => a(v)));
        }
        if (CONSTS[t.id] !== undefined) return () => CONSTS[t.id];
        if (names.includes(t.id)) return (v) => v[t.id];
        throw new Error(tr('variable desconocida «{name}»', { name: t.id }));
      }
      throw new Error(tr('símbolo inesperado'));
    }
    const f = sum();
    if (p !== toks.length) throw new Error(tr('sobran símbolos'));
    return f;
  }

  /* ---------- shared pieces ---------- */
  const cssVar = (n) => getComputedStyle(document.documentElement).getPropertyValue(n).trim();
  const PALETTE = ['--l1', '--l2', '--l3', '--l0', '--l5', '--l6'];
  const colorAt = (i) => cssVar(PALETTE[i % PALETTE.length]);
  const fmt = (x) => {
    if (!Number.isFinite(x)) return '—';
    const a = Math.abs(x);
    if (a !== 0 && (a < 1e-3 || a >= 1e5)) return x.toExponential(2);
    return String(Math.round(x * 1000) / 1000);
  };
  const inline = (src) => {
    const span = h('span', { class: 'viz-md' });
    span.innerHTML = renderMd(String(src || '')).replace(/^<p>|<\/p>\s*$/g, '');
    return span;
  };
  const reducedMotion = () => matchMedia('(prefers-reduced-motion: reduce)').matches;

  function frame(spec, kind) {
    const body = h('div', { class: 'viz-body' });
    const controls = h('div', { class: 'viz-controls' });
    const fig = h('figure', { class: `viz viz-${kind}` },
      h('div', { class: 'viz-head' }, h('span', { class: 'viz-tag' }, KIND_LABEL[kind] || tr('Interactivo')), spec.title ? h('strong', {}, inline(spec.title)) : null),
      body, controls,
      spec.caption ? h('figcaption', {}, inline(spec.caption)) : null);
    return { fig, body, controls };
  }
  const KIND_LABEL = { plot: tr('Mueve los controles'), transform2d: tr('Transformación'), steps: tr('Paso a paso'), sample: tr('Experimento'), predict: tr('Predice'), html: tr('Interactivo') };

  // A slider styled as a metro line with a station for a thumb.
  function slider(p, value, onInput) {
    const out = h('output', {}, fmt(value));
    const input = h('input', { type: 'range', min: p.min, max: p.max, step: p.step || (p.max - p.min) / 100, value, 'aria-label': p.label || p.name });
    input.addEventListener('input', () => { out.textContent = fmt(+input.value); onInput(+input.value); });
    return { el: h('label', { class: 'viz-slider' }, h('span', { class: 'viz-slider-label' }, inline(p.label || p.name)), input, out), input, out };
  }

  // JSXGraph is large, so it loads the first time a note has a plot or a transformation.
  let jxgReady = null;
  function loadJXG() {
    jxgReady ??= new Promise((resolve, reject) => {
      const css = document.createElement('link');
      css.rel = 'stylesheet';
      css.href = 'vendor/jsxgraph/jsxgraph.css';
      document.head.append(css);
      const s = document.createElement('script');
      s.src = 'vendor/jsxgraph/jsxgraphcore.js';
      s.onload = () => resolve(window.JXG);
      s.onerror = () => { jxgReady = null; reject(new Error('jsxgraph')); };
      document.head.append(s);
    });
    return jxgReady;
  }

  let boardSeq = 0;
  // whenVisible draws a widget the first time it scrolls into view: a long guide with several boards stays light.
  function whenVisible(el, draw) {
    if (!('IntersectionObserver' in window)) { requestAnimationFrame(draw); return; }
    const io = new IntersectionObserver((entries) => {
      if (entries.some((e) => e.isIntersecting)) { io.disconnect(); draw(); }
    }, { rootMargin: '200px' });
    io.observe(el);
  }

  function makeBoard(JXG, box, bbox, opts = {}) {
    box.id = `jxg${++boardSeq}`;
    const ink = cssVar('--ink'), muted = cssVar('--muted'), line = cssVar('--line');
    return JXG.JSXGraph.initBoard(box.id, {
      boundingbox: bbox, axis: true, showCopyright: false, showNavigation: false, keepAspectRatio: !!opts.square,
      pan: { enabled: true, needTwoFingers: true }, zoom: { enabled: false },
      defaultAxes: {
        x: { strokeColor: ink, strokeWidth: 1.5, ticks: { strokeColor: line, majorHeight: 8, minorTicks: 0, label: { strokeColor: muted, fontSize: 11 } } },
        y: { strokeColor: ink, strokeWidth: 1.5, ticks: { strokeColor: line, majorHeight: 8, minorTicks: 0, label: { strokeColor: muted, fontSize: 11 } } },
      },
      grid: !!opts.grid,
    });
  }

  function failBox(body, err) {
    body.replaceChildren(h('p', { class: 'hint' }, `No pude dibujar este visual (${err.message}).`));
  }

  function readoutsBox(list, env) {
    const items = list.map((r) => ({ f: compile(r.expr, Object.keys(env)), val: h('strong', {}) }));
    const box = h('div', { class: 'viz-readouts' }, list.map((r, i) => h('span', {}, inline(r.label), ' = ', items[i].val)));
    const update = (v) => items.forEach((it) => { it.val.textContent = fmt(it.f(v)); });
    return { box, update };
  }

  /* ---------- plot: functions of x with sliders ---------- */
  function plot(spec) {
    const { fig, body, controls } = frame(spec, 'plot');
    const env = { x: 0 };
    for (const p of spec.params || []) env[p.name] = p.value;
    const names = Object.keys(env);
    const box = h('div', { class: 'viz-board' });
    body.append(box);
    const legend = h('div', { class: 'viz-legend' }, (spec.curves || []).map((c, i) =>
      h('span', { style: `--c:${colorAt(i)}` }, h('i'), inline(c.label || c.expr))));
    if ((spec.curves || []).length) body.append(legend);
    const ro = readoutsBox(spec.readouts || [], env);
    let board = null;
    const refresh = () => { ro.update(env); if (board) board.update(); };
    for (const p of spec.params || []) controls.append(slider(p, p.value, (v) => { env[p.name] = v; refresh(); }).el);
    if ((spec.readouts || []).length) controls.append(ro.box);
    ro.update(env);

    whenVisible(box, async () => {
      try {
        const JXG = await loadJXG();
        const [x0, x1] = spec.x || [-5, 5], [y0, y1] = spec.y || [-5, 5];
        board = makeBoard(JXG, box, [x0, y1, x1, y0], { grid: false });
        (spec.curves || []).forEach((c, i) => {
          const f = compile(c.expr, names);
          // a curve with both coordinates as functions: functiongraph would compile its "x" term with
          // new Function, which the page's content security policy blocks
          board.create('curve', [(t) => t, (t) => f({ ...env, x: t }), x0, x1], { strokeColor: colorAt(i), strokeWidth: 3, highlight: false, numberPointsHigh: 1600 });
        });
        (spec.points || []).forEach((pt, i) => {
          const fx = compile(pt.x, names), fy = compile(pt.y, names);
          board.create('point', [() => fx(env), () => fy(env)], {
            name: pt.label || '', fixed: true, size: 4, strokeColor: cssVar('--ink'), fillColor: colorAt(i + 1), highlight: false,
            label: { strokeColor: cssVar('--ink'), offset: [8, 8] },
          });
        });
      } catch (err) { failBox(body, err); }
    });
    return fig;
  }

  /* ---------- transform2d: a matrix acting on the plane ---------- */
  function transform2d(spec) {
    const { fig, body, controls } = frame(spec, 'transform2d');
    const env = {};
    for (const p of spec.params || []) env[p.name] = p.value;
    const names = Object.keys(env);
    const cells = spec.matrix.map((row) => row.map((c) => compile(String(c), names)));
    let t = 0;
    const M = () => {
      const [[a, b], [c, d]] = cells.map((row) => row.map((f) => f(env)));
      return [[1 + t * (a - 1), t * b], [t * c, 1 + t * (d - 1)]];
    };
    const apply = (m, x, y) => [m[0][0] * x + m[0][1] * y, m[1][0] * x + m[1][1] * y];
    const box = h('div', { class: 'viz-board square' });
    body.append(box);

    const det = h('strong', {});
    const matrixView = h('span', { class: 'viz-matrix' });
    const ro = readoutsBox(spec.readouts || [], env);
    let board = null;
    const refresh = () => {
      const [[a, b], [c, d]] = cells.map((row) => row.map((f) => f(env)));
      matrixView.innerHTML = katex.renderToString(`\\begin{bmatrix}${fmt(a)} & ${fmt(b)} \\\\ ${fmt(c)} & ${fmt(d)}\\end{bmatrix}`, { throwOnError: false });
      det.textContent = fmt(a * d - b * c);
      ro.update(env);
      if (board) board.update();
    };
    for (const p of spec.params || []) controls.append(slider(p, p.value, (v) => { env[p.name] = v; refresh(); }).el);
    const tSlider = slider({ name: 't', label: tr('Cuánto se aplica'), min: 0, max: 1, step: 0.01 }, 0, (v) => { t = v; refresh(); });
    let anim = 0;
    const play = h('button', { class: 'btn small green', type: 'button', onclick: () => {
      cancelAnimationFrame(anim);
      const from = t >= 0.999 ? 0 : t, start = performance.now(), dur = reducedMotion() ? 1 : 1400 * (1 - from);
      const step = (now) => {
        const k = Math.min(1, (now - start) / dur);
        const s = k * k * (3 - 2 * k); // smoothstep, the ease 3Blue1Brown's animations use
        t = from + (1 - from) * s;
        tSlider.input.value = t; tSlider.out.textContent = fmt(t);
        refresh();
        if (k < 1) anim = requestAnimationFrame(step);
      };
      anim = requestAnimationFrame(step);
    } }, tr('Aplicar'));
    controls.append(h('div', { class: 'viz-row' }, play, tSlider.el),
      h('div', { class: 'viz-readouts' }, h('span', {}, tr('Matriz '), matrixView), h('span', {}, tr('Determinante (área del cuadrado amarillo) = '), det)),
      ...((spec.readouts || []).length ? [ro.box] : []));
    refresh();

    whenVisible(box, async () => {
      try {
        const JXG = await loadJXG();
        const R = 4;
        board = makeBoard(JXG, box, [-R, R, R, -R], { square: true, grid: false });
        const faint = cssVar('--line'), gridC = cssVar('--l1');
        const seg = (p, q, attrs) => board.create('segment', [
          [() => apply(M(), ...p)[0], () => apply(M(), ...p)[1]],
          [() => apply(M(), ...q)[0], () => apply(M(), ...q)[1]]], { fixed: true, highlight: false, ...attrs });
        const G = 2 * R;
        for (let k = -G; k <= G; k++) {
          board.create('segment', [[k, -G], [k, G]], { strokeColor: faint, strokeWidth: 1, fixed: true, highlight: false });
          board.create('segment', [[-G, k], [G, k]], { strokeColor: faint, strokeWidth: 1, fixed: true, highlight: false });
          seg([k, -G], [k, G], { strokeColor: gridC, strokeWidth: k === 0 ? 2.5 : 1.2, strokeOpacity: 0.75 });
          seg([-G, k], [G, k], { strokeColor: gridC, strokeWidth: k === 0 ? 2.5 : 1.2, strokeOpacity: 0.75 });
        }
        const corner = (x, y) => [() => apply(M(), x, y)[0], () => apply(M(), x, y)[1]];
        board.create('polygon', [corner(0, 0), corner(1, 0), corner(1, 1), corner(0, 1)], {
          fillColor: cssVar('--l2'), fillOpacity: 0.35, borders: { strokeWidth: 0 }, vertices: { visible: false }, fixed: true, highlight: false,
        });
        const arrow = (x, y, color, name) => board.create('arrow', [[0, 0], corner(x, y)], {
          strokeColor: color, strokeWidth: 4, fixed: true, highlight: false, lastArrow: { type: 2, size: 5 },
          name, withLabel: !!name, label: { strokeColor: color, fontSize: 14, offset: [6, 6] },
        });
        arrow(1, 0, cssVar('--l3'), 'î');
        arrow(0, 1, cssVar('--l0'), 'ĵ');
        (spec.vectors || []).forEach((v, i) => arrow(v.x, v.y, i ? cssVar('--l5') : cssVar('--l4'), v.label || ''));
      } catch (err) { failBox(body, err); }
    });
    return fig;
  }

  /* ---------- steps: a process frame by frame ---------- */
  function steps(spec) {
    const { fig, body, controls } = frame(spec, 'steps');
    const frames = spec.frames || [];
    let i = 0, timer = 0;
    const stage = h('div', { class: 'viz-stage', 'aria-live': 'polite' });
    const counter = h('span', { class: 'viz-counter' });
    const dots = h('div', { class: 'viz-dots' }, frames.map((_, k) => h('button', { type: 'button', 'aria-label': `Paso ${k + 1}`, onclick: () => { stop(); go(k); } })));
    body.append(stage);

    function arrayView(f) {
      const hl = new Set(f.highlight || []);
      const ptr = {};
      for (const [name, idx] of Object.entries(f.pointers || {})) (ptr[idx] = ptr[idx] || []).push(name);
      return h('div', { class: 'viz-array' }, f.array.map((v, k) => h('div', { class: `cell${hl.has(k) ? ' on' : ''}` },
        h('span', { class: 'val' }, String(v)), h('small', { class: 'idx' }, String(k)),
        ptr[k] ? h('span', { class: 'ptr' }, ptr[k].join(' ')) : null)));
    }
    function tableView(f) {
      const hl = new Set(f.highlight || []);
      return h('table', { class: 'viz-table' },
        f.table.head ? h('thead', {}, h('tr', {}, f.table.head.map((c) => h('th', {}, inline(c))))) : null,
        h('tbody', {}, f.table.rows.map((r, k) => h('tr', { class: hl.has(k) ? 'on' : null }, r.map((c) => h('td', {}, inline(c)))))));
    }
    function go(k) {
      i = Math.max(0, Math.min(frames.length - 1, k));
      const f = frames[i];
      const parts = [];
      if (f.text) parts.push(h('div', { class: 'viz-text' }, inline(f.text)));
      if (f.formula) {
        const d = h('div', { class: 'viz-formula' });
        d.innerHTML = katex.renderToString(f.formula, { displayMode: true, throwOnError: false });
        parts.push(d);
      }
      if (f.array) parts.push(arrayView(f));
      if (f.table) parts.push(tableView(f));
      stage.replaceChildren(...parts);
      counter.textContent = `${i + 1} / ${frames.length}`;
      [...dots.children].forEach((d, k) => d.classList.toggle('on', k <= i));
      prev.disabled = i === 0;
      next.disabled = i === frames.length - 1;
    }
    function stop() { clearInterval(timer); timer = 0; playBtn.textContent = tr('Reproducir'); }
    const prev = h('button', { class: 'btn small', type: 'button', onclick: () => { stop(); go(i - 1); } }, tr('Anterior'));
    const next = h('button', { class: 'btn small', type: 'button', onclick: () => { stop(); go(i + 1); } }, tr('Siguiente'));
    const playBtn = h('button', { class: 'btn small green', type: 'button', onclick: () => {
      if (timer) { stop(); return; }
      if (i === frames.length - 1) go(0);
      playBtn.textContent = tr('Pausa');
      timer = setInterval(() => { if (i >= frames.length - 1) stop(); else go(i + 1); }, 2400);
    } }, tr('Reproducir'));
    controls.append(h('div', { class: 'viz-row' }, prev, playBtn, next, counter), dots);
    go(0);
    return fig;
  }

  /* ---------- sample: a random experiment repeated many times ---------- */
  function sample(spec) {
    const { fig, body, controls } = frame(spec, 'sample');
    const total = spec.outcomes.reduce((s, o) => s + o.p, 0);
    const outs = spec.outcomes.map((o) => ({ ...o, p: o.p / total }));
    const k = spec.draws || 1;
    const mean = spec.measure === 'mean';
    // exact distribution of one trial: key -> probability
    let dist;
    if (k === 1) dist = new Map(outs.map((o) => [o.label, o.p]));
    else {
      dist = new Map([[0, 1]]);
      for (let d = 0; d < k; d++) {
        const nd = new Map();
        for (const [s, ps] of dist) for (const o of outs) nd.set(s + o.value, (nd.get(s + o.value) || 0) + ps * o.p);
        dist = nd;
      }
      dist = new Map([...dist].sort((a, b) => a[0] - b[0]).map(([s, p]) => [mean ? Math.round((s / k) * 1e6) / 1e6 : Math.round(s * 1e6) / 1e6, p]));
    }
    const keys = [...dist.keys()];
    const counts = new Map(keys.map((key) => [key, 0]));
    const cum = [];
    outs.reduce((s, o) => { cum.push(s + o.p); return s + o.p; }, 0);
    const draw1 = () => { const r = Math.random(); const j = cum.findIndex((c) => r < c); return outs[j < 0 ? outs.length - 1 : j]; };
    let n = 0, sumV = 0;
    const trial = () => {
      if (k === 1) { const o = draw1(); counts.set(o.label, counts.get(o.label) + 1); if (o.value !== undefined) sumV += o.value; } else {
        let s = 0;
        for (let d = 0; d < k; d++) s += draw1().value;
        const key = Math.round((mean ? s / k : s) * 1e6) / 1e6;
        counts.set(key, (counts.get(key) || 0) + 1);
        sumV += mean ? s / k : s;
      }
      n++;
    };
    const numeric = k > 1 || outs.every((o) => o.value !== undefined);
    const expected = numeric ? (k === 1 ? outs.reduce((s, o) => s + o.value * o.p, 0) : [...dist].reduce((s, [v, p]) => s + v * p, 0)) : NaN;

    const W = 560, H = 220, pad = 26;
    const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
    svg.setAttribute('viewBox', `0 0 ${W} ${H + pad}`);
    svg.setAttribute('class', 'viz-hist');
    svg.setAttribute('role', 'img');
    body.append(svg);
    const stats = h('div', { class: 'viz-readouts' });
    const ns = (tag, attrs) => { const el = document.createElementNS('http://www.w3.org/2000/svg', tag); for (const [a, v] of Object.entries(attrs)) el.setAttribute(a, v); return el; };
    function render() {
      const bw = W / keys.length;
      const maxP = Math.max(...keys.map((key) => Math.max(dist.get(key), n ? counts.get(key) / n : 0)));
      const y = (p) => H - (p / (maxP || 1)) * (H - 10);
      svg.replaceChildren();
      const labelEvery = Math.ceil(keys.length / 16);
      keys.forEach((key, j) => {
        const x = j * bw;
        const obs = n ? counts.get(key) / n : 0;
        svg.append(ns('rect', { x: x + bw * 0.12, y: y(obs), width: bw * 0.76, height: H - y(obs), fill: cssVar('--l1') }));
        svg.append(ns('line', { x1: x + bw * 0.05, x2: x + bw * 0.95, y1: y(dist.get(key)), y2: y(dist.get(key)), stroke: cssVar('--l0'), 'stroke-width': 3 }));
        if (j % labelEvery === 0) {
          const t = ns('text', { x: x + bw / 2, y: H + 17, 'text-anchor': 'middle', 'font-size': 12, fill: cssVar('--muted') });
          t.textContent = typeof key === 'number' ? fmt(key) : key;
          svg.append(t);
        }
      });
      svg.append(ns('line', { x1: 0, x2: W, y1: H, y2: H, stroke: cssVar('--ink'), 'stroke-width': 2 }));
      stats.replaceChildren(
        h('span', {}, tr('Repeticiones: '), h('strong', {}, String(n))),
        numeric ? h('span', {}, tr('Promedio observado: '), h('strong', {}, n ? fmt(sumV / n) : '—'), ' ' + tr('(teórico {value})', { value: fmt(expected) })) : null,
        h('span', { class: 'viz-key' }, h('i', { style: `background:${cssVar('--l1')}` }), 'observado', h('i', { class: 'line', style: `background:${cssVar('--l0')}` }), 'exacto'));
    }
    let anim = 0;
    const run = (count) => {
      cancelAnimationFrame(anim);
      if (reducedMotion() || count <= 10) { for (let j = 0; j < count; j++) trial(); render(); return; }
      let left = count;
      const per = Math.max(1, Math.ceil(count / 40));
      const tick = () => { for (let j = 0; j < per && left > 0; j++, left--) trial(); render(); if (left > 0) anim = requestAnimationFrame(tick); };
      anim = requestAnimationFrame(tick);
    };
    controls.append(h('div', { class: 'viz-row' },
      [1, 10, 100, 1000].map((c) => h('button', { class: `btn small${c === 100 ? ' green' : ''}`, type: 'button', onclick: () => run(c) }, `+${c}`)),
      h('button', { class: 'btn small', type: 'button', onclick: () => { cancelAnimationFrame(anim); n = 0; sumV = 0; for (const key of keys) counts.set(key, 0); render(); } }, tr('Reiniciar'))), stats);
    render();
    return fig;
  }

  /* ---------- predict: guess before reading on ---------- */
  function predict(spec) {
    const { fig, body } = frame(spec, 'predict');
    const reveal = h('div', { class: 'viz-reveal', hidden: true });
    const buttons = spec.options.map((o, j) => h('button', { type: 'button', class: 'choice', onclick: () => {
      buttons.forEach((b, k) => { b.disabled = true; b.classList.toggle('right', k === spec.answer); });
      if (j !== spec.answer) buttons[j].classList.add('picked');
      reveal.replaceChildren(
        h('strong', {}, j === spec.answer ? tr('Bien visto.') : tr('No era esa, y es una intuición muy común.')),
        spec.explain ? mdBlock(spec.explain) : null);
      reveal.hidden = false;
    } }, inline(o)));
    body.append(h('p', { class: 'q-text' }, inline(spec.question)), h('div', { class: 'choices' }, buttons), reveal);
    return fig;
  }

  /* ---------- html: a page built on demand ---------- */
  function currentTheme(ctx) {
    if (ctx && typeof ctx.theme === 'function') return ctx.theme();
    return document.documentElement.dataset.theme || (matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light');
  }
  function frameURL(src, theme, bust) {
    const clean = String(src || '').replace(/^\/+/, '');
    const q = new URLSearchParams();
    if (theme === 'dark' || theme === 'light') q.set('theme', theme);
    if (bust) q.set('v', String(bust));
    const qs = q.toString();
    return `/${clean}${qs ? '?' + qs : ''}`;
  }

  function html(spec, ctx) {
    const { fig, body, controls } = frame(spec, 'html');
    const head = fig.querySelector('.viz-head');
    const actions = h('div', { class: 'viz-actions' });
    head.append(actions);
    const statusBar = h('div', { class: 'viz-status', hidden: true });

    let currentSrc = spec.src || '';
    let iframe = null;
    let autoRetried = false;
    let busy = false;
    let onMsg = null;

    function detachMsg() {
      if (onMsg) { window.removeEventListener('message', onMsg); onMsg = null; }
    }
    function setStatus(text, { err = false, retry = null } = {}) {
      if (!text) { statusBar.hidden = true; statusBar.replaceChildren(); return; }
      statusBar.className = `viz-status${err ? ' err' : ''}`;
      statusBar.replaceChildren(
        h('span', {}, text),
        retry ? h('button', { class: 'btn small', type: 'button', onclick: retry }, tr('Reintentar')) : null,
      );
      statusBar.hidden = false;
    }

    async function rebuild({ force = false, fixError = '' } = {}) {
      if (busy || !ctx) return;
      const fn = fixError ? (ctx.fix || ctx.regenerate) : (force ? ctx.regenerate : ctx.generate);
      if (!fn) return;
      busy = true;
      setStatus(fixError
        ? tr('Detecté un error en el visual; intentando corregirlo…')
        : tr('Construyendo el visual, puede tardar un par de minutos…'));
      try {
        const nextSrc = await fn(spec, fixError);
        spec.src = nextSrc;
        show(nextSrc, Date.now());
      } catch (err) {
        setStatus(err.message || tr('No pude corregir el visual.'), { err: true, retry: () => rebuild({ force: true }) });
      } finally {
        busy = false;
      }
    }

    function show(src, bust) {
      detachMsg();
      currentSrc = src;
      setStatus('');
      iframe = h('iframe', {
        class: 'viz-frame',
        src: frameURL(src, currentTheme(ctx), bust),
        sandbox: 'allow-scripts',
        title: spec.title || tr('Visual interactivo'),
        referrerpolicy: 'no-referrer',
        loading: 'lazy',
      });
      body.replaceChildren(iframe, statusBar);
      controls.replaceChildren();

      actions.replaceChildren(
        ctx && ctx.expand ? h('button', {
          class: 'icon-btn', type: 'button', title: tr('Abrir en grande'), 'aria-label': tr('Abrir en grande'),
          onclick: () => ctx.expand(currentSrc, spec.title || tr('Visual interactivo')),
        }, icon('maximize-2')) : null,
        ctx && ctx.regenerate ? h('button', {
          class: 'icon-btn', type: 'button', title: tr('Regenerar visual'), 'aria-label': tr('Regenerar visual'),
          onclick: () => { autoRetried = false; rebuild({ force: true }); },
        }, icon('refresh-cw')) : null,
      );

      onMsg = (e) => {
        if (!iframe || !iframe.isConnected) { detachMsg(); return; }
        if (e.source !== iframe.contentWindow || !e.data || typeof e.data !== 'object') return;
        if (e.data.type === 'sh-viz-height' && Number.isFinite(e.data.height)) {
          const px = Math.max(260, Math.min(760, Math.ceil(e.data.height) + 8));
          iframe.style.height = `${px}px`;
        } else if (e.data.type === 'sh-viz-error' && e.data.message) {
          if (!autoRetried && ctx && ctx.fix) {
            autoRetried = true;
            rebuild({ fixError: String(e.data.message) });
          } else {
            setStatus(tr('El visual tuvo un error al ejecutarse.'), { err: true, retry: () => rebuild({ fixError: String(e.data.message) }) });
          }
        }
      };
      window.addEventListener('message', onMsg);
    }

    if (spec.src) { show(spec.src); return fig; }
    body.append(h('p', {}, inline(spec.brief)), statusBar);
    if (ctx && ctx.generate) {
      const btn = h('button', { class: 'btn small green', type: 'button', onclick: async () => {
        btn.disabled = true;
        const wait = h('span', { class: 'hint' }, tr('Construyendo el visual, puede tardar un par de minutos…'));
        controls.append(wait);
        try {
          const src = await ctx.generate(spec);
          spec.src = src;
          show(src);
        } catch (err) { wait.remove(); btn.disabled = false; toast(err.message); }
      } }, tr('Construir este visual'));
      controls.append(btn);
    }
    return fig;
  }

  function syncTheme(theme) {
    for (const f of document.querySelectorAll('iframe.viz-frame, #sim-frame')) {
      try { if (f.contentWindow) f.contentWindow.postMessage({ type: 'sh-theme', theme }, '*'); } catch { /* ignore */ }
    }
  }

  const BUILD = { plot, transform2d, steps, sample, predict, html };

  // A block written straight into a chat answer has not been checked on the server: a LaTeX
  // command with a single backslash ("\sqrt") would make it invalid JSON, so try once more with it doubled.
  function parseSpec(text) {
    try { return JSON.parse(text); } catch (err) {
      try { return JSON.parse(text.replace(/\\(?!["\\/bfnrtu])/g, '\\\\')); } catch { throw err; }
    }
  }

  // enhance replaces every ```viz code block under root with its widget.
  function enhance(root, ctx) {
    for (const code of root.querySelectorAll('pre > code.language-viz')) {
      let el;
      try {
        const spec = parseSpec(code.textContent);
        const build = BUILD[spec.type];
        if (!build) throw new Error(tr('tipo «{type}» desconocido', { type: spec.type }));
        el = build(spec, ctx);
      } catch (err) {
        el = h('p', { class: 'hint' }, `No pude mostrar un visual: ${err.message}`);
      }
      code.parentElement.replaceWith(el);
    }
  }

  return { enhance, compile, syncTheme };
})();
