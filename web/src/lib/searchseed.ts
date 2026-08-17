/**
 * Client-side twins of internal/searchql's seed builders.
 *
 * The item loads fast and the fan-out is the slow half of the picker, so the
 * box seeds from the item the moment it arrives instead of sitting empty for
 * seconds. The server's `search_expression` replaces these when the search
 * lands; the twins exist to write the exact same string, so that replacement
 * is invisible. The unit tests pin each one against the server's spelling.
 */

/** Always quoted, like the server: the quotes show a machine-written value's
 * edges before the user starts editing it. */
function quote(value: string): string {
  return '"' + value.trim().replaceAll('\\', '\\\\').replaceAll('"', '\\"') + '"';
}

/** internal/core's sceneQueryText: letters and digits of any script survive,
 * everything else is a word break. */
function sceneQueryText(title: string): string {
  return (title.replace(/[^\p{L}\p{N}]+/gu, ' ').match(/\S+/g) ?? []).join(' ');
}

export function movieSeed(title: string, year: number): string {
  const parts = [`title:${quote(title)}`];
  if (year > 0) parts.push(`year:${year}`);
  return parts.join(' ');
}

export function seriesSeed(title: string, season: number, episode: number): string {
  const parts = [`title:${quote(title)}`];
  if (season >= 0) {
    parts.push(`season:${season}`);
    if (episode > 0) parts.push(`episode:${episode}`);
  }
  return parts.join(' ');
}

/**
 * The scene seed spells out both variants the page will run: the dated form
 * OR the site-and-title fallback. airDate is the item payload's ISO string
 * ("" when unknown); sceneTitle may be empty or all punctuation, either of
 * which means there is no title form worth writing.
 */
export function sceneSeed(site: string, airDate: string, sceneTitle: string): string {
  const siteTerm = `site:${quote(site)}`;
  const date = airDate.slice(0, 10);
  const dated = /^\d{4}-\d{2}-\d{2}$/.test(date) ? `${siteTerm} date:${date}` : '';
  const cleaned = sceneQueryText(sceneTitle);
  const titled = cleaned === '' ? '' : quote(`${site.trim()} ${cleaned}`);
  if (dated !== '' && titled !== '') return `(${dated}) OR ${titled}`;
  if (dated !== '') return dated;
  if (titled !== '') return titled;
  return siteTerm;
}
