import type { APIRoute } from 'astro';
import { LANGS, HTML_LANG, pathOf } from '../i18n';
import { SITE } from '../lib/install';

export const GET: APIRoute = () => {
  const alt = LANGS.map((l) => `<xhtml:link rel="alternate" hreflang="${HTML_LANG[l]}" href="${SITE}${pathOf(l)}"/>`).join('');
  const urls = LANGS.map((l) => `<url><loc>${SITE}${pathOf(l)}</loc>${alt}</url>`).join('');
  return new Response(
    `<?xml version="1.0" encoding="UTF-8"?><urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9" xmlns:xhtml="http://www.w3.org/1999/xhtml">${urls}</urlset>`,
    { headers: { 'Content-Type': 'application/xml' } },
  );
};
