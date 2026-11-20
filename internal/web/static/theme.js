'use strict';
// Applies the chosen theme before the page paints, so it never flashes the wrong one.
(() => {
  try {
    const t = JSON.parse(localStorage.getItem('sh.theme') || 'null');
    if (t === 'light' || t === 'dark') document.documentElement.dataset.theme = t;
  } catch { /* private mode: follow the system */ }
})();
