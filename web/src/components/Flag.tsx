/**
 * Country flags: the country-flag-icons 3x2 SVG set (MIT), vendored under
 * public/flags/ and loaded as plain images, so no flag is in a bundle and a
 * page fetches only the flags it shows. Emoji flags are not used because
 * Windows draws them as two letters.
 */

let regionNames: Intl.DisplayNames | null | undefined;

/** "DE" -> "Germany"; the code itself where the browser has no name for it */
export function countryName(cc: string): string {
  if (regionNames === undefined) {
    try { regionNames = new Intl.DisplayNames(["en"], { type: "region" }); } catch { regionNames = null; }
  }
  try { return regionNames?.of(cc.toUpperCase()) ?? cc; } catch { return cc; }
}

/**
 * A flag, 16 x 11 by default. With `label` the image carries the country's
 * name for assistive tech (use it where the flag stands alone); without, it is
 * decorative because the name is already written beside it.
 */
export function Flag({ cc, label = false, size = 16 }: { cc?: string; label?: boolean; size?: number }) {
  const code = (cc ?? "").toUpperCase();
  if (!/^[A-Z]{2}$/.test(code)) return null;
  const name = countryName(code);
  return (
    <img className="flag" src={`/flags/${code}.svg`} width={size} height={Math.round((size * 2) / 3)}
      alt={label ? name : ""} title={label ? name : undefined} loading="lazy" decoding="async" draggable={false} />
  );
}
