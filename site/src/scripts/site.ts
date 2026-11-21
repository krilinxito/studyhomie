// The page's only script: scroll progress for the choreographies, reveals, the depth gauge,
// the OS-specific install command, copy buttons, theme and language choice.
const root = document.documentElement;
const reduced = matchMedia('(prefers-reduced-motion: reduce)').matches;

const store = {
  get(k: string): string | null { try { return localStorage.getItem(k); } catch { return null; } },
  set(k: string, v: string): void { try { localStorage.setItem(k, v); } catch { /* private mode */ } },
};

/* ---------- scroll progress: --p from 0 to 1 on every [data-p] ---------- */
// view: from the moment the element's top enters at the bottom until its bottom leaves at the top.
// track: a tall section with a sticky stage, from its top at the top to its bottom at the bottom.
const tracked = [...document.querySelectorAll<HTMLElement>('[data-p]')];
const steps = [...document.querySelectorAll<HTMLElement>('[data-at]')];
const sea = document.querySelector<HTMLElement>('[data-sea]');
const levels = [...document.querySelectorAll<HTMLElement>('[data-mood]')];
const gauge = document.querySelector<HTMLElement>('[data-gauge]');
const header = document.querySelector<HTMLElement>('.top');
const diver = document.querySelector<HTMLElement>('[data-diver]');
const diverSay = document.querySelector<HTMLElement>('[data-diver-say]');
const depths = [...document.querySelectorAll<HTMLElement>('[data-depth]')];
const MAX_DEPTH = Number(depths[depths.length - 1]?.dataset.depth || 260);
const clamp = (x: number) => Math.min(1, Math.max(0, x));

function progress(el: HTMLElement, vh: number): number {
  const r = el.getBoundingClientRect();
  // a track only works while it is taller than the screen (on phones the stage is not sticky)
  if (el.dataset.p === 'track' && r.height > vh * 1.3) return clamp(-r.top / (r.height - vh));
  // views finish at 60% of the way, so the end state is readable before the section leaves
  return clamp((vh - r.top) / (vh + r.height) / 0.6);
}

let ticking = false;
function frame(): void {
  ticking = false;
  const vh = innerHeight;
  for (const el of tracked) el.style.setProperty('--p', progress(el, vh).toFixed(4));
  for (const s of steps) {
    const owner = s.closest<HTMLElement>('[data-p]');
    const p = owner ? Number(owner.style.getPropertyValue('--p') || 0) : 1;
    s.classList.toggle('on', p >= Number(s.dataset.at));
  }
  // underwater from the waterline (the top of the sea) down to the bottom of the page
  const hy = header ? header.offsetHeight / 2 : 30;
  const seaTop = sea ? sea.getBoundingClientRect().top : Infinity;
  const under = seaTop < hy;
  root.classList.toggle('under', under);
  const m = under ? meters(vh * 0.5, seaTop) : 0;
  root.style.setProperty('--depth', (m / MAX_DEPTH).toFixed(4));
  if (gauge) gauge.textContent = m > 0 ? `−${Math.round(m)} m` : '0 m';
  swim(vh);
}
// meters at the middle of the screen: the waterline is 0 and each level's top is the depth in its heading
function meters(mid: number, seaTop: number): number {
  const marks = [{ y: seaTop, m: 0 }, ...depths.map((el) => ({ y: el.getBoundingClientRect().top, m: Number(el.dataset.depth) }))];
  for (let i = marks.length - 1; i >= 0; i--) {
    const a = marks[i];
    if (a.y > mid) continue;
    const b = marks[i + 1];
    return b ? a.m + ((b.m - a.m) * (mid - a.y)) / Math.max(1, b.y - a.y) : a.m;
  }
  return 0;
}
function onScroll(): void {
  if (!ticking) { ticking = true; requestAnimationFrame(frame); }
}

