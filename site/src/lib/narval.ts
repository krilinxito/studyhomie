// Renders the app's narval (internal/web/static/narval.js) to an SVG string at build time, so the
// site shows the same mascot without shipping the script or waiting for JS.
import { parseHTML } from 'linkedom';
import source from '../../../internal/web/static/narval.js?raw';

export type Mood =
  | 'normal' | 'feliz' | 'triste' | 'enojado' | 'dormido' | 'sorprendido'
  | 'estudiando' | 'concentrado' | 'celebrando' | 'preocupado' | 'mirando' | 'desanimado';

type NarvalAPI = { svg(mood: Mood, opts?: { class?: string; label?: string }): { outerHTML: string } };

let api: NarvalAPI | undefined;
function load(): NarvalAPI {
  if (api) return api;
  const { window, document } = parseHTML('<!doctype html><html><body></body></html>');
  new Function('window', 'document', source)(window, document);
  api = (window as unknown as { Narval: NarvalAPI }).Narval;
  return api;
}

export function narvalSVG(mood: Mood, label?: string): string {
  return load().svg(mood, { label }).outerHTML;
}
