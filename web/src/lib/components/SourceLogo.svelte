<script lang="ts" module>
  const DARK_ID = 'source-logo-tritone-dark';
  const LIGHT_ID = 'source-logo-tritone-light';

  /**
   * One pair of SVG filters on the document. The CSS class then points at
   * them. Installing per image would duplicate ids.
   *
   * The discrete tables are the theme tokens as 0–1 RGB. CSS variables
   * cannot feed feFunc tableValues, so the numbers live here and must stay
   * in lockstep with app.css.
   */
  function installFilters(): void {
    if (typeof document === 'undefined') return;
    if (document.getElementById(DARK_ID)) return;

    const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
    svg.setAttribute('aria-hidden', 'true');
    svg.setAttribute('focusable', 'false');
    svg.setAttribute('width', '0');
    svg.setAttribute('height', '0');
    svg.classList.add('sr-only');
    svg.innerHTML = `
      <filter id="${DARK_ID}" color-interpolation-filters="sRGB" x="0" y="0" width="1" height="1">
        <feColorMatrix type="saturate" values="0"/>
        <feColorMatrix type="matrix" values="-1 0 0 0 1  0 -1 0 0 1  0 0 -1 0 1  0 0 0 1 0"/>
        <feComponentTransfer>
          <feFuncR type="discrete" tableValues="0.122 0.639 0.929 0.929"/>
          <feFuncG type="discrete" tableValues="0.114 0.612 0.914 0.914"/>
          <feFuncB type="discrete" tableValues="0.102 0.569 0.890 0.890"/>
          <feFuncA type="identity"/>
        </feComponentTransfer>
      </filter>
      <filter id="${LIGHT_ID}" color-interpolation-filters="sRGB" x="0" y="0" width="1" height="1">
        <feColorMatrix type="saturate" values="0"/>
        <feComponentTransfer>
          <feFuncR type="discrete" tableValues="0.129 0.384 0.961 0.961"/>
          <feFuncG type="discrete" tableValues="0.122 0.361 0.945 0.945"/>
          <feFuncB type="discrete" tableValues="0.110 0.329 0.918 0.918"/>
          <feFuncA type="identity"/>
        </feComponentTransfer>
      </filter>
    `;
    document.body.appendChild(svg);
  }
</script>

<script lang="ts">
  /**
   * A network or studio mark. Flat artwork silhouettes to ink. A lockup
   * (Marvel) uses the tri-tone so the lettering stays distinct from the badge.
   */
  import { sourceLogoLockup } from '../sourceLogo';

  interface Props {
    src: string;
    lockup?: boolean;
    class?: string;
  }

  let { src, lockup = undefined, class: klass = '' }: Props = $props();

  let lockupMark = $derived(sourceLogoLockup(src, lockup));

  installFilters();
</script>

<img
  {src}
  alt=""
  class="{lockupMark ? 'source-logo' : 'source-logo-flat'} {klass}"
  loading="lazy"
  decoding="async"
  onerror={(event) => {
    event.currentTarget.hidden = true;
  }} />
