import type { Metadata } from "next";
import "./globals.css";
import { Header, Banner, Footer } from "@/components/Chrome";

export const metadata: Metadata = {
  title: "Fibre observer",
  description: "Independent measurement of whether Celestia validators keep their Fibre serving promise.",
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en">
      <body>
        <Header />
        <Banner />
        <main className="wrap">{children}</main>
        <Footer />
      </body>
    </html>
  );
}
