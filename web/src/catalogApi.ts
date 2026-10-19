import { request } from "./api";
import type {
  CatalogCommand,
  CatalogEvent,
  CatalogSnapshot,
} from "./catalogTypes";

export const catalogApi = {
  snapshot: () => request<{ catalog: CatalogSnapshot }>("/api/catalog"),
  command: (expectedRevision: number, command: CatalogCommand) =>
    request<{ catalog: CatalogSnapshot }>("/api/catalog/commands", {
      method: "POST",
      body: JSON.stringify({
        expected_revision: expectedRevision,
        kind: command.kind,
        input: command.input,
      }),
    }),
  events: (after = 0, limit = 100) =>
    request<{ events: CatalogEvent[] }>(
      "/api/catalog/events?after=" + after + "&limit=" + limit,
    ),
};
