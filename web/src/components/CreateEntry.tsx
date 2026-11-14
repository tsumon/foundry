import { useState, type ComponentProps } from "react";
import { CreateMission } from "./CreateMission";
import { IdeaCreate } from "./IdeaCreate";

export function CreateEntry(props: ComponentProps<typeof CreateMission>) {
  const [entry, setEntry] = useState<"idea" | "repository">(
    props.initialRepository ? "repository" : "idea",
  );
  const [visitedRepository, setVisitedRepository] = useState(
    !!props.initialRepository,
  );
  const [busy, setBusy] = useState(false);
  const changed = (value: boolean) => {
    setBusy(value);
    props.onBusyChange?.(value);
  };
  return (
    <>
      <div className="creation-entry" role="tablist" aria-label="创建方式">
        <button
          role="tab"
          aria-selected={entry === "idea"}
          disabled={busy}
          onClick={() => setEntry("idea")}
        >
          从想法创建
        </button>
        <button
          role="tab"
          aria-selected={entry === "repository"}
          disabled={busy}
          onClick={() => {
            setVisitedRepository(true);
            setEntry("repository");
          }}
        >
          改进已有仓库
        </button>
      </div>
      <div hidden={entry !== "idea"}>
        <IdeaCreate
          {...props}
          active={props.active !== false && entry === "idea"}
          onBusyChange={changed}
        />
      </div>
      {visitedRepository && (
        <div hidden={entry !== "repository"}>
          <CreateMission
            {...props}
            active={props.active !== false && entry === "repository"}
            onBusyChange={changed}
          />
        </div>
      )}
    </>
  );
}
