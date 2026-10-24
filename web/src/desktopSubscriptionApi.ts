import { request } from "./api";
export interface DesktopPreparation {
  preparation: {
    phase: string;
    route_id?: string;
    blocker?: string;
    connection_ready: boolean;
    catalog_ready: boolean;
  };
  catalog_choices: {
    operation_id: string;
    route_id: string;
    model: string;
    display_name: string;
    expires_at: string;
  }[];
}
const path = (id: string) =>
  `/api/model-accounts/${encodeURIComponent(id)}/desktop-preparation`;
export const desktopSubscriptionApi = {
  read: (id: string) => request<DesktopPreparation>(path(id)),
  prepare: (
    id: string,
    phase: "connection" | "catalog",
    route_id: string,
    expected_account_revision: number,
  ) =>
    request<DesktopPreparation>(path(id), {
      method: "POST",
      body: JSON.stringify({ phase, route_id, expected_account_revision }),
    }),
};
