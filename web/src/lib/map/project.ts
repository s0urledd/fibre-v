/**
 * The host map's geometry: the land dot grid and country label points built
 * offline by web/scripts/build-map.mjs from Natural Earth, and the Equal Earth
 * forward projection that places a host on the same grid.
 */
import dots from "./world-dots.json";
import centroids from "./centroids.json";

export const GRID = { cols: dots.cols, rows: dots.rows };

const A1 = 1.340264, A2 = -0.081106, A3 = 0.000893, A4 = 0.003796, M = Math.sqrt(3) / 2;

/** lon/lat in degrees -> grid units (x right, y down), matching the dot grid */
export function toGrid(lon: number, lat: number): [number, number] {
  const l = (lon * Math.PI) / 180, p = (lat * Math.PI) / 180;
  const t = Math.asin(M * Math.sin(p)), t2 = t * t, t6 = t2 * t2 * t2;
  const x = (l * Math.cos(t)) / (M * (A1 + 3 * A2 * t2 + t6 * (7 * A3 + 9 * A4 * t2)));
  const y = t * (A1 + A2 * t2 + t6 * (A3 + A4 * t2));
  return [(x - dots.xmin) / dots.step, Math.min(dots.rows - 0.5, Math.max(0.5, (dots.ytop - y) / dots.step))];
}

const points = centroids.points as unknown as Record<string, [number, number]>;

/** a country's label point, by ISO 3166-1 alpha-2 */
export function countryPoint(cc: string | undefined): [number, number] | null {
  if (!cc) return null;
  return points[cc.toUpperCase()] ?? null;
}

/** every land dot as one path of zero-length round-capped strokes: one DOM node for ~4,000 dots */
export function landPath(): string {
  const out: string[] = [];
  dots.runs.forEach((row, r) => {
    let at = 0;
    for (let i = 0; i < row.length; i += 2) {
      at += row[i];
      out.push(`M${at + 0.5} ${r + 0.5}h0`);
      for (let k = 1; k < row[i + 1]; k++) out.push("m1 0h0");
      at += row[i + 1];
    }
  });
  return out.join("");
}
