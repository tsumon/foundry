import { request } from "./api";
export interface KnowledgeSource {
  kind: string;
  id: string;
  revision: number;
}
export interface KnowledgeNode {
  id: string;
  kind: string;
  label: string;
  project_id?: string;
  mission_id?: string;
  state?: string;
  evidence_source?: string;
  historical: boolean;
  source: KnowledgeSource;
  properties: Record<string, string>;
}
export interface KnowledgeEdge {
  id: string;
  from: string;
  to: string;
  kind: string;
  source: KnowledgeSource;
}
export interface KnowledgeGraph {
  schema_version: number;
  backend: string;
  source_digest: string;
  synced_at: string;
  total_nodes: number;
  total_edges: number;
  truncated: boolean;
  nodes: KnowledgeNode[];
  edges: KnowledgeEdge[];
  issues: { code: string; source_id: string; message: string }[];
}
export interface Ontology {
  version: number;
  types: { kind: string; label: string; description: string }[];
  relations: {
    kind: string;
    label: string;
    from: string[];
    to: string[];
    pairs?: [string, string][];
  }[];
  rules: string[];
}
export const knowledgeApi = {
  schema: (signal?: AbortSignal) =>
    request<Ontology>("/api/knowledge/schema", { signal }),
  graph: (query: URLSearchParams, signal?: AbortSignal) =>
    request<KnowledgeGraph>(`/api/knowledge?${query}`, { signal }),
  rebuild: (signal?: AbortSignal) =>
    request<KnowledgeGraph>("/api/knowledge/rebuild", {
      method: "POST",
      body: "{}",
      signal,
    }),
};
