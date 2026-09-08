import type { NextConfig } from "next";

// Static export: the pages are plain files served by Caddy next to the API;
// all data is fetched in the browser from NEXT_PUBLIC_API_BASE.
const nextConfig: NextConfig = {
  output: "export",
  trailingSlash: true,
  images: { unoptimized: true },
};

export default nextConfig;
