// Loads AutoAnimate (an ES module) and hands it to app.js, which is a classic script.
import autoAnimate from './vendor/auto-animate/index.min.js';

window.autoAnimate = autoAnimate;
window.dispatchEvent(new Event('autoanimate-ready'));
