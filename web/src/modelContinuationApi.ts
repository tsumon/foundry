import { request } from "./api";
import type { Mission } from "./types";

const path = (id: string) =>
  `/api/missions/${encodeURIComponent(id)}/model-continuations`;
const post = (url: string, body: unknown) =>
  request<{ mission: Mission }>(url, {
    method: "POST",
    body: JSON.stringify(body),
  });
export const modelContinuationApi = {
  candidates: (id: string) =>
    request<{
      authorizations: {
        id: string;
        account_id: string;
        state: string;
        generation: number;
        expires_at: string;
      }[];
    }>(`${path(id)}/candidates`),
  prepare: (
    id: string,
    revision: number,
    authorization_id: string,
    catalog_operation_id: string,
  ) => post(path(id), { revision, authorization_id, catalog_operation_id }),
  approve: (
    id: string,
    continuationID: string,
    revision: number,
    digest: string,
  ) =>
    post(`${path(id)}/${encodeURIComponent(continuationID)}/approve`, {
      revision,
      digest,
    }),
  apply: (
    id: string,
    continuationID: string,
    revision: number,
    digest: string,
  ) =>
    post(`${path(id)}/${encodeURIComponent(continuationID)}/apply`, {
      revision,
      digest,
    }),
};
