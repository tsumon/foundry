import { afterEach, describe, expect, it, vi } from "vitest";
import { APIError } from "./api";
import { catalogApi } from "./catalogApi";
import type { CatalogCommand, CatalogSnapshot } from "./catalogTypes";

type SupportedCommandKind =
  | "project.create"
  | "project.update"
  | "target.create"
  | "target.rename"
  | "environment.create"
  | "environment.rename"
  | "resource.create"
  | "resource.rename"
  | "resource.bind"
  | "resource.unbind"
  | "mission.link"
  | "mission.relink"
  | "mission.unlink";
type Equal<A, B> =
  (<T>() => T extends A ? 1 : 2) extends <T>() => T extends B ? 1 : 2
    ? (<T>() => T extends B ? 1 : 2) extends <T>() => T extends A ? 1 : 2
      ? true
      : false
    : false;
type Assert<T extends true> = T;
type CatalogCommandKindsAreExact = Assert<
  Equal<CatalogCommand["kind"], SupportedCommandKind>
>;

const emptyCatalog: CatalogSnapshot = {
  schema_version: 1,
  revision: 0,
  projects: [],
  targets: [],
  environments: [],
  resources: [],
  bindings: [],
  mission_links: [],
};

const response = (value: unknown, status = 200) =>
  new Response(JSON.stringify(value), {
    status,
    headers: { "Content-Type": "application/json" },
  });

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("catalogApi", () => {
  it("loads the authenticated catalog without bootstrapping another session", async () => {
    const fetcher = vi
      .fn()
      .mockResolvedValue(response({ catalog: emptyCatalog }));
    vi.stubGlobal("fetch", fetcher);

    await expect(catalogApi.snapshot()).resolves.toEqual({
      catalog: emptyCatalog,
    });
    expect(fetcher).toHaveBeenCalledTimes(1);
    expect(fetcher).toHaveBeenCalledWith("/api/catalog", {
      credentials: "same-origin",
      headers: {},
    });
  });

  it("sends the caller revision and the exact typed command envelope", async () => {
    const fetcher = vi.fn().mockResolvedValue(
      response({
        catalog: { ...emptyCatalog, revision: 1 },
      }),
    );
    vi.stubGlobal("fetch", fetcher);

    const command: CatalogCommand = {
      kind: "resource.create",
      input: {
        name: "shared-db",
        kind: "database",
        owner_project_id: null,
        provider: "manual",
        account: "",
        region: "",
        external_id: "",
      },
    };
    await catalogApi.command(7, command);

    expect(fetcher).toHaveBeenCalledWith("/api/catalog/commands", {
      method: "POST",
      body: JSON.stringify({
        expected_revision: 7,
        kind: "resource.create",
        input: command.input,
      }),
      credentials: "same-origin",
      headers: { "Content-Type": "application/json" },
    });
  });

  it("requests bounded catalog events with explicit defaults", async () => {
    const fetcher = vi
      .fn()
      .mockImplementation(() => Promise.resolve(response({ events: [] })));
    vi.stubGlobal("fetch", fetcher);

    await catalogApi.events();
    await catalogApi.events(12, 25);

    expect(fetcher.mock.calls.map(([path]) => path)).toEqual([
      "/api/catalog/events?after=0&limit=100",
      "/api/catalog/events?after=12&limit=25",
    ]);
  });

  it("propagates API failures without inventing catalog data", async () => {
    const fetcher = vi
      .fn()
      .mockResolvedValue(response({ error: { message: "unavailable" } }, 500));
    vi.stubGlobal("fetch", fetcher);

    await expect(catalogApi.snapshot()).rejects.toEqual(
      expect.objectContaining<Partial<APIError>>({
        status: 500,
        code: "request_failed",
        message: "unavailable",
      }),
    );
  });

  it("serializes every member of the closed thirteen-command union", async () => {
    const fetcher = vi
      .fn()
      .mockImplementation(() =>
        Promise.resolve(response({ catalog: emptyCatalog })),
      );
    vi.stubGlobal("fetch", fetcher);
    const commands: CatalogCommand[] = [
      { kind: "project.create", input: { name: "p", description: "" } },
      {
        kind: "project.update",
        input: { id: "p1", name: "p", description: "d" },
      },
      {
        kind: "target.create",
        input: {
          project_id: "p1",
          name: "mac",
          platform: "macos",
          format: "dmg",
          architecture: "universal",
          distribution: "direct",
        },
      },
      { kind: "target.rename", input: { id: "t1", name: "mac" } },
      {
        kind: "environment.create",
        input: { project_id: "p1", name: "prod", tier: "production" },
      },
      { kind: "environment.rename", input: { id: "e1", name: "prod" } },
      {
        kind: "resource.create",
        input: {
          name: "db",
          kind: "database",
          owner_project_id: null,
          provider: "manual",
          account: "",
          region: "",
          external_id: "",
        },
      },
      { kind: "resource.rename", input: { id: "r1", name: "db" } },
      {
        kind: "resource.bind",
        input: {
          project_id: "p1",
          environment_id: "e1",
          resource_id: "r1",
          purpose: "primary-db",
        },
      },
      { kind: "resource.unbind", input: { id: "b1" } },
      { kind: "mission.link", input: { mission_id: "m1", project_id: "p1" } },
      {
        kind: "mission.relink",
        input: {
          mission_id: "m1",
          from_project_id: "p1",
          to_project_id: "p2",
        },
      },
      { kind: "mission.unlink", input: { mission_id: "m1", project_id: "p2" } },
    ];

    for (const [revision, command] of commands.entries()) {
      await catalogApi.command(revision, command);
    }

    expect(
      fetcher.mock.calls.map(([path, init], revision) => ({
        path,
        credentials: init.credentials,
        body: JSON.parse(init.body),
        revision,
      })),
    ).toEqual(
      commands.map((command, revision) => ({
        path: "/api/catalog/commands",
        credentials: "same-origin",
        body: {
          expected_revision: revision,
          kind: command.kind,
          input: command.input,
        },
        revision,
      })),
    );
    expect(commands.map(({ kind }) => kind)).toEqual([
      "project.create",
      "project.update",
      "target.create",
      "target.rename",
      "environment.create",
      "environment.rename",
      "resource.create",
      "resource.rename",
      "resource.bind",
      "resource.unbind",
      "mission.link",
      "mission.relink",
      "mission.unlink",
    ]);
  });
});
