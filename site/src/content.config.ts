import { defineCollection } from "astro:content";
import { glob } from "astro/loaders";
import { z } from "astro/zod";

const chapters = defineCollection({
  loader: glob({ pattern: "**/*.{md,mdx}", base: "./src/content/chapters" }),
  schema: z.object({
    title: z.string(),
    description: z.string(),
    order: z.number(),
    chapter: z.number(),
    chapterTitle: z.string(),
    lesson: z.number(),
    status: z.enum(["complete", "building", "planned"]),
    readTime: z.string(),
    sourceRef: z.string(),
    topics: z.array(z.string()),
  }),
});

export const collections = { chapters };
