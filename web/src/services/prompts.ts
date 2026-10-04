import { controlPath } from "./controlPlane";
import { protectedJSON } from "./api";

export async function savePrompt(text: string): Promise<string[]> {
  const data = await protectedJSON<any>(controlPath("/api/prompts"), {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
    },
    body: JSON.stringify({ text }),
  });
  return Array.isArray(data?.items) ? data.items : [];
}

export async function deletePrompt(text: string): Promise<string[]> {
  const data = await protectedJSON<any>(controlPath("/api/prompts"), {
    method: "DELETE",
    headers: {
      "Content-Type": "application/json",
    },
    body: JSON.stringify({ text }),
  });
  return Array.isArray(data?.items) ? data.items : [];
}
