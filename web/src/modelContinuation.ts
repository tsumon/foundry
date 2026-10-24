import { roles, type Mission, type ModelContinuationMaterial } from "./types";

const digest = (value: unknown) =>
  typeof value === "string" && /^[a-f0-9]{64}$/.test(value);
const date = (value: unknown) =>
  typeof value === "string" && Number.isFinite(Date.parse(value));
const identifier = (value: unknown) =>
  typeof value === "string" && value.length > 0;
export function validContinuation(c: ModelContinuationMaterial): boolean {
  return (
    !!c &&
    identifier(c.id) &&
    digest(c.digest) &&
    digest(c.predecessor_digest) &&
    digest(c.replacement_digest) &&
    Number.isSafeInteger(c.prepared_revision) &&
    c.prepared_revision > 0 &&
    Number.isSafeInteger(c.authorization_generation) &&
    c.authorization_generation > 0 &&
    identifier(c.authorization_id) &&
    identifier(c.catalog_operation_id) &&
    date(c.created_at) &&
    date(c.expires_at) &&
    date(c.authorization_expires_at) &&
    Array.isArray(c.affected_roles) &&
    c.affected_roles.length > 0 &&
    c.affected_roles.every((role) => roles.includes(role)) &&
    ["prepared", "approved", "applied", "stale", "expired"].includes(c.state) &&
    (!c.approval ||
      (c.approval.digest === c.digest &&
        c.approval.revision === c.prepared_revision + 2 &&
        date(c.approval.created_at))) &&
    (!c.application ||
      (!!c.approval &&
        c.application.digest === c.digest &&
        c.application.revision === c.prepared_revision + 3 &&
        date(c.application.created_at))) &&
    (c.state !== "prepared" || (!c.approval && !c.application)) &&
    (c.state !== "approved" || (!!c.approval && !c.application)) &&
    (c.state !== "applied" || !!c.application)
  );
}
export function invalidContinuation(m: Mission): boolean {
  if (m.model_continuation_status === "invalid_or_unsupported") return true;
  if (
    m.model_continuations !== undefined &&
    !Array.isArray(m.model_continuations)
  )
    return true;
  const history = m.model_continuations ?? [];
  if (!history.length)
    return (
      !!m.effective_model_snapshot &&
      m.effective_model_snapshot.digest !== m.model_snapshot?.digest
    );
  if (
    m.model_continuation_status !== "valid" ||
    history.some((c) => !validContinuation(c))
  )
    return true;
  const applied = history.filter((c) => c.state === "applied");
  if (!applied.length)
    return (
      !!m.effective_model_snapshot &&
      m.effective_model_snapshot.digest !== m.model_snapshot?.digest
    );
  const effective = m.effective_model_snapshot;
  return (
    !effective ||
    effective.schema_version !== 2 ||
    effective.digest !== applied.at(-1)!.replacement_digest ||
    !effective.roles ||
    Object.keys(effective.roles).length !== 7 ||
    roles.some((role) => {
      const item = effective.roles[role],
        original = m.model_snapshot?.roles[role];
      if (!item?.profile || !original?.profile) return true;
      if (
        [
          "id",
          "name",
          "model",
          "protocol",
          "account_id",
          "route_id",
          "credential_version",
        ].some(
          (key) =>
            item.profile[key as keyof typeof item.profile] !==
            original.profile[key as keyof typeof original.profile],
        )
      )
        return true;
      if (!item.profile.account_id)
        return !!item.account || !!item.route || !!item.authorization;
      return (
        !item.account ||
        item.account.id !== item.profile.account_id ||
        item.account.revision !== item.profile.account_revision ||
        !item.route ||
        item.route.id !== item.profile.route_id ||
        item.route.account_id !== item.profile.account_id ||
        item.route.protocol !== item.profile.protocol ||
        (item.route.adapter === "openai_siwc_responses" &&
          (!item.authorization?.id ||
            item.authorization.account_id !== item.profile.account_id ||
            !Array.isArray(item.authorization.scopes)))
      );
    })
  );
}
export function continuationState(
  c: ModelContinuationMaterial,
  revision: number,
): string {
  if (!validContinuation(c)) return "invalid";
  if (c.state === "applied") return "applied";
  if (
    Date.parse(c.expires_at) <= Date.now() ||
    Date.parse(c.authorization_expires_at) <= Date.now()
  )
    return "expired";
  if (revision !== c.prepared_revision + (c.approval ? 2 : 1)) return "stale";
  return c.state;
}
export function effectiveModelSnapshot(m: Mission) {
  if (invalidContinuation(m)) return undefined;
  return m.effective_model_snapshot ?? m.model_snapshot;
}
