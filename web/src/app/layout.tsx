import type { Metadata } from "next";
import "./globals.css";
import { Header } from "@/components/Chrome";

export const metadata: Metadata = {
  title: "Fibrescope · Celestia Fibre observer",
  description: "Independent measurement of whether Celestia validators keep their Fibre serving promise.",
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en">
      <body>
        <Header />
        <main className="wrap">{children}</main>
      </body>
    </html>
  );
}
