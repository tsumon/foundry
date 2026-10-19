export interface CatalogMeta {
  id: string;
  revision: number;
  created_at: string;
  updated_at: string;
}

export interface Project extends CatalogMeta {
  name: string;
  description: string;
}

export interface DeliveryTarget extends CatalogMeta {
  project_id: string;
  name: string;
  platform: "windows" | "macos" | "android" | "ios";
  format: "exe" | "dmg" | "apk" | "ipa";
  architecture: string;
  distribution: string;
}

export interface Environment extends CatalogMeta {
  project_id: string;
  name: string;
  tier: "local" | "dev" | "staging" | "production";
}

export interface Resource extends CatalogMeta {
  name: string;
  kind: "service" | "database" | "storage" | "domain" | "runner";
  owner_project_id: string | null;
  provider: "manual" | "local" | "aws" | "gcp" | "azure" | "apple";
  account: string;
  region: string;
  external_id: string;
  protected: true;
  retention: "retain";
}

export interface ResourceBinding extends CatalogMeta {
  project_id: string;
  environment_id: string;
  resource_id: string;
  purpose: string;
}

export interface MissionProjectLink {
  mission_id: string;
  project_id: string;
  linked_at: string;
}

export interface CatalogSnapshot {
  schema_version: 1;
  revision: number;
  projects: Project[];
  targets: DeliveryTarget[];
  environments: Environment[];
  resources: Resource[];
  bindings: ResourceBinding[];
  mission_links: MissionProjectLink[];
}

export interface CatalogEvent {
  seq: number;
  catalog_revision: number;
  kind: string;
  entity_ids: string[];
  created_at: string;
}

export type CatalogCommand =
  | {
      kind: "project.create";
      input: { name: string; description: string };
    }
  | {
      kind: "project.update";
      input: { id: string; name: string; description: string };
    }
  | {
      kind: "target.create";
      input: {
        project_id: string;
        name: string;
        platform: DeliveryTarget["platform"];
        format: DeliveryTarget["format"];
        architecture: string;
        distribution: string;
      };
    }
  | { kind: "target.rename"; input: { id: string; name: string } }
  | {
      kind: "environment.create";
      input: {
        project_id: string;
        name: string;
        tier: Environment["tier"];
      };
    }
  | { kind: "environment.rename"; input: { id: string; name: string } }
  | {
      kind: "resource.create";
      input: {
        name: string;
        kind: Resource["kind"];
        owner_project_id: string | null;
        provider: Resource["provider"];
        account: string;
        region: string;
        external_id: string;
      };
    }
  | { kind: "resource.rename"; input: { id: string; name: string } }
  | {
      kind: "resource.bind";
      input: {
        project_id: string;
        environment_id: string;
        resource_id: string;
        purpose: string;
      };
    }
  | { kind: "resource.unbind"; input: { id: string } }
  | {
      kind: "mission.link";
      input: { mission_id: string; project_id: string };
    }
  | {
      kind: "mission.relink";
      input: {
        mission_id: string;
        from_project_id: string;
        to_project_id: string;
      };
    }
  | {
      kind: "mission.unlink";
      input: { mission_id: string; project_id: string };
    };
