import { z } from "zod";

// @json-render 三件套（core/react/shadcn）只用于 view-mode plugin 渲染，静态 import 会把整个
// shadcn 组件库打进主 bundle。改为动态 import：首次真正需要时才加载，之后缓存 Promise。
let catalogPromise: Promise<any> | null = null;

export function getBaseCatalog(): Promise<any> {
  if (!catalogPromise) {
    catalogPromise = (async () => {
      const [{ defineCatalog }, { schema }, { shadcnComponentDefinitions }] =
        await Promise.all([
          import("@json-render/core"),
          import("@json-render/react"),
          import("@json-render/shadcn/catalog"),
        ]);
      return defineCatalog(schema, {
        name: "MindFS Base UI Catalog",
        components: shadcnComponentDefinitions as any,
        actions: {
          navigate: {
            params: z.object({
              path: z.string().optional(),
              cursor: z.number().optional(),
              query: z.record(z.string(), z.any()).optional(),
            }),
            description:
              "Update URL state for view plugins within current root. Built-in params: path/cursor; plugin params are exposed as file.query.",
          },
        },
      } as any);
    })();
  }
  return catalogPromise;
}

let cachedPrompt = "";

export async function getViewModeSystemPrompt(): Promise<string> {
  if (cachedPrompt) return cachedPrompt;
  const catalog = await getBaseCatalog();
  cachedPrompt = catalog.prompt();
  return cachedPrompt;
}

export async function buildViewModeMessage(userPrompt: string): Promise<string> {
  return [
    "[SYSTEM_PROMPT]",
    await getViewModeSystemPrompt(),
    "",
    "[USER_PROMPT]",
    userPrompt,
  ].join("\n");
}
