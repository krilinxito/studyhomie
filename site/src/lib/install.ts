// Install commands, the same in every language.
export const SITE = 'https://studyhomie.maxi42.space';
export const REPO = 'https://github.com/krilinxito/studyhomie';

export const CMD = {
  win: `irm ${SITE}/install.ps1 | iex`,
  unix: `curl -fsSL ${SITE}/install.sh | sh`,
  npm: 'npm install -g studyhomie',
  scoop: 'scoop bucket add studyhomie https://github.com/krilinxito/scoop-bucket\nscoop install studyhomie',
  brew: 'brew install krilinxito/tap/studyhomie',
  choco: 'choco install studyhomie',
  go: 'go install github.com/krilinxito/studyhomie/cmd/studyhomie@latest',
} as const;
export type Method = keyof typeof CMD;
export const METHODS = Object.keys(CMD) as Method[];

export const THEN = 'mkdir estudios\ncd estudios\nstudyhomie init\nstudyhomie';

// The latest release tag, read once per build (GITHUB_TOKEN avoids the anonymous rate limit in CI).
let latest: Promise<string> | undefined;
export function latestVersion(): Promise<string> {
  latest ??= (async () => {
    try {
      const headers: Record<string, string> = { Accept: 'application/vnd.github+json' };
      if (process.env.GITHUB_TOKEN) headers.Authorization = `Bearer ${process.env.GITHUB_TOKEN}`;
      const r = await fetch('https://api.github.com/repos/krilinxito/studyhomie/releases/latest', { headers });
      if (!r.ok) return '';
      const j = (await r.json()) as { tag_name?: string };
      return j.tag_name ?? '';
    } catch {
      return '';
    }
  })();
  return latest;
}
