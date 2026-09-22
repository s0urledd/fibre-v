"use client";
import { useState } from "react";

/** Copies one identifier to the clipboard and says so for a moment. */
export default function Copy({ text, label }: { text: string; label?: string }) {
  const [done, setDone] = useState(false);
  const go = async () => {
    try { await navigator.clipboard.writeText(text); setDone(true); setTimeout(() => setDone(false), 1500); } catch { /* clipboard unavailable */ }
  };
  return <button type="button" className="copy" onClick={go} aria-label={`copy ${label ?? text}`} title={text}>{done ? "copied" : "copy"}</button>;
}
