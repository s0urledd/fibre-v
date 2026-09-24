import type { ReactNode } from "react";

/**
 * A formatted figure with its unit in the quieter colour, at the same size:
 * "4.2 MiB", "0.120 TIA". Strings without a trailing unit come back as they are.
 */
export function unit(s: string): ReactNode {
  const m = /^(.*\d)(\s+[A-Za-z/]+)$/.exec(s);
  return m ? <>{m[1]}<span className="u">{m[2]}</span></> : s;
}
