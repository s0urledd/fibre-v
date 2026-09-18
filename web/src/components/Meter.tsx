/**
 * One ratio against a threshold: the rows that came back, the rows that
 * would rebuild the blob, the rows there are. The fill is the value, the
 * tick is the threshold, the track is the total.
 */
export default function Meter({ value, threshold, total, unit }: { value: number; threshold: number; total: number; unit: string }) {
  if (!total || total <= 0) return null;
  const pct = (n: number) => `${Math.min(100, Math.max(0, (n / total) * 100))}%`;
  const ok = value >= threshold;
  return (
    <div className="meter" role="img" aria-label={`${value.toLocaleString("en-US")} of ${total.toLocaleString("en-US")} ${unit}; ${threshold.toLocaleString("en-US")} needed`}>
      <div className="meter-track">
        <div className={"meter-fill" + (ok ? " ok" : " short")} style={{ width: pct(value) }} />
        <div className="meter-tick" style={{ left: pct(threshold) }} title={`${threshold.toLocaleString("en-US")} ${unit} are enough to rebuild the blob`} />
      </div>
      <div className="meter-scale">
        <span>0</span>
        <span className="meter-need" style={{ left: pct(threshold) }}>needed {threshold.toLocaleString("en-US")}</span>
        <span>{total.toLocaleString("en-US")}</span>
      </div>
    </div>
  );
}
