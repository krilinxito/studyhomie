import { es, type Dict } from './es';
import { en } from './en';
import { fr } from './fr';
import { zh } from './zh';
import { pt } from './pt';

export const LANGS = ['es', 'en', 'fr', 'zh', 'pt'] as const;
export type Lang = (typeof LANGS)[number];
export const DEFAULT_LANG: Lang = 'es';

export const NAMES: Record<Lang, string> = { es: 'Español', en: 'English', fr: 'Français', zh: '简体中文', pt: 'Português (Brasil)' };
// the html lang attribute and og:locale for each one
export const HTML_LANG: Record<Lang, string> = { es: 'es', en: 'en', fr: 'fr', zh: 'zh-Hans', pt: 'pt-BR' };

const DICTS: Record<Lang, Dict> = { es, en, fr, zh, pt };
export const dict = (lang: Lang): Dict => DICTS[lang];
export const pathOf = (lang: Lang): string => (lang === DEFAULT_LANG ? '/' : `/${lang}/`);
export const fill = (s: string, vars: Record<string, string | number>): string =>
  s.replace(/\{(\w+)\}/g, (_, k: string) => String(vars[k] ?? ''));
