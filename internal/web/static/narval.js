'use strict';

/* ---------- StudyHomie Narval mascot (pure SVG, 12 moods, themeable & animated) ----------
 * Recreates the lavender narval concept art in clean vector layers so each emotion swaps
 * eyes, mouth, flippers and floating accents without extra image assets. */
const Narval = (window.Narval = (() => {
  const NS = 'http://www.w3.org/2000/svg';
  const MOODS = [
    'normal', 'feliz', 'triste', 'enojado',
    'dormido', 'sorprendido', 'estudiando', 'concentrado',
    'celebrando', 'preocupado', 'mirando', 'desanimado',
  ];

  function el(tag, attrs = {}, children = []) {
    const node = document.createElementNS(NS, tag);
    for (const [k, v] of Object.entries(attrs)) {
      if (v != null && v !== false) node.setAttribute(k, String(v));
    }
    for (const c of children) if (c) node.append(c);
    return node;
  }

  function bursts(x, y, flip) {
    const g = el('g', { class: 'nv-fx nv-burst', transform: flip ? `translate(${x},${y}) scale(-1,1)` : `translate(${x},${y})` });
    g.append(
      el('path', { d: 'M0,0 L-10,-8', 'stroke-width': 4.5 }),
      el('path', { d: 'M-2,10 L-13,8', 'stroke-width': 4.5 }),
      el('path', { d: 'M8,-8 L4,-18', 'stroke-width': 4.5 }),
    );
    return g;
  }

  function faceOf(mood) {
    const g = el('g', { class: 'nv-face' });
    const eye = (cx, cy, r = 4.2) => el('circle', { class: 'nv-eye nv-ink', cx, cy, r });
    const stroke = (d, w = 3) => el('path', { class: 'nv-ink-s', d, 'stroke-width': w });

    switch (mood) {
      case 'feliz':
        g.append(
          stroke('M72,62 Q77,54 82,62', 3.2),
          stroke('M95,60 Q100,52 105,60', 3.2),
          el('path', { class: 'nv-ink', d: 'M84,63 Q89,62 94,63 Q93,72 89,72 Q85,72 84,63 Z' }),
          el('path', { class: 'nv-tongue', d: 'M86,68 Q89,66.5 92,68 Q91,71.5 89,71.5 Q87,71.5 86,68 Z' }),
        );
        break;
      case 'celebrando':
        g.append(
          stroke('M71,57 L79,62 L71,67', 3.2),
          stroke('M105,55 L97,60 L105,65', 3.2),
          el('path', { class: 'nv-ink', d: 'M82,63 Q89,61 95,63 Q94,74 88.5,74 Q83,74 82,63 Z' }),
          el('path', { class: 'nv-tongue', d: 'M85,69.5 Q88.5,67.5 92,69.5 Q91,73.5 88.5,73.5 Q86,73.5 85,69.5 Z' }),
        );
        break;
      case 'triste':
        g.append(
          el('path', { class: 'nv-ink', d: 'M72,63 A4.5,4.5 0 0 0 81,61 L72,63 Z' }),
          el('path', { class: 'nv-ink', d: 'M95,60 A4.5,4.5 0 0 0 104,62 L95,60 Z' }),
          stroke('M71,60 L81,57', 2.4),
          stroke('M95,56 L105,59', 2.4),
          stroke('M84,71 Q89,66 93,71', 2.8),
        );
        break;
      case 'enojado':
        g.append(
          el('path', { class: 'nv-ink', d: 'M72,58 L82,62 A4.8,4.8 0 0 1 72,64 Z' }),
          el('path', { class: 'nv-ink', d: 'M105,57 L95,61 A4.8,4.8 0 0 0 105,63 Z' }),
          stroke('M84,69 Q89,66.5 94,69', 2.8),
        );
        break;
      case 'dormido':
        g.append(
          stroke('M72,64 Q77,70 82,64', 3),
          stroke('M95,63 Q100,69 105,63', 3),
          stroke('M86,70 Q89,71 92,70', 2.6),
        );
        break;
      case 'sorprendido':
        g.append(
          eye(76, 61, 4.4),
          eye(100, 59, 4.4),
          el('ellipse', { class: 'nv-ink', cx: 88.5, cy: 67.5, rx: 4.2, ry: 5 }),
          el('ellipse', { class: 'nv-tongue', cx: 88.5, cy: 69.2, rx: 2.3, ry: 2.2 }),
        );
        break;
      case 'estudiando':
        g.append(
          eye(77, 62, 4),
          eye(99, 60, 4),
          stroke('M86,66 Q88.5,68 91,66', 2.4),
        );
        break;
      case 'concentrado':
        g.append(
          el('path', { class: 'nv-ink', d: 'M72,58 L82,61.5 A4.5,4.5 0 0 1 72,64 Z' }),
          el('path', { class: 'nv-ink', d: 'M105,57 L95,60.5 A4.5,4.5 0 0 0 105,63 Z' }),
          stroke('M85,68.5 L92,68', 2.8),
        );
        break;
      case 'preocupado':
        g.append(
          eye(77, 63, 4),
          eye(99, 61, 4),
          stroke('M83,71 Q88.5,66.5 94,70.5', 2.8),
        );
        break;
      case 'mirando':
        g.append(
          eye(77, 60, 4.1),
          eye(99, 58, 4.1),
          el('ellipse', { class: 'nv-ink', cx: 88.5, cy: 67.5, rx: 3.6, ry: 2.8 }),
        );
        break;
      case 'desanimado':
        g.append(
          stroke('M72,63 Q77,67 82,63', 2.8),
          stroke('M95,61 Q100,65 105,61', 2.8),
          stroke('M85,69.5 Q88.5,67.5 92,69.5', 2.6),
        );
        break;
      default: // normal
        g.append(
          eye(76, 62, 4.2),
          eye(99, 60, 4.2),
          stroke('M84,65 Q88.5,70.5 93,64.5', 3),
        );
        break;
    }
    return g;
  }

  function extrasOf(mood) {
    switch (mood) {
      case 'normal':
      case 'feliz':
      case 'sorprendido':
      case 'celebrando':
        return bursts(36, 44, false);
      case 'triste':
        return el('g', { class: 'nv-fx nv-float-slow' }, [
          el('path', { d: 'M32,42 C24,38 25,29 33,29 C39,29 39,35 34,36 C28,37 26,23 36,21 C43,19 46,25 41,28', 'stroke-width': 2.6 }),
        ]);
      case 'enojado':
        return el('g', { class: 'nv-fx nv-pulse', transform: 'translate(116,38)' }, [
          el('path', { d: 'M-8,-4 Q-4,-4 -4,-8', 'stroke-width': 3 }),
          el('path', { d: 'M8,-4 Q4,-4 4,-8', 'stroke-width': 3 }),
          el('path', { d: 'M-8,4 Q-4,4 -4,8', 'stroke-width': 3 }),
          el('path', { d: 'M8,4 Q4,4 4,8', 'stroke-width': 3 }),
        ]);
      case 'dormido':
        return el('g', { class: 'nv-fx nv-zzz' }, [
          el('path', { class: 'nv-z1', d: 'M105,35 H114 L105,45 H114', 'stroke-width': 3 }),
          el('path', { class: 'nv-z2', d: 'M118,20 H129 L118,33 H129', 'stroke-width': 3.4 }),
        ]);
      case 'estudiando':
        return el('g', { class: 'nv-fx nv-burst', transform: 'translate(114,48) scale(-1,1)' }, [
          el('path', { d: 'M0,0 L-10,-6', 'stroke-width': 4.2 }),
          el('path', { d: 'M-2,10 L-12,9', 'stroke-width': 4.2 }),
        ]);
      case 'concentrado':
        return el('g', { class: 'nv-fx nv-sparkle' }, [
          el('path', { class: 'nv-star', d: 'M119,35 Q121,43 129,45 Q121,47 119,55 Q117,47 109,45 Q117,43 119,35 Z' }),
          el('path', { class: 'nv-star nv-star2', d: 'M131,52 Q132,57 137,58 Q132,59 131,64 Q130,59 125,58 Q130,57 131,52 Z' }),
        ]);
      case 'preocupado':
        return el('g', { class: 'nv-fx' }, [
          el('path', { class: 'nv-sweat', d: 'M61,48 C61,48 56,56 56,60 A5,5 0 0 0 66,60 C66,56 61,48 61,48 Z' }),
          el('path', { d: 'M113,44 L118,38', 'stroke-width': 2.8 }),
          el('path', { d: 'M116,49 L123,46', 'stroke-width': 2.8 }),
        ]);
      case 'mirando':
        return el('g', { class: 'nv-fx nv-float-slow' }, [
          el('path', { d: 'M115,31 C115,24 126,24 126,31 C126,36 120,37 120,42', 'stroke-width': 4 }),
          el('circle', { class: 'nv-fx-dot', cx: 120, cy: 49, r: 2.6 }),
        ]);
      case 'desanimado':
        return el('g', { class: 'nv-fx nv-sigh' }, [
          el('path', { d: 'M121,76 C121,72 126,70 129,73 C133,73 135,77 132,81 C134,85 130,89 126,88 C122,89 118,86 120,82 C118,80 119,77 121,76 Z', 'stroke-width': 2.6 }),
        ]);
      default:
        return null;
    }
  }

  function bookLayer() {
    return el('g', { class: 'nv-book' }, [
      // open pages
      el('path', { class: 'nv-light', d: 'M62,73 Q76,71 86,78 Q96,71 110,72 L106,96 Q94,94 86,101 Q76,94 66,97 Z' }),
      // left & right covers
      el('path', { class: 'nv-deep', d: 'M60,75 L85,80 L84,107 L63,100 Z' }),
      el('path', { class: 'nv-mid', d: 'M112,73 L87,80 L86,107 L107,100 Z' }),
      // spine highlight
      el('path', { class: 'nv-light', opacity: 0.5, d: 'M85,80 L87,80 L86,107 L84,107 Z' }),
    ]);
  }

  function svg(mood = 'normal', opts = {}) {
    const m = MOODS.includes(mood) ? mood : 'normal';
    const raisedRight = m === 'feliz' || m === 'celebrando' || m === 'mirando';
    const sleeping = m === 'dormido';

    const root = el('svg', {
      class: `narval nv-${m}${opts.class ? ' ' + opts.class : ''}`,
      viewBox: '0 0 144 120',
      role: opts.label ? 'img' : null,
      'aria-label': opts.label || null,
      'aria-hidden': opts.label ? null : 'true',
    });

    const bodyGrp = el('g', { class: 'nv-body-grp' });

    // Right side flipper (behind body edge)
    const rightFlipper = raisedRight
      ? el('path', { class: 'nv-mid nv-flip-r', d: 'M98,74 C111,61 122,63 119,74 C116,83 105,86 96,82 Z' })
      : el('path', { class: 'nv-mid nv-flip-r', d: 'M100,76 C113,75 123,82 119,90 C115,96 103,94 95,87 Z' });

    // Tail flukes
    const tail = el('path', {
      class: 'nv-body nv-tail',
      d: 'M42,76 C30,70 18,68 14,75 C11,80 19,86 30,87 C24,93 20,101 26,104 C33,107 40,98 46,89 Z',
    });

    // Main round body
    const body = el('path', {
      class: 'nv-body',
      d: sleeping
        ? 'M36,88 C38,57 62,43 86,47 C106,50 116,66 113,86 C110,100 90,104 65,103 C46,102 35,98 36,88 Z'
        : 'M40,78 C40,46 64,30 87,34 C107,38 115,58 111,78 C107,98 86,107 65,105 C49,103 40,92 40,78 Z',
    });

    // Pale belly dome
    const belly = el('path', {
      class: 'nv-light',
      d: sleeping
        ? 'M52,102 C59,83 88,78 106,89 C102,100 81,106 52,102 Z'
        : 'M54,102 C60,77 90,71 107,84 C102,99 81,108 54,102 Z',
    });

    // Left front flipper
    const leftFlipper = el('path', {
      class: 'nv-mid nv-flip-l',
      d: sleeping
        ? 'M56,92 C46,95 45,103 54,104 C62,105 69,100 70,94 Z'
        : 'M56,82 C46,86 44,94 52,97 C58,100 67,95 69,87 Z',
    });

    // Iconic diagonal horn
    const horn = el('path', {
      class: 'nv-light nv-horn',
      d: sleeping
        ? 'M86,51 L115,24 C117,22 119,24 117,26 L96,58 C92,61 84,56 86,51 Z'
        : 'M85,42 L117,11 C119,9 121,11 119,13 L95,50 C91,53 83,48 85,42 Z',
    });

    bodyGrp.append(rightFlipper, tail, body, belly, leftFlipper, horn, faceOf(m));
    if (m === 'estudiando') bodyGrp.append(bookLayer());

    root.append(bodyGrp);
    const fx = extrasOf(m);
    if (fx) root.append(fx);
    return root;
  }

  return { MOODS, svg };
})());
