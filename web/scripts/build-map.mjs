#!/usr/bin/env node
/**
 * Builds the two small files the overview's host map draws from. Run once,
 * offline; the output is committed and nothing here runs in the browser.
 *
 *   node web/scripts/build-map.mjs <ne_110m_land.geojson> <ne_50m_admin_0_countries.geojson>
 *
 * Inputs are Natural Earth (public domain), from
 * https://github.com/nvkelso/natural-earth-vector/tree/master/geojson
 *
 * Output:
 *   web/src/lib/map/world-dots.json   land as a dot grid in the Equal Earth
 *                                     projection, one run list per row
 *   web/src/lib/map/centroids.json    ISO 3166-1 alpha-2 -> [lon, lat], each
 *                                     country's Natural Earth label point
 *
 * The projection is Equal Earth (Šavrič, Patterson, Jenny 2018): equal-area,
 * so a country's share of dots is its share of land, and it keeps the poles
 * from swelling the way a plate carrée does. The page projects host positions
 * with the same forward formula (web/src/lib/map/project.ts).
 */
import { readFileSync, writeFileSync, mkdirSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const [landPath, countriesPath] = process.argv.slice(2);
if (!landPath || !countriesPath) {
  console.error("usage: build-map.mjs <ne_110m_land.geojson> <ne_50m_admin_0_countries.geojson>");
  process.exit(2);
}
const outDir = join(dirname(fileURLToPath(import.meta.url)), "..", "src", "lib", "map");
mkdirSync(outDir, { recursive: true });

// ---- grid parameters -------------------------------------------------------
const COLS = 190;          // dots across the full width of the projection
const LAT_TOP = 81;        // Greenland loses only its northern rim
const LAT_BOTTOM = -55.5;  // Cape Horn; Antarctica is left out
const SUPER = 3;           // 3x3 samples per cell
const MIN_LAND = 3;        // of 9 samples on land -> a dot (keeps the UK, Japan, NZ)

// ---- Equal Earth -----------------------------------------------------------
const A1 = 1.340264, A2 = -0.081106, A3 = 0.000893, A4 = 0.003796, M = Math.sqrt(3) / 2;
function forward(lon, lat) {
  const l = (lon * Math.PI) / 180, p = (lat * Math.PI) / 180;
  const t = Math.asin(M * Math.sin(p)), t2 = t * t, t6 = t2 * t2 * t2;
  return [(l * Math.cos(t)) / (M * (A1 + 3 * A2 * t2 + t6 * (7 * A3 + 9 * A4 * t2))), t * (A1 + A2 * t2 + t6 * (A3 + A4 * t2))];
}
function inverse(x, y) {
  let t = y;
  for (let i = 0; i < 20; i++) {
    const t2 = t * t, t6 = t2 * t2 * t2;
    const f = t * (A1 + A2 * t2 + t6 * (A3 + A4 * t2)) - y;
    const d = A1 + 3 * A2 * t2 + t6 * (7 * A3 + 9 * A4 * t2);
    const dt = f / d;
    t -= dt;
    if (Math.abs(dt) < 1e-12) break;
  }
  const t2 = t * t, t6 = t2 * t2 * t2;
  const lon = (M * x * (A1 + 3 * A2 * t2 + t6 * (7 * A3 + 9 * A4 * t2))) / Math.cos(t);
  const s = Math.sin(t) / M;
  if (Math.abs(s) > 1 || Math.abs(lon) > Math.PI) return null;
  return [(lon * 180) / Math.PI, (Math.asin(s) * 180) / Math.PI];
}

// ---- land polygons ---------------------------------------------------------
const land = JSON.parse(readFileSync(landPath, "utf8"));
const polys = [];
for (const f of land.features) {
  const g = f.geometry;
  const list = g.type === "Polygon" ? [g.coordinates] : g.type === "MultiPolygon" ? g.coordinates : [];
  for (const rings of list) {
    let minX = 180, maxX = -180, minY = 90, maxY = -90;
    for (const [x, y] of rings[0]) { minX = Math.min(minX, x); maxX = Math.max(maxX, x); minY = Math.min(minY, y); maxY = Math.max(maxY, y); }
    polys.push({ rings, minX, maxX, minY, maxY });
  }
}
function inRing(ring, x, y) {
  let c = false;
  for (let i = 0, j = ring.length - 1; i < ring.length; j = i++) {
    const [xi, yi] = ring[i], [xj, yj] = ring[j];
    if (yi > y !== yj > y && x < ((xj - xi) * (y - yi)) / (yj - yi) + xi) c = !c;
  }
  return c;
}
function onLand(lon, lat) {
  for (const p of polys) {
    if (lon < p.minX || lon > p.maxX || lat < p.minY || lat > p.maxY) continue;
    let c = false;
    for (const r of p.rings) if (inRing(r, lon, lat)) c = !c; // outer ring, holes flip it back
    if (c) return true;
  }
  return false;
}

// ---- the dot grid ----------------------------------------------------------
const XMAX = forward(180, 0)[0];
const STEP = (2 * XMAX) / COLS;
const YTOP = forward(0, LAT_TOP)[1];
const YBOT = forward(0, LAT_BOTTOM)[1];
const ROWS = Math.round((YTOP - YBOT) / STEP);

const runs = [];
let dots = 0;
for (let r = 0; r < ROWS; r++) {
  const row = [];
  let start = -1;
  for (let c = 0; c <= COLS; c++) {
    let hit = 0;
    if (c < COLS) {
      for (let i = 0; i < SUPER; i++) for (let j = 0; j < SUPER; j++) {
        const x = -XMAX + (c + (i + 0.5) / SUPER) * STEP;
        const y = YTOP - (r + (j + 0.5) / SUPER) * STEP;
        const ll = inverse(x, y);
        if (ll && onLand(ll[0], ll[1])) hit++;
      }
    }
    const isLand = hit >= MIN_LAND;
    if (isLand && start < 0) start = c;
    if (!isLand && start >= 0) { row.push(start, c - start); dots += c - start; start = -1; }
  }
  runs.push(row);
}
// Trim the empty ocean at both ends: Equal Earth narrows toward the poles,
// so no land reaches the outer columns. One column of margin either side.
let c0 = COLS, c1 = 0;
for (const row of runs) if (row.length) { c0 = Math.min(c0, row[0]); c1 = Math.max(c1, row[row.length - 2] + row[row.length - 1]); }
c0 = Math.max(0, c0 - 1); c1 = Math.min(COLS, c1 + 1);
// Delta-encode each row's starts so the JSON stays small: [gap, len, gap, len, ...].
const packed = runs.map((row) => {
  const out = [];
  let at = c0;
  for (let i = 0; i < row.length; i += 2) { out.push(row[i] - at, row[i + 1]); at = row[i] + row[i + 1]; }
  return out;
});
const round = (n, d = 6) => Number(n.toFixed(d));
writeFileSync(join(outDir, "world-dots.json"), JSON.stringify({
  source: "Natural Earth 1:110m land (public domain), Equal Earth projection",
  cols: c1 - c0, rows: ROWS, step: round(STEP), xmin: round(-XMAX + c0 * STEP), ytop: round(YTOP),
  runs: packed,
}) + "\n");

// ---- country label points --------------------------------------------------
const countries = JSON.parse(readFileSync(countriesPath, "utf8"));
const cent = {};
for (const f of countries.features) {
  const p = f.properties;
  let code = [p.ISO_A2_EH, p.ISO_A2, p.WB_A2].find((v) => typeof v === "string" && /^[A-Z]{2}$/.test(v));
  if (!code) continue;
  if (cent[code]) continue; // the first feature for a code is the sovereign mainland
  const lon = p.LABEL_X, lat = p.LABEL_Y;
  if (typeof lon !== "number" || typeof lat !== "number") continue;
  cent[code] = [round(lon, 2), round(lat, 2)];
}
const sorted = Object.fromEntries(Object.keys(cent).sort().map((k) => [k, cent[k]]));
writeFileSync(join(outDir, "centroids.json"), JSON.stringify({
  source: "Natural Earth 1:50m admin-0 countries, LABEL_X/LABEL_Y (public domain)",
  points: sorted,
}) + "\n");

console.log(`${c1 - c0}x${ROWS} grid, ${dots} land dots; ${Object.keys(sorted).length} country points -> ${outDir}`);
