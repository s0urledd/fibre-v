import type { Metadata } from "next";
import { IBM_Plex_Sans, IBM_Plex_Mono } from "next/font/google";
import "./globals.css";
import { Header } from "@/components/Chrome";

// Fonts are fetched once at build time and served from this site's own
// origin: a visitor's browser opens no connection to a font CDN, which is the
// same promise the rest of the site makes about third parties.
const sans = IBM_Plex_Sans({ subsets: ["latin"], weight: ["400", "500", "600", "700"], variable: "--font-sans", display: "swap" });
const mono = IBM_Plex_Mono({ subsets: ["latin"], weight: ["400", "500", "600"], variable: "--font-mono", display: "swap" });

export const metadata: Metadata = {
  title: "Fibrescope · Celestia Fibre observer",
  description: "Independent measurement of whether Celestia validators keep their Fibre serving promise.",
};

// Applies a saved theme before the first paint so a dark-mode reader never
// sees a light flash; with nothing saved, the system preference is the start.
const themeBoot = `try{var t=localStorage.getItem("theme");if(t!=="light"&&t!=="dark")t=window.matchMedia&&window.matchMedia("(prefers-color-scheme: light)").matches?"light":"dark";document.documentElement.dataset.theme=t}catch(e){}`;

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en" className={`${sans.variable} ${mono.variable}`} suppressHydrationWarning>
      <body>
        <script dangerouslySetInnerHTML={{ __html: themeBoot }} />
        <Header />
        <main className="wrap">{children}</main>
      </body>
    </html>
  );
}
