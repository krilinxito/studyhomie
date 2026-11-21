// @ts-check
import { defineConfig } from 'astro/config';

export default defineConfig({
  site: 'https://studyhomie.maxi42.space',
  trailingSlash: 'ignore',
  i18n: {
    defaultLocale: 'es',
    locales: ['es', 'en', 'fr', 'zh', 'pt'],
    routing: { prefixDefaultLocale: false },
  },
  // the narval (SVG + CSS) is shared with the app, in internal/web/static
  vite: { server: { fs: { allow: ['..'] } } },
});