/* ---------- the narval dives with you: the level at the middle of the screen sets its mood ---------- */
let mood = '';
let said = '';
function swim(vh: number): void {
  if (!diver) return;
  const mid = vh * 0.5;
  const here = levels.find((el) => { const r = el.getBoundingClientRect(); return r.top <= mid && r.bottom > mid; });
  if (!here) return;
  root.classList.toggle('shallow-here', here.classList.contains('shallow'));
  root.classList.toggle('at-base', here.classList.contains('seabed'));
  const p = Number(here.style.getPropertyValue('--p') || 0);
  const late = !!here.dataset.moodEnd && p > 0.7;
  const m = (late ? here.dataset.moodEnd : here.dataset.mood) || 'normal';
  const say = (late ? here.dataset.sayEnd : here.dataset.say) || '';
  if (m !== mood) {
    mood = m;
    diver.querySelectorAll('.nv').forEach((n) => n.classList.toggle('on', n.classList.contains(`m-${m}`)));
  }
  if (diverSay && say !== said) {
    said = say;
    diverSay.classList.remove('show');
    void diverSay.offsetWidth; // restart the fade
    diverSay.textContent = say;
    if (say) diverSay.classList.add('show');
  }
}

// it noses down while you scroll down, up while you go back up, and levels off when you stop
let lastY = scrollY;
let still: number | undefined;
addEventListener('scroll', () => {
  if (!diver || reduced) return;
  const dy = scrollY - lastY;
  lastY = scrollY;
  if (Math.abs(dy) < 2) return;
  diver.dataset.dir = dy > 0 ? 'down' : 'up';
  clearTimeout(still);
  still = window.setTimeout(() => { delete diver.dataset.dir; }, 260);
}, { passive: true });

/* ---------- reveals ---------- */
const reveals = document.querySelectorAll<HTMLElement>('.rv');
if (reduced || !('IntersectionObserver' in window)) {
  reveals.forEach((el) => el.classList.add('in'));
  tracked.forEach((el) => el.style.setProperty('--p', '1'));
  steps.forEach((el) => el.classList.add('on'));
  root.classList.add('still');
} else {
  const io = new IntersectionObserver((entries) => {
    for (const e of entries) if (e.isIntersecting) { e.target.classList.add('in'); io.unobserve(e.target); }
  }, { rootMargin: '0px 0px -12% 0px' });
  reveals.forEach((el) => io.observe(el));
}
addEventListener('scroll', onScroll, { passive: true });
addEventListener('resize', onScroll);
frame();

/* ---------- the install command for this visitor's system ---------- */
function detectOS(): 'win' | 'unix' {
  const uad = (navigator as Navigator & { userAgentData?: { platform?: string } }).userAgentData;
  const p = (uad?.platform || navigator.platform || navigator.userAgent || '').toLowerCase();
  return p.includes('win') ? 'win' : 'unix';
}
const osBox = document.querySelector<HTMLElement>('[data-oscmd]');
function pickOS(os: string): void {
  if (!osBox) return;
  osBox.querySelectorAll<HTMLElement>('.cmd[data-os]').forEach((c) => { c.hidden = c.dataset.os !== os; });
  osBox.querySelectorAll<HTMLButtonElement>('[data-os-pick]').forEach((b) => b.setAttribute('aria-pressed', String(b.dataset.osPick === os)));
}
pickOS(detectOS());
document.querySelectorAll<HTMLButtonElement>('[data-os-pick]').forEach((b) => b.addEventListener('click', () => pickOS(b.dataset.osPick || 'win')));

/* ---------- copy buttons ---------- */
document.querySelectorAll<HTMLButtonElement>('[data-copy]').forEach((btn) => {
  btn.addEventListener('click', async () => {
    const code = btn.closest('.cmd')?.querySelector('code')?.textContent ?? '';
    try { await navigator.clipboard.writeText(code); } catch { return; }
    const label = btn.querySelector('span');
    if (!label) return;
    const before = label.textContent;
    label.textContent = btn.dataset.done || '✓';
    btn.classList.add('done');
    setTimeout(() => { label.textContent = before; btn.classList.remove('done'); }, 1600);
  });
});

/* ---------- theme ---------- */
document.querySelector('[data-theme-toggle]')?.addEventListener('click', () => {
  const dark = root.dataset.theme ? root.dataset.theme === 'dark' : matchMedia('(prefers-color-scheme: dark)').matches;
  const next = dark ? 'light' : 'dark';
  root.dataset.theme = next;
  store.set('sh-theme', next);
});

/* ---------- language: an explicit choice wins over the browser's ---------- */
document.querySelectorAll<HTMLAnchorElement>('[data-lang]').forEach((a) => {
  a.addEventListener('click', () => store.set('sh-lang', a.dataset.lang || 'es'));
});
