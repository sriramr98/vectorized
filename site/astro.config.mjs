import { defineConfig } from "astro/config";
import mdx from "@astrojs/mdx";
import astroExpressiveCode from "astro-expressive-code";

export default defineConfig({
  integrations: [
    astroExpressiveCode({ themes: ["github-dark-default"] }),
    mdx(),
  ],
  output: "static",
});
